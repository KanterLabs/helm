#!/usr/bin/env bash
# Opt-in real-forge proof (WP5 part 2 of docs/GITEA_OAUTH_PLAN.md): start real
# Gitea and Forgejo containers and drive Helm's real binary through OAuth
# sign-in against each. Needs Docker and network access to pull images. Never
# part of routine CI or test/e2e/run.sh. See README.md and FAILURES.md.
set -Eeuo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/../.." && pwd)
read -r -a images <<< "${HELM_GITEAPROOF_IMAGES:-gitea/gitea:28.3.0 codeberg.org/forgejo/forgejo:17}"
scratch=${TMPDIR:-/var/tmp}
[[ "$scratch" = /* && -d "$scratch" ]] || { printf 'TMPDIR must be an existing absolute directory\n' >&2; exit 64; }
for tool in docker python3 openssl curl; do
  command -v "$tool" >/dev/null || { printf 'Missing required tool: %s\n' "$tool" >&2; exit 69; }
done
docker info >/dev/null 2>&1 || { printf 'Docker is not reachable\n' >&2; exit 69; }

run_id="giteaproof-$(date -u +%Y%m%d%H%M%S)-$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"
artifact_dir=${HELM_GITEAPROOF_ARTIFACT_DIR:-"$root/.artifacts/giteaproof/$run_id"}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
tmp=$(mktemp -d "$scratch/giteaproof.XXXXXX")
chmod 0700 "$tmp"
go_work=${HELM_GITEAPROOF_GO_WORK:-"$tmp/go-work"}
mkdir -p "$go_work" "$tmp/bin" "$tmp/certs"
containers_file="$tmp/containers"
pulled_file="$tmp/pulled-images"
pid_file="$tmp/helm.pids"
: > "$containers_file"; : > "$pulled_file"; : > "$pid_file"
started_at=$(date +%s)
proof_pid=

cleanup() {
  local status=$? pid cid image leftover
  trap - EXIT
  trap '' TERM INT
  set +e
  if [[ -n "$proof_pid" ]]; then
    kill -TERM "$proof_pid" 2>/dev/null; wait "$proof_pid" 2>/dev/null
  fi
  # Helm processes this run started, and only while they still run our binary.
  while read -r pid; do
    [[ "$pid" =~ ^[0-9]+$ ]] || continue
    if [[ "$(readlink "/proc/$pid/exe" 2>/dev/null)" == "$tmp/bin/helm" ]]; then
      kill -TERM "$pid" 2>/dev/null; sleep 1; kill -KILL "$pid" 2>/dev/null
    fi
  done < "$pid_file"
  while read -r cid; do
    [[ "$cid" =~ ^[0-9a-f]{64}$ ]] && docker rm -f -v "$cid" >/dev/null 2>&1
  done < "$containers_file"
  leftover=$(docker ps -aq --filter "label=helm.giteaproof.run=$run_id")
  [[ -z "$leftover" ]] || { printf 'Containers left behind: %s\n' "$leftover" >&2; status=1; }
  while read -r image; do
    [[ -n "$image" ]] || continue
    if docker image inspect "$image" >/dev/null 2>&1; then
      docker rmi "$image" >/dev/null 2>&1 && printf 'Removed pulled image %s\n' "$image" \
        || { printf 'Could not remove pulled image %s (in use?)\n' "$image" >&2; status=1; }
    fi
  done < "$pulled_file"
  chmod -R u+w "$tmp" 2>/dev/null
  rm -rf -- "$tmp"
  [[ ! -e "$tmp" ]] || { printf 'Temporary directory left behind: %s\n' "$tmp" >&2; status=1; }
  printf 'run_id=%s\nexit_status=%s\nelapsed_seconds=%s\n' "$run_id" "$status" "$(( $(date +%s) - started_at ))" \
    > "$artifact_dir/run.metadata"
  printf 'Artifacts: %s\n' "$artifact_dir"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

free_port() {
  python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

printf '== Building Helm (%s)\n' "$(git -C "$root" rev-parse --short HEAD)"
HELM_E2E_DOCKER_WORK_DIR="$go_work" HELM_E2E_DOCKER_BUNDLE_PARENT="$tmp/bin" CGO_ENABLED=0 \
  "$root/test/e2e/go-command.sh" build -trimpath -o "$tmp/bin/helm" ./cmd/helm
[[ -x "$tmp/bin/helm" ]] || { printf 'Helm binary was not built\n' >&2; exit 1; }

# A throwaway certificate authority, so the forges speak https on 127.0.0.1
# and a bare host (which Helm completes with https) can be proven for real.
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj "/CN=giteaproof CA" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -addext "subjectKeyIdentifier=hash" -keyout "$tmp/ca.key" -out "$tmp/ca.pem" >/dev/null 2>&1
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=127.0.0.1" \
  -keyout "$tmp/certs/forge.key" -out "$tmp/forge.csr" >/dev/null 2>&1
printf '%s\n' subjectAltName=IP:127.0.0.1 basicConstraints=critical,CA:FALSE keyUsage=critical,digitalSignature \
  extendedKeyUsage=serverAuth subjectKeyIdentifier=hash authorityKeyIdentifier=keyid:always > "$tmp/ext"
openssl x509 -req -in "$tmp/forge.csr" -CA "$tmp/ca.pem" -CAkey "$tmp/ca.key" -CAcreateserial -days 2 \
  -extfile "$tmp/ext" -out "$tmp/certs/forge.pem" >/dev/null 2>&1
chmod 0755 "$tmp/certs"; chmod 0644 "$tmp/certs/forge.pem" "$tmp/certs/forge.key"

start_forge() { # family image -> sets cid port base nonce
  local family=$1 image=$2 prefix attempt name err="$tmp/docker.err"
  prefix=GITEA; [[ "$family" == forgejo ]] && prefix=FORGEJO
  for attempt in 1 2 3 4 5; do
    port=$(free_port)
    name="$run_id-$family-$attempt"
    nonce="proof-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
    base="https://127.0.0.1:$port"
    local env=(
      "${prefix}__security__INSTALL_LOCK=true" "${prefix}__security__PASSWORD_CHECK_PWN=false"
      "${prefix}__database__DB_TYPE=sqlite3"
      "${prefix}__server__PROTOCOL=https" "${prefix}__server__HTTP_PORT=3000"
      "${prefix}__server__CERT_FILE=/certs/forge.pem" "${prefix}__server__KEY_FILE=/certs/forge.key"
      "${prefix}__server__ROOT_URL=$base/" "${prefix}__server__DOMAIN=127.0.0.1"
      "${prefix}__server__DISABLE_SSH=true" "${prefix}__server__START_SSH_SERVER=false"
      "${prefix}__service__DISABLE_REGISTRATION=true" "${prefix}__mailer__ENABLED=false"
      "${prefix}____APP_NAME=$nonce" "${prefix}__log__LEVEL=Warn"
      "${prefix}__repository__DEFAULT_BRANCH=main"
      "${prefix}__oauth2__ACCESS_TOKEN_EXPIRATION_TIME=5"
    )
    # Gitea reuses refresh tokens by default; turn on one-time mode. Forgejo's
    # default is already one-time, and the proof checks it either way.
    [[ "$family" == gitea ]] && env+=("${prefix}__oauth2__INVALIDATE_REFRESH_TOKENS=true")
    local args=(--name "$name" --label "helm.giteaproof.run=$run_id" -p "127.0.0.1:$port:3000"
      --mount type=tmpfs,destination=/data --volume "$tmp/certs:/certs:ro")
    for value in "${env[@]}"; do args+=(--env "$value"); done
    cid=$(docker create "${args[@]}" "$image" 2>"$err") || { cat "$err" >&2; return 1; }
    printf '%s\n' "$cid" >> "$containers_file"
    if docker start "$cid" >/dev/null 2>"$err"; then
      return 0
    fi
    printf 'Start on port %s failed (attempt %s); retrying on a new port\n' "$port" "$attempt" >&2
    docker rm -f -v "$cid" >/dev/null 2>&1 || true
  done
  return 1
}

wait_ready() { # cid base
  local deadline=$(( $(date +%s) + 180 ))
  while (( $(date +%s) < deadline )); do
    [[ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" == true ]] || return 1
    curl --silent --fail --max-time 3 --cacert "$tmp/ca.pem" "$2/api/v1/version" >/dev/null 2>&1 && return 0
    sleep 1
  done
  return 1
}

overall=0
results=()
for image in "${images[@]}"; do
  family=gitea; cli=gitea; cid=
  [[ "$image" == *forgejo* ]] && { family=forgejo; cli=forgejo; }
  printf '\n== %s (%s)\n' "$family" "$image"
  forge_started=$(date +%s)
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    printf '%s\n' "$image" >> "$pulled_file"
    docker pull --quiet "$image" >/dev/null
  fi
  if ! start_forge "$family" "$image" || ! wait_ready "$cid" "$base"; then
    printf '%s did not become ready\n' "$family" >&2
    [[ -z "${cid:-}" ]] || docker logs --tail 40 "$cid" > "$artifact_dir/$family-startup.log" 2>&1 || true
    overall=1
    continue
  fi
  printf 'Ready at %s after %ss\n' "$base" "$(( $(date +%s) - forge_started ))"
  helm_dir="$tmp/helm-$family"
  install -d -m 0700 "$helm_dir"
  settings="$tmp/settings-$family.json"
  python3 -I - "$settings" <<PY
import json, sys
json.dump({"family": "$family", "image": "$image", "container": "$cid", "base": "$base", "cli": "$cli",
           "cafile": "$tmp/ca.pem", "binary": "$tmp/bin/helm", "helm_dir": "$helm_dir", "pid_file": "$pid_file",
           "result": "$artifact_dir/$family.json", "artifact_dir": "$artifact_dir", "nonce": "$nonce"},
          open(sys.argv[1], "w"))
PY
  # In the background, so an interrupt reaches the trap at once (bash defers
  # traps while a foreground command runs).
  status=0
  python3 -I -B "$here/proof.py" "$settings" > >(tee "$artifact_dir/$family.log") 2>&1 &
  proof_pid=$!
  wait "$proof_pid" || status=$?
  proof_pid=
  [[ "$status" == 0 ]] || overall=1
  [[ -f "$artifact_dir/$family.json" ]] && results+=("$artifact_dir/$family.json")
  docker rm -f -v "$cid" >/dev/null 2>&1 || true
done

python3 -I - "$artifact_dir/result.json" "$run_id" "$overall" "${results[@]}" <<'PY'
import json, sys
out, run_id, overall, files = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
forges = [json.load(open(f)) for f in files]
passed = overall == "0" and len(forges) > 0 and all(f["passed"] for f in forges)
json.dump({"run_id": run_id, "passed": passed, "forges": forges}, open(out, "w"), indent=1)
for f in forges:
    print(f"{f['forge']:8} {f['version'] or '-':24} {'PASSED' if f['passed'] else 'FAILED'} "
          f"{sum(a['ok'] for a in f['assertions'])}/{len(f['assertions'])} assertions in {f['seconds']}s")
print("RESULT:", "PASSED" if passed else "FAILED")
sys.exit(0 if passed else 1)
PY
