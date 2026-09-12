-- Notifications, watches, and preferences are additive coordination
-- records. Existing task/project/activity rows remain untouched so a
-- retained binary can continue to read and write the original model.

CREATE TABLE IF NOT EXISTS watches (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS watches_project_unique
    ON watches(actor_id, project_id) WHERE task_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS watches_task_unique
    ON watches(actor_id, task_id) WHERE task_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS watches_project_idx
    ON watches(project_id, task_id, actor_id);
CREATE INDEX IF NOT EXISTS watches_actor_idx
    ON watches(actor_id, created_at DESC, id DESC);
CREATE TRIGGER IF NOT EXISTS watches_same_project_insert
BEFORE INSERT ON watches
WHEN NEW.task_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'watch_task_project_mismatch')
    WHERE NOT EXISTS (
        SELECT 1 FROM tasks watched_task
        WHERE watched_task.id = NEW.task_id
          AND watched_task.project_id = NEW.project_id
          AND watched_task.deleted_at IS NULL
    );
END;

CREATE TABLE IF NOT EXISTS notification_preferences (
    actor_id TEXT PRIMARY KEY REFERENCES actors(id) ON DELETE CASCADE,
    assignments INTEGER NOT NULL DEFAULT 1 CHECK (assignments IN (0, 1)),
    mentions INTEGER NOT NULL DEFAULT 1 CHECK (mentions IN (0, 1)),
    blockers INTEGER NOT NULL DEFAULT 1 CHECK (blockers IN (0, 1)),
    state_changes INTEGER NOT NULL DEFAULT 1 CHECK (state_changes IN (0, 1)),
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
    id TEXT PRIMARY KEY,
    recipient_id TEXT NOT NULL REFERENCES actors(id) ON DELETE CASCADE,
    actor_id TEXT REFERENCES actors(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    dedupe_key TEXT NOT NULL,
    read_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(recipient_id, dedupe_key),
    CHECK (json_valid(payload))
);
CREATE INDEX IF NOT EXISTS notifications_recipient_unread_idx
    ON notifications(recipient_id, read_at, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS notifications_task_idx
    ON notifications(task_id, created_at DESC, id DESC);
