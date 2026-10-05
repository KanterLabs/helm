-- External alert intake (Coolify webhooks). One row is one alert condition:
-- an integration, a stable resource family, and the fixture-proven facts that
-- distinguish an episode (for Traefik, the offered version). The unique
-- condition key is the authoritative deduplication point, so concurrent
-- deliveries of one notice serialize on SQLite's writer lock and create at
-- most one task. Evidence holds parsed, bounded fields only, never raw
-- webhook bodies.
CREATE TABLE IF NOT EXISTS alert_conditions (
    id TEXT PRIMARY KEY,
    integration TEXT NOT NULL CHECK (length(integration) BETWEEN 1 AND 64),
    alert_type TEXT NOT NULL CHECK (length(alert_type) BETWEEN 1 AND 128),
    family_key TEXT NOT NULL CHECK (length(family_key) BETWEEN 1 AND 512),
    condition_key TEXT NOT NULL CHECK (length(condition_key) BETWEEN 1 AND 512),
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    previous_condition_id TEXT REFERENCES alert_conditions(id) ON DELETE SET NULL,
    resource_name TEXT NOT NULL CHECK (length(resource_name) <= 200),
    resource_id TEXT NOT NULL CHECK (length(resource_id) BETWEEN 1 AND 128),
    evidence TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence) AND length(evidence) <= 8192),
    occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK (typeof(occurrence_count) = 'integer' AND occurrence_count >= 1),
    first_received_at TEXT NOT NULL,
    last_received_at TEXT NOT NULL,
    UNIQUE (integration, condition_key)
);

CREATE INDEX IF NOT EXISTS alert_conditions_family
    ON alert_conditions(integration, family_key, first_received_at);

CREATE UNIQUE INDEX IF NOT EXISTS alert_conditions_task
    ON alert_conditions(task_id) WHERE task_id IS NOT NULL;
