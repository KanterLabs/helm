#!/usr/bin/env bash
# Scope is a validated planner enum, never a command or path supplied by a PR.
set -Eeuo pipefail
scope=${1:?validation scope required}
case "$scope" in docs|fast|focused|full|promotion) ;; *) exit 64 ;; esac
(
  cd web
  npm ci
  if [[ "$scope" == docs ]]; then
    npm run docs:check
  else
    npm audit --audit-level=high
    npm run openapi:check
    npm run check
    if [[ "$scope" == full ]]; then npm run build; fi
  fi
)
python3 -m unittest discover -s test/ci -p 'test_*.py'
[[ "$scope" != docs ]] || exit 0
if [[ "$scope" == full ]]; then
  rm -rf -- internal/webassets/dist internal/frontend/dist
  install -d internal/webassets/dist internal/frontend/dist dist
  cp -a web/dist/. internal/webassets/dist/
  cp -a web/dist/. internal/frontend/dist/
fi
test -z "$(gofmt -l cmd internal)"
GOSUMDB=sum.golang.org go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.10
go vet ./...
if [[ "$scope" != full && "$scope" != promotion ]]; then exit 0; fi
GOSUMDB=sum.golang.org go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
python3 -m py_compile deploy/coolify-deploy.py
bash -n deploy/*.sh deploy/helm-deploy-gateway
./deploy/test-deployment-security.sh
if [[ "$scope" == full ]]; then
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/helm ./cmd/helm
fi
docker compose config >/dev/null
