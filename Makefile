APP := helm
ROOT_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
WEB_DIR := $(ROOT_DIR)/web
DIST_DIR := $(ROOT_DIR)/dist
FRONTEND_DIST := $(ROOT_DIR)/internal/webassets/dist
FRONTEND_COMPAT_DIST := $(ROOT_DIR)/internal/frontend/dist
GO ?= go
NPM ?= npm
SHA ?= $(shell git rev-parse HEAD 2>/dev/null || true)

.PHONY: all web-install web-check web-test web-build openapi openapi-check frontend build test focus-test e2e vet lint \
	neglect-binary neglect-rollback neglect-test neglect-red neglect-corpus neglect-db neglect-bench \
	docker-build compose-up compose-down bundle clean release-status publish-beta prepare-release finish-release release-test

all: build

web-install:
	cd $(WEB_DIR) && $(NPM) ci

web-check: web-install
	cd $(WEB_DIR) && $(NPM) run check

web-test: e2e

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
	cd $(WEB_DIR) && $(NPM) run build
	rm -rf $(FRONTEND_DIST) $(FRONTEND_COMPAT_DIST)
	install -d $(FRONTEND_DIST)
	cp -a $(WEB_DIR)/dist/. $(FRONTEND_DIST)/
	install -d $(FRONTEND_COMPAT_DIST)
	cp -a $(WEB_DIR)/dist/. $(FRONTEND_COMPAT_DIST)/

build: frontend
	install -d $(DIST_DIR)
	$(GO) build -trimpath -ldflags="-s -w" -o $(DIST_DIR)/$(APP) ./cmd/$(APP)

test: e2e

# Synthetic API/CLI receipts live under .artifacts/focus-<unique run>/.
focus-test:
	install -d .artifacts
	$(GO) build -o .artifacts/focus-helm ./cmd/helm
	python3 -m unittest discover -s skills/helm/scripts -p 'test_*.py'
	python3 test/focus/run.py --binary .artifacts/focus-helm

# Neglect signal (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md). Receipts: .artifacts/neglect-<run>/.
# neglect-test is strict. Since TC-381 every case 1-20 is a guard, so
# neglect-red is as strict (test/neglect/FAILURES.md, CLI.md).
# Cases 6, 17 and 20 need a retained pre-signal binary: by default main 1c54493
# is built once under $$TMPDIR; pass NEGLECT_ROLLBACK=<binary> to use another.
# Case 14 runs the helm_testclock build (test/neglect/API.md).
NEGLECT_ROLLBACK_SHA = 1c54493
NEGLECT_ROLLBACK_BUILT = $(or $(TMPDIR),/var/tmp/agent-scratch-1000/tmp)/helm-neglect-rollback/helm-$(NEGLECT_ROLLBACK_SHA)
NEGLECT_ROLLBACK ?= $(NEGLECT_ROLLBACK_BUILT)
NEGLECT_ARGS = --binary .artifacts/neglect-helm --clock-binary .artifacts/neglect-helm-testclock \
	--rollback-binary $(NEGLECT_ROLLBACK)
neglect-binary: neglect-rollback
	install -d .artifacts
	$(GO) build -o .artifacts/neglect-helm ./cmd/helm
	$(GO) build -tags helm_testclock -o .artifacts/neglect-helm-testclock ./cmd/helm

neglect-rollback:
ifeq ($(NEGLECT_ROLLBACK),$(NEGLECT_ROLLBACK_BUILT))
	@test -x $(NEGLECT_ROLLBACK_BUILT) || { set -e; src=$(dir $(NEGLECT_ROLLBACK_BUILT))src; \
		rm -rf $$src; install -d $$src; git archive $(NEGLECT_ROLLBACK_SHA) | tar -x -C $$src; \
		(cd $$src && $(GO) build -o $(NEGLECT_ROLLBACK_BUILT) ./cmd/helm); rm -rf $$src; }
endif

neglect-test: neglect-binary neglect-corpus
	python3 test/neglect/run.py $(NEGLECT_ARGS)

neglect-red: neglect-binary neglect-corpus
	python3 test/neglect/run.py $(NEGLECT_ARGS) --expect-red

neglect-corpus:
	python3 test/neglect/corpus_run.py

# Full-size benchmark database (20,000 tasks / 500,000 events) under $$TMPDIR.
neglect-db: neglect-binary
	python3 test/neglect/populate.py --binary .artifacts/neglect-helm

# Cases 8 and 16 on the neglect-db file with the budgets recorded in
# test/neglect/PROJECTOR.md. Override NEGLECT_BENCH_DB for another file.
NEGLECT_BENCH_DB ?= $(or $(TMPDIR),/var/tmp/agent-scratch-1000/tmp)/helm-neglect-bench/bench-20000t-500000e-s377.db
NEGLECT_BENCH_BUDGETS ?= --seed-budget 180 --write-budget-ms 1000 --rebuild-budget-ms 20 --lock-budget-ms 250
neglect-bench: neglect-binary
	python3 test/neglect/run.py $(NEGLECT_ARGS) --expect-red --case 8 --case 16 \
		--populated-db $(NEGLECT_BENCH_DB) --scale-tasks 20000 --scale-events 500000 $(NEGLECT_BENCH_BUDGETS)

e2e: web-install
	cd $(WEB_DIR) && $(NPM) run e2e:install
	cd $(WEB_DIR) && $(NPM) test

vet:
	$(GO) vet ./...

lint:
	@test -z "$$(gofmt -l cmd internal)" || { echo 'gofmt required' >&2; exit 1; }
	@cd $(WEB_DIR) && $(NPM) run openapi:check
	@cd $(WEB_DIR) && $(NPM) run docs:check
	@bash -n deploy/*.sh deploy/helm-deploy-gateway deploy/cloudflare-connect-relay/deploy.sh
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

clean:
	rm -rf $(DIST_DIR) $(FRONTEND_DIST) $(FRONTEND_COMPAT_DIST)

# Preparation creates a PR into main; only its reviewed merge deploys production.
release-status:
	python3 deploy/release.py status

publish-beta:
	python3 deploy/release.py beta

prepare-release:
	@test -n "$(VERSION)" || { echo 'Use make prepare-release VERSION=vX.Y.Z' >&2; exit 64; }
	python3 deploy/release.py prepare "$(VERSION)"

finish-release:
	@test -n "$(VERSION)" || { echo 'Use make finish-release VERSION=vX.Y.Z' >&2; exit 64; }
	python3 deploy/release.py finish "$(VERSION)"

release-test:
	python3 test/release-pipeline/run.py
