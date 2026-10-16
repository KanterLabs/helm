# Private Coolify deployment

`beta` deploys to `KanterLabs Private / staging / helm-beta` and
`main` to `KanterLabs Private / production / helm-production`, both on the
homelab application host VM124 (`10.0.10.21`, private origins `10.0.30.21`).
The existing private edge retains TLS, verified owner assertions, split DNS,
and the `.home.shanekanterman.dev` hostnames. No public dashboard route is added.

| Lane | Service UUID | Origin | Persistent host directory |
| --- | --- | --- | --- |
| beta | `4pn0vrmau0g9nqaecsil0rxq` | `10.0.30.21:18446` | `/srv/helm/beta` |
| production | `w7bbeca8q8cs4btkzu9p9v9z` | `10.0.30.21:18447` | `/srv/helm/production` |

`compose.py beta` or `compose.py production` renders the secret-free Coolify
service definition. Each lane has a separate `data` directory and root-protected
runtime files in `config`; the service runs as UID/GID 65532. The data bind keeps
its historical in-container path `/var/lib/roadmap/data`, including account
material and webhook state. Configure `HELM_TAILNET_TLS_ADDR` to the exact
private container address (`172.28.25.2:8443` or `172.28.26.2:8443`) and allow
only peer `10.0.30.2`. The beta LXC release switcher is retired.

Before creating either service, provision its persistent network (these are
retained across deployments):

```sh
docker network create --subnet 172.28.25.0/24 --ip-range 172.28.25.0/30 \
  --aux-address reserved=172.28.25.3 helm-beta
docker network create --subnet 172.28.26.0/24 --ip-range 172.28.26.0/30 \
  --aux-address reserved=172.28.26.3 helm-production
```

The subnet, gateway and reserved address leave one assignable address per
network. `network_mode` keeps Coolify from attaching a second network and
publishing TLS to the wrong interface. The host's existing Docker firewall
guard permits only edge `10.0.30.2` to the exact published ports and container
port 8443. The edge permits only these two origin destinations.

## Branch automation

The existing CI validates, smoke-tests, scans and publishes an immutable image
in `ghcr.io/kanterlabs/helm`. Each branch's protected environment supplies its
exact Coolify service identifiers and a separate API token. The deployer
checks the branch tip, service, project, environment, host, prior digest and
revision, updates only `HELM_IMAGE` and `HELM_RELEASE_SHA`, redeploys, then
requires healthy status and the exact revision from the private HTTPS
`/healthz` and `/readyz`. Deployment artifacts contain only sanitized evidence.
A failed image deployment restores the preceding image and revision and
verifies them; it never restores an older database.

CI retains the repository's hosted-runner cutover: live platform admission
requires the retired `homelab-heavy` pool to stay zero, and the current
single-slot automatic pool is paused. See `../COOLIFY_FAILURE_MODES.md`.
Hosted deployment jobs join the tailnet as `tag:gha-helm-deploy`, which has
TCP 443 access to `svc:coolify`, `svc:helm-beta`, and `svc:helm`; the retired
Proxmox SSH grant is removed.

Gitea remains the canonical repository and GitHub the public mirror. Publish
release branch commits to both remotes. An automatic Gitea push mirror could
not be installed because GitHub organization policy disables deploy keys;
no broad GitHub credential was installed on Gitea. GitHub branch pushes
trigger deployment. Keep this limitation explicit until a scoped mirror
identity is provisioned.

Tokens are held in Infisical `lab:/apps/helm/beta` and
`lab:/apps/helm/production` as `COOLIFY_API_TOKEN`, and in their matching
GitHub environment secret. Initial CI tokens expire after 90 days; rotate
before 2027-01-14. Tokens require `read`, `read:sensitive`, `write`, `deploy`.
The image package must be public for anonymous pulls, linked to the Helm
repository, and grant that repository Actions write access.

## Backup and rollback

`helm-coolify-backup.timer` runs `backup.py` daily. It uses SQLite's online
backup API, verifies integrity and foreign keys, and records SHA-256 hashes.
Backups and runtime/account archives are root-only under each lane's
`backups/<UTC timestamp>`. Run `systemctl start helm-coolify-backup.service`
after a release and inspect the `verified.json` artifacts. These backups have
no automatic deletion policy; monitor disk usage and archive them separately.
Retain copies off the application host for host-loss recovery.

CT106 and CT103 keep their final stopped source datasets, release binaries,
and verified pre-migration backups. They are rollback material, not current
replicas. **Do not restart their Helm writers against those old datasets.**
Normal rollback changes the Coolify image only and keeps current data.
Retained binaries passed readiness against populated migrated database copies.

If moving back to an LXC is necessary, stop the Coolify writer, take and verify
a fresh backup of its current database, transfer the entire current data and
account state into a separately staged LXC directory, verify row counts and
stable IDs, then activate the retained binary and edge route. Keep the Coolify
data intact. A destructive restore requires separate explicit authorization
and an exact backup target.

CT103 retains only `cloudflared` and `helm-legacy-relay.service` for the old
bearer API URL. The relay accepts only bearer `/api/v1/` requests on loopback,
removes cookies, verifies edge TLS and forwards through a source-restricted
private edge listener to the authoritative Coolify production service. It
has no database mount or writer. The private `.home` dashboard remains the
canonical user route. Origin certificates expire 2027-09-09; renew them in
the Coolify `config` directories before expiry while retaining their CA/SNI.
