-- Keep the latest intent with its card after a claim ends for future handoffs.
CREATE TABLE task_agent_admissions (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    actor_id TEXT NOT NULL,
    token_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    fence TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('started', 'resumed')),
    next_action TEXT NOT NULL,
    finish_condition TEXT NOT NULL,
    ended_at TEXT
);

CREATE INDEX task_agent_admission_session_idx
    ON task_agent_admissions(token_id, session_id)
    WHERE ended_at IS NULL;

CREATE TRIGGER task_agent_admission_end_on_claim_change
AFTER UPDATE OF claimed_by, claim_expires_at, completed_at, deleted_at ON tasks
WHEN NEW.claimed_by IS NOT OLD.claimed_by
  OR NEW.claim_expires_at IS NULL
  OR NEW.completed_at IS NOT NULL
  OR NEW.deleted_at IS NOT NULL
  OR (julianday(OLD.claim_expires_at) IS NULL OR julianday(OLD.claim_expires_at) <= julianday('now'))
BEGIN
    UPDATE task_agent_admissions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
    WHERE task_id = NEW.id AND ended_at IS NULL;
END;

CREATE TRIGGER task_agent_admission_end_on_task_move
AFTER UPDATE OF column_id ON tasks
WHEN NOT EXISTS (SELECT 1 FROM columns WHERE id=NEW.column_id AND semantic_state='active' AND archived_at IS NULL)
BEGIN
    UPDATE task_agent_admissions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
    WHERE task_id = NEW.id AND ended_at IS NULL;
END;

CREATE TRIGGER task_agent_admission_end_on_column_change
AFTER UPDATE OF semantic_state ON columns
WHEN NEW.semantic_state <> 'active'
BEGIN
    UPDATE task_agent_admissions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
    WHERE ended_at IS NULL AND task_id IN (SELECT id FROM tasks WHERE column_id=NEW.id);
END;

CREATE TRIGGER task_agent_admission_end_on_project_archive
AFTER UPDATE OF archived_at ON projects
WHEN NEW.archived_at IS NOT NULL
BEGIN
    UPDATE task_agent_admissions SET ended_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
    WHERE ended_at IS NULL AND task_id IN (SELECT id FROM tasks WHERE project_id=NEW.id);
END;
