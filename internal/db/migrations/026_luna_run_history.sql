-- Luna run history is a private debugging ledger for the human who invoked
-- each model turn. It deliberately stores execution metadata only: prompts,
-- project/task text, model output, account metadata, and credentials are not
-- retained. The additive table remains invisible to retained binaries.
CREATE TABLE IF NOT EXISTS luna_runs (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    project_key TEXT NOT NULL DEFAULT '' CHECK (length(project_key) <= 80),
    feature TEXT NOT NULL CHECK (feature IN ('task_draft', 'project_intelligence')),
    outcome TEXT NOT NULL DEFAULT 'running' CHECK (outcome IN ('running', 'succeeded', 'invalid_output', 'incomplete', 'limit_reached', 'timed_out', 'canceled', 'unavailable')),
    model TEXT NOT NULL CHECK (length(trim(model)) > 0 AND length(model) <= 120),
    effort TEXT NOT NULL CHECK (effort IN ('low', 'medium', 'high', 'xhigh', 'max', 'ultra')),
    thread_id TEXT NOT NULL DEFAULT '' CHECK (length(thread_id) <= 200),
    turn_id TEXT NOT NULL DEFAULT '' CHECK (length(turn_id) <= 200),
    duration_ms INTEGER CHECK (duration_ms IS NULL OR (duration_ms >= 0 AND duration_ms <= 300000)),
    output_bytes INTEGER CHECK (output_bytes IS NULL OR (output_bytes >= 0 AND output_bytes <= 2097152)),
    detail TEXT NOT NULL DEFAULT '' CHECK (length(detail) <= 500),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    CHECK (
        (outcome = 'running' AND completed_at IS NULL AND duration_ms IS NULL)
        OR (outcome <> 'running' AND completed_at IS NOT NULL AND duration_ms IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS luna_runs_actor_started_idx
    ON luna_runs(actor_id, started_at DESC, id DESC);
