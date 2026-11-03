#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${GITHUB_SHA:-}" =~ ^[0-9a-f]{40}$ ]]
image_dir="${IMAGE_DIR:-$RUNNER_TEMP/helm-container}"
install -d -m 0700 "$image_dir"
if [[ -n "${PROMOTION_IMAGE:-}" ]]; then
  [[ "$GITHUB_EVENT_NAME" == push && "$GITHUB_REF" == refs/heads/main ]]
  [[ "$PROMOTION_IMAGE" =~ ^ghcr\.io/kanterlabs/helm@sha256:[0-9a-f]{64}$ ]]
  docker pull "$PROMOTION_IMAGE"
  revision=$(docker image inspect "$PROMOTION_IMAGE" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}')
  [[ "$revision" == "$GITHUB_SHA" ]]
  docker tag "$PROMOTION_IMAGE" "helm:$GITHUB_SHA"
else
  docker build --pull --label org.opencontainers.image.source=https://github.com/KanterLabs/helm --label "org.opencontainers.image.revision=$GITHUB_SHA" --tag "helm:${GITHUB_SHA}" .
fi
docker image inspect "helm:${GITHUB_SHA}" >/dev/null
container_id=$(docker run --detach "helm:${GITHUB_SHA}")
cleanup() { docker rm --force "$container_id" >/dev/null 2>&1 || true; }
trap cleanup EXIT
ready=false
for _ in $(seq 1 30); do
  if docker exec "$container_id" /usr/local/bin/helm healthcheck >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  docker logs "$container_id"
  exit 1
fi
docker exec "$container_id" /usr/local/bin/helm healthcheck
docker cp "$container_id:/usr/local/bin/codex" "$image_dir/codex"
test -x "$image_dir/codex"
(cd "$image_dir" && sha256sum codex > codex.sha256)
docker exec "$container_id" /usr/local/bin/cloudflared --version
docker exec "$container_id" /usr/local/bin/cloudflared tunnel --help >/dev/null
docker cp "$container_id:/usr/local/bin/cloudflared" "$image_dir/cloudflared"
test -x "$image_dir/cloudflared"
(cd "$image_dir" && sha256sum cloudflared > cloudflared.sha256)
printf 'container_smoke=ok\n'
image_tar="$image_dir/helm-${GITHUB_SHA}.tar"
docker save --output "$image_tar" "helm:${GITHUB_SHA}"
docker run --rm \
  --volume "$image_dir:/scan:ro" \
  aquasec/trivy:0.67.2@sha256:e2b22eac59c02003d8749f5b8d9bd073b62e30fefaef5b7c8371204e0a4b0c08 \
  image --input "/scan/helm-${GITHUB_SHA}.tar" \
  --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 --no-progress
gzip -n "$image_tar"
image_archive=${image_tar##*/}.gz
(cd "$image_dir" && sha256sum "$image_archive" > "$image_archive.sha256")
