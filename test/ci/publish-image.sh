#!/usr/bin/env bash
# Publication is a separate trusted job. Loading a PR archive never authorizes it.
set -Eeuo pipefail
[[ "${GITHUB_EVENT_NAME:-}" == push ]]
[[ "${GITHUB_REF:-}" == refs/heads/beta || "${GITHUB_REF:-}" == refs/heads/main ]]
[[ "${GITHUB_SHA:-}" =~ ^[0-9a-f]{40}$ ]]
image_dir=${IMAGE_DIR:?image directory required}
archive="helm-${GITHUB_SHA}.tar.gz"
[[ -f "$image_dir/$archive" && ! -L "$image_dir/$archive" ]]
[[ -f "$image_dir/$archive.sha256" && ! -L "$image_dir/$archive.sha256" ]]
(cd "$image_dir" && sha256sum --check --strict "$archive.sha256")
docker load --input "$image_dir/$archive"
revision=$(docker image inspect "helm:$GITHUB_SHA" --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}')
[[ "$revision" == "$GITHUB_SHA" ]]
image=ghcr.io/kanterlabs/helm
docker_config=$(mktemp -d "$RUNNER_TEMP/helm-ghcr.XXXXXX")
chmod 0700 "$docker_config"
cleanup_registry() { rm -rf -- "$docker_config"; }
trap cleanup_registry EXIT
export DOCKER_CONFIG="$docker_config"
printf '%s' "${GHCR_TOKEN:?registry credential required}" | docker login ghcr.io --username "$GITHUB_ACTOR" --password-stdin
docker tag "helm:$GITHUB_SHA" "$image:$GITHUB_SHA"
docker push "$image:${GITHUB_SHA}"
digest=$(docker image inspect "$image:$GITHUB_SHA" --format '{{range .RepoDigests}}{{println .}}{{end}}' | sed -n 's|^ghcr.io/kanterlabs/helm@||p')
[[ "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]
printf 'image=%s@%s\n' "$image" "$digest" >> "$GITHUB_OUTPUT"
