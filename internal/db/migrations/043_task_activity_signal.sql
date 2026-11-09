-- Additive derived read model for the neglect signal
-- (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D1, D2). Both tables start empty; the
-- projector rebuilds rows from history, so no existing data is rewritten and
-- a retained binary that ignores these tables keeps working. Times are
-- fixed-width UTC text so they compare as instants.
CREATE TABLE IF NOT EXISTS task_activity_signal (
 task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
 status TEXT NOT NULL CHECK (status IN ('known', 'bounded', 'unknown')),
 last_meaningful_at TEXT,
 source TEXT NOT NULL DEFAULT '',
 not_after TEXT,
 reason TEXT NOT NULL DEFAULT '',
 classification_version INTEGER NOT NULL,
 computed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_activity_signal_projection (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 event_cursor INTEGER NOT NULL DEFAULT 0,
 seeded_version INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
