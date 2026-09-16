-- Agent notes are bounded, curated task memory rather than chronological
-- activity. Resolution is additive so prior guidance remains auditable.
CREATE TABLE agent_notes (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL REFERENCES actors(id),
    category TEXT NOT NULL,
    body TEXT NOT NULL,
    evidence_json TEXT NOT NULL DEFAULT '[]',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE INDEX agent_notes_task_active_idx
    ON agent_notes(task_id, resolved_at, created_at, id);
