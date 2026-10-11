-- Email addresses for ticket webhooks (Cloudflare Email Routing → Worker →
-- Helm). See docs/EMAIL_ALERT_INTAKE_PLAN.md. Only provider resource IDs and
-- SHA-256 digests are stored: never a Cloudflare token, the Worker's intake
-- secret or a webhook's address tag.
CREATE TABLE IF NOT EXISTS email_intakes (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL CHECK (provider IN ('cloudflare')),
    domain TEXT NOT NULL CHECK (length(domain) BETWEEN 3 AND 253),
    local_part TEXT NOT NULL CHECK (length(local_part) BETWEEN 1 AND 32),
    account_id TEXT NOT NULL,
    zone_id TEXT NOT NULL,
    worker_name TEXT NOT NULL,
    rule_id TEXT NOT NULL,
    secret_sha256 TEXT NOT NULL UNIQUE,
    fallback_address TEXT,
    subaddress_enabled_by_helm INTEGER NOT NULL DEFAULT 0 CHECK (subaddress_enabled_by_helm IN (0, 1)),
    public_endpoint_id TEXT REFERENCES public_endpoints(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'disabled')),
    cleanup_pending INTEGER NOT NULL DEFAULT 0 CHECK (cleanup_pending IN (0, 1)),
    created_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    disabled_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS email_intakes_one_active
    ON email_intakes(status) WHERE status = 'active';

-- A webhook's address is <local_part>+<tag>@<domain>; the tag selects it.
ALTER TABLE ticket_webhooks ADD COLUMN email_tag_sha256 TEXT;
ALTER TABLE ticket_webhooks ADD COLUMN email_tag_hint TEXT;
ALTER TABLE ticket_webhooks ADD COLUMN email_tag_created_at TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS ticket_webhooks_email_tag
    ON ticket_webhooks(email_tag_sha256) WHERE email_tag_sha256 IS NOT NULL;

-- One row per received message (or refusal). The unique receipt digest makes
-- re-delivery idempotent; accepted receipts commit with the ticket.
CREATE TABLE IF NOT EXISTS email_receipts (
    id TEXT PRIMARY KEY,
    intake_id TEXT NOT NULL REFERENCES email_intakes(id) ON DELETE CASCADE,
    receipt_sha256 TEXT NOT NULL,
    webhook_id TEXT REFERENCES ticket_webhooks(id) ON DELETE SET NULL,
    sender TEXT NOT NULL,
    subject TEXT NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('processing', 'created', 'repeated', 'retained', 'unknown_recipient', 'unreadable', 'too_large')),
    task_id TEXT,
    task_key TEXT,
    occurrence_count INTEGER NOT NULL DEFAULT 0,
    reason TEXT,
    received_at TEXT NOT NULL,
    UNIQUE (intake_id, receipt_sha256)
);

CREATE INDEX IF NOT EXISTS email_receipts_recent
    ON email_receipts(intake_id, received_at DESC);
