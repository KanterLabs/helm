APP := helm
ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
WEB_DIR := $(ROOT_DIR)/web
DIST_DIR := $(ROOT_DIR)/dist
FRONTEND_DIST := $(ROOT_DIR)/internal/webassets/dist
FRONTEND_COMPAT_DIST := $(ROOT_DIR)/internal/frontend/dist
GO ?= go
NPM ?= npm
PYTHON ?= python3
SHA ?= $(shell git rev-parse HEAD 2>/dev/null || true)
RELEASE_CONFIG ?= $(ROOT_DIR)/packaging/release.json
RELEASE_BUILDER ?= $(ROOT_DIR)/ci/build-release.py
RELEASE_VERSION ?= $(shell $(PYTHON) -c 'import json; print(json.load(open("$(RELEASE_CONFIG)", encoding="utf-8"))["version"])' 2>/dev/null || true)
RELEASE_OUT ?= $(DIST_DIR)/release
RELEASE_STAGING ?= $(DIST_DIR)/staging
RELEASE_PLATFORM ?= linux/$(shell $(GO) env GOARCH 2>/dev/null || printf amd64)
RELEASE_PACKAGE_FORMAT ?= all
OCI_PLATFORM ?= $(RELEASE_PLATFORM)
OCI_PLATFORMS ?= linux/amd64 linux/arm64

.PHONY: all web-install web-check web-test web-build openapi openapi-check frontend build test vet lint \
	docker-build compose-up compose-down bundle clean release-config-check release-archives \
	release-packages release-oci release-oci-multiarch release-e2e release-local

all: build

web-install:
	cd $(WEB_DIR) && $(NPM) ci

web-check: web-install
	cd $(WEB_DIR) && $(NPM) run check

web-test: web-install
	cd $(WEB_DIR) && $(NPM) test

web-build: web-install
	cd $(WEB_DIR) && $(NPM) run build

openapi: web-install
	cd $(WEB_DIR) && $(NPM) run openapi:generate

openapi-check: web-install
	cd $(WEB_DIR) && $(NPM) run openapi:check


# Keep this order in one target so an embedded frontend can never be stale.
frontend:
	cd $(WEB_DIR) && $(NPM) ci
	cd $(WEB_DIR) && $(NPM) run openapi:check
	cd $(WEB_DIR) && $(NPM) run check
	cd $(WEB_DIR) && $(NPM) test
	cd $(WEB_DIR) && $(NPM) run build
	rm -rf $(FRONTEND_DIST) $(FRONTEND_COMPAT_DIST)
	install -d $(FRONTEND_DIST)
	cp -a $(WEB_DIR)/dist/. $(FRONTEND_DIST)/
	install -d $(FRONTEND_COMPAT_DIST)
	cp -a $(WEB_DIR)/dist/. $(FRONTEND_COMPAT_DIST)/

build: frontend
	install -d $(DIST_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(DIST_DIR)/$(APP) ./cmd/$(APP)

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	@test -z "$$(gofmt -l cmd internal)" || { echo 'gofmt required' >&2; exit 1; }
	@cd $(WEB_DIR) && $(NPM) run openapi:check
	@bash -n deploy/*.sh deploy/helm-deploy-gateway
	@./deploy/test-deployment-security.sh

docker-build:
	docker build --tag $(APP):$(if $(SHA),$(SHA),local) .

compose-up:
	docker compose up --build --detach

compose-down:
	docker compose down

bundle: build
	@test -n "$(SHA)" || { echo 'bundle requires a git SHA' >&2; exit 64; }
	./deploy/build-bundle.sh $(SHA)

# Release entrypoints deliberately keep compilation/package/image work
# separate. CI can schedule them on native architecture builders and the
# downloaded-artifact E2E test can consume their combined output afterwards.
release-config-check:
	$(PYTHON) -m json.tool $(RELEASE_CONFIG) >/dev/null
	$(PYTHON) -c 'import json; c=json.load(open("$(RELEASE_CONFIG)", encoding="utf-8")); assert c["version"] == "$(RELEASE_VERSION)"; assert {p["os"]+"/"+p["arch"] for p in c["platforms"]} >= {"linux/amd64", "linux/arm64"}'

release-archives: release-config-check
	@test -f "$(RELEASE_BUILDER)" || { echo "missing release builder: $(RELEASE_BUILDER)" >&2; exit 64; }
	GO="$(GO)" NPM="$(NPM)" $(PYTHON) $(RELEASE_BUILDER) archives --config $(RELEASE_CONFIG) --output $(RELEASE_OUT) --staging-root $(RELEASE_STAGING) --platform $(RELEASE_PLATFORM)

release-packages: release-config-check
	@test -d "$(RELEASE_STAGING)" || { echo "missing release staging root: $(RELEASE_STAGING)" >&2; exit 64; }
	@test -f "$(RELEASE_BUILDER)" || { echo "missing release builder: $(RELEASE_BUILDER)" >&2; exit 64; }
	GO="$(GO)" NPM="$(NPM)" $(PYTHON) $(RELEASE_BUILDER) packages --config $(RELEASE_CONFIG) --staging-root $(RELEASE_STAGING) --output $(RELEASE_OUT) --format $(RELEASE_PACKAGE_FORMAT) --platform $(RELEASE_PLATFORM)

release-oci: release-config-check
	@test -f "$(RELEASE_BUILDER)" || { echo "missing release builder: $(RELEASE_BUILDER)" >&2; exit 64; }
	$(PYTHON) $(RELEASE_BUILDER) oci --config $(RELEASE_CONFIG) --output $(RELEASE_OUT) --platform $(OCI_PLATFORM)

release-oci-multiarch: release-config-check
	@test -f "$(RELEASE_BUILDER)" || { echo "missing release builder: $(RELEASE_BUILDER)" >&2; exit 64; }
	$(PYTHON) $(RELEASE_BUILDER) oci --config $(RELEASE_CONFIG) --output $(RELEASE_OUT) $(foreach platform,$(OCI_PLATFORMS),--platform $(platform))

release-e2e:
	HELM_VERSION=$(RELEASE_VERSION) ./ci/test-release-artifacts.sh $(RELEASE_OUT) $(RELEASE_VERSION)

release-local: release-config-check release-archives release-packages release-oci

clean:
	rm -rf $(DIST_DIR) $(FRONTEND_DIST) $(FRONTEND_COMPAT_DIST)
