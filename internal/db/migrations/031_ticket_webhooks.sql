-- Admin-managed inbound webhooks that let outside apps open tickets. The
-- secret appears only in the create/rotate response; Helm stores its SHA-256
-- digest (unique, used for lookup) and a short display hint.
CREATE TABLE IF NOT EXISTS ticket_webhooks (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    format TEXT NOT NULL CHECK (format IN ('generic', 'coolify')),
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    assignee_id TEXT REFERENCES actors(id) ON DELETE SET NULL,
    secret_sha256 TEXT NOT NULL UNIQUE,
    secret_hint TEXT NOT NULL,
    created_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    rotated_at TEXT,
    disabled_at TEXT,
    last_delivery_at TEXT,
    delivery_count INTEGER NOT NULL DEFAULT 0 CHECK (delivery_count >= 0)
);
