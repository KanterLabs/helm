# Helm observability and operator response

Helm exposes bounded, privacy-safe diagnostics for the production operator.
The application does not send prompts, task descriptions, credentials, email
addresses, actor names, project keys, or opaque IDs to a metrics backend.

## Endpoints

`GET /healthz` is a public liveness probe. It only confirms that the HTTP
process is serving and returns `{"status":"ok","service":"helm"}`.

`GET /readyz` (and the `/ready` compatibility alias) is a public,
machine-readable readiness probe. A ready response is HTTP 200 with
`status: "ok"` and `ready: true`; a failed check is HTTP 503 with
`status: "degraded"` and `ready: false`. The response contains only bounded
check names, statuses, safe fixed messages, timings, schema version numbers,
page counts, WAL size, and filesystem capacity. It never includes the
configured filesystem path or a database error string.

Unknown migration versions are reported as a bounded compatibility warning
(`migration_state: "unknown"`) and do not by themselves fail readiness: the
database migration contract allows a retained binary to serve a database
upgraded by a newer additive release. Embedded migrations that are pending,
or schema inspection errors, still fail readiness.

The readiness checks are:

- `database`: ping latency and a short `BEGIN IMMEDIATE` writer-lock probe;
- `schema` and `schema_compatibility`: the embedded schema version, pending
  migrations, and unknown newer migrations;
- `migration`: whether the embedded migration set is current;
- `writable_capacity`: a read-only preflight of directory mode bits, mount
  state, filesystem availability, and the free-space reserve for the database
  directory; and
- `storage`: SQLite page usage, database bytes, and WAL bytes.

The writable flag checks directory mode bits and the filesystem read-only
mount flag; it cannot prove effective write access through ACLs, id-mapped
mounts, service privileges, or a later filesystem state change. The database
open/write path remains authoritative.

`GET /metrics` returns Prometheus text format. Unauthenticated collection is
allowed only when the TCP peer is loopback (`127.0.0.0/8` or `::1`) and the
request has no forwarding or proxy identity headers. A non-loopback scrape,
including a loopback request forwarded by a proxy, must use the normal Helm
session or a scoped bearer token. The endpoint never trusts
`X-Forwarded-For` or other proxy headers for identity; keep it behind the
existing loopback service boundary or an authenticated private monitoring
path.

Remote authenticated scrapes use the normal Helm authentication path. As with
other authenticated requests, an eligible session or bearer token may update
the existing throttled `last_seen_at` or `last_used_at` bookkeeping. A
direct loopback scrape without proxy headers avoids that authentication
bookkeeping.

The primary metric families are:

| Metric | Meaning |
| --- | --- |
| `helm_http_requests_total` | Request count by method, bounded route template, and status. |
| `helm_http_errors_total` | Requests returning 4xx or 5xx. |
| `helm_http_request_duration_seconds` | Request latency histogram, sum, and count. |
| `helm_auth_failures_total` | Authentication failures by a fixed reason class. |
| `helm_rate_limit_failures_total` | Credential, request, mutation, or auth-slot rejections. |
| `helm_database_lock_latency_seconds` | SQLite writer-lock acquisition latency and failures. |
| `helm_database_size_bytes`, `helm_database_wal_bytes` | Logical database and WAL sidecar size. |
| `helm_database_page_usage_ratio` | SQLite pages used divided by the configured page ceiling. |
| `helm_agent_mutations_total` | Agent mutation attempts, successes, failures, and limit rejections. |
| `helm_agent_mutation_pressure_ratio` | Limit rejections divided by observed agent mutation attempts. |

All labels are bounded enums or finite route templates. Task and project
references are replaced with `:task` and `:project`; unknown API paths collapse
to `/api/v1/other`, and non-standard HTTP methods collapse to `OTHER`. Query
strings and request bodies are never labels.

## Alert thresholds and response

These are starting thresholds for a single production instance. Tune them to
the scrape interval and workload after collecting a week of normal traffic.

| Signal | Starting alert | Operator response |
| --- | --- | --- |
| HTTP 5xx | `sum(rate(helm_http_errors_total{status=~"5.."}[5m])) / sum(rate(helm_http_requests_total[5m])) > 0.05` for 10m | Check the bounded request error rate, readiness output, and the service log by `request_id`; retain the current release and roll back only after confirming a release regression. |
| HTTP latency | p95 from `helm_http_request_duration_seconds` > 1s for 10m | Check database lock latency and pool waits, then reduce polling or move heavy agent work; do not increase SQLite writer concurrency blindly. |
| Authentication/rate limits | Any sustained increase for 10m, or `helm_rate_limit_failures_total{scope="mutation"}` > 0 | Confirm the caller and token scope, apply client backoff, and rotate/revoke only the affected credential. Never put a token in a log query. |
| Database contention | `rate(helm_database_lock_latency_seconds_sum[5m]) / rate(helm_database_lock_latency_seconds_count[5m]) > 0.25` or any lock errors | Inspect the writer workload and free capacity. Allow blocked writes to drain, verify WAL growth, and use the backup/runbook before any repair. |
| Capacity | `helm_database_page_usage_ratio > 0.8`, WAL > 48 MiB, or free bytes < 64 MiB | Stop bulk/agent mutations, take a verified backup, compact or expand the explicitly configured data volume, then re-check `/readyz`. The readiness gate uses SQLite's live page ceiling and journal-size limit; it does not impose a second arbitrary database-byte cutoff. Never delete the database as a first response. |
| Readiness | `/readyz` is 503 for 2 consecutive scrapes | Read each failed check. For schema/migration, deploy the matching binary through the migration preflight path; for storage, repair capacity/permissions; for lock failures, drain the writer and preserve the database. |

## Structured application logs

Each request produces one JSON event with a request ID, bounded route, method,
status, duration, authentication kind, and actor kind. Authenticated actors
are represented by a short one-way actor hash; names, email addresses, task
content, query strings, and bearer values are excluded. Internal failures log
only a fixed error class, while the client receives a stable error code and a
generic message. Use the response `X-Request-ID` to correlate a safe request
event with an operator report.

## Deployment

The production systemd and Compose defaults bind Helm to loopback and keep the
database volume as the only writable application storage. Scrape `/metrics`
from the host or a private monitoring process over that loopback boundary.
Do not publish `/metrics` through the public Cloudflare application. If a
remote monitor is required, use a separately authenticated private path with
a read-only monitoring token and preserve the existing Cloudflare audience
and origin rules.

Deployments should gate activation on `/readyz`, retain the previous release,
and use `helm-backup` plus `schema-preflight` before migrations. A failed
readiness check is a signal to pause promotion, not a reason to recreate or
restore over the live database.
