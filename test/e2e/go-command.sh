#!/usr/bin/env bash
# Pinned Go fallback for local machines without Go; prepared CI uses native Go.
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=${HELM_E2E_DOCKER_WORK_DIR:?missing private Docker build workspace}
parent=${HELM_E2E_DOCKER_BUNDLE_PARENT:?missing prepared bundle parent}
[[ "$work" = /* && "$parent" = /* && -d "$work" ]] || exit 64
mkdir -p "$work/go-cache" "$parent"
state=$(mktemp -d "$work/.docker-go.XXXXXX")
cidfile="$state/container.cid"
docker_pid=
cleanup() {
  local status=$? cid=
  trap - EXIT
  trap '' TERM INT
  # Removing a stopped client does not remove a daemon-owned container. Scope
  # cleanup to the CID Docker wrote in this invocation's private directory.
  if [[ -s "$cidfile" ]]; then
    IFS= read -r cid < "$cidfile" || true
    if [[ "$cid" =~ ^[0-9a-f]{64}$ ]]; then
      timeout --kill-after=0.2s 1s docker rm -f "$cid" >/dev/null 2>&1 || true
    fi
  fi
  if [[ -n "$docker_pid" ]]; then
    kill -KILL "$docker_pid" >/dev/null 2>&1 || true
    wait "$docker_pid" >/dev/null 2>&1 || true
  fi
  rm -rf -- "$state"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT
args=(--rm --cidfile "$cidfile" --user "$(id -u):$(id -g)" --volume "$root:$root"
  --workdir "$root"
  --env "GOPATH=$work/go-cache" --env "GOCACHE=$work/go-cache/build"
  --env "GOMODCACHE=$work/go-cache/pkg/mod" --env GOTOOLCHAIN=local)
[[ "$work" == "$root" ]] || args+=(--volume "$work:$work")
[[ "$parent" == "$root" || "$parent" == "$work" ]] || args+=(--volume "$parent:$parent")
for name in GOMAXPROCS GOFLAGS GOOS GOARCH GOEXPERIMENT CGO_ENABLED CGO_CFLAGS CGO_CPPFLAGS CGO_CXXFLAGS CGO_LDFLAGS; do
  [[ ! -v "$name" ]] || args+=(--env "$name")
done
docker run "${args[@]}" \
  golang:1.26.10-bookworm@sha256:fe3da92a5cb2881e4aa75e625cb0b7c50571632fb4cf340cbe53e6ccdcffdd51 \
  go "$@" &
docker_pid=$!
status=0
wait "$docker_pid" || status=$?
docker_pid=
exit "$status"
