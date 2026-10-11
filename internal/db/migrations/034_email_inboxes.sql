-- Email inboxes: an address created in Helm whose mail becomes tickets in a
-- project. See docs/EMAIL_ALERT_INTAKE_PLAN.md § Inboxes. The address is
-- <intake local part>+<tag>@<intake domain>; the tag is stored as is so the
-- address can always be shown and copied (an email address is not a
-- secret: it appears in every message sent to it). Replacing the address
-- issues a new tag and the old one stops working.
--
-- This replaces the per-webhook addresses of migration 033; its
-- ticket_webhooks.email_tag_* columns are no longer read.
CREATE TABLE IF NOT EXISTS email_inboxes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    assignee_id TEXT REFERENCES actors(id) ON DELETE SET NULL,
    tag TEXT NOT NULL UNIQUE CHECK (length(tag) BETWEEN 4 AND 40),
    created_by TEXT REFERENCES actors(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    replaced_at TEXT,
    disabled_at TEXT,
    last_received_at TEXT,
    received_count INTEGER NOT NULL DEFAULT 0 CHECK (received_count >= 0)
);

ALTER TABLE email_receipts ADD COLUMN inbox_id TEXT REFERENCES email_inboxes(id) ON DELETE SET NULL;
