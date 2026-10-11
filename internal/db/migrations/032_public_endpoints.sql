-- Public webhook endpoints (Cloudflare Tunnel). Only provider resource IDs
-- are stored here. The Cloudflare API token is never stored, and the tunnel
-- run token lives in an owner-only file beside the database, not in it, so
-- database backups cannot be used to impersonate the connector.
CREATE TABLE IF NOT EXISTS public_endpoints (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('cloudflare')),
    hostname TEXT NOT NULL CHECK (length(hostname) BETWEEN 3 AND 253),
    account_id TEXT NOT NULL,
    zone_id TEXT NOT NULL,
    tunnel_id TEXT NOT NULL,
    dns_record_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    cleanup_pending INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_pending IN (0, 1)),
    created_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    disabled_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS public_endpoints_one_active
    ON public_endpoints(status) WHERE status = 'active';
