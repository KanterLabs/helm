-- One ticket queue (docs/TICKET_QUEUE_PLAN.md). New tickets from webhooks,
-- email inboxes, Coolify and the New ticket button land in a built-in
-- project marked system_kind = 'tickets' instead of a project chosen up
-- front. The store creates that project on first use, so installs that
-- never use Tickets see no change. The project keeps every task invariant
-- (numbering, columns, access) and is hidden from project navigation.
ALTER TABLE projects ADD COLUMN system_kind TEXT CHECK (system_kind IS NULL OR system_kind = 'tickets');
CREATE UNIQUE INDEX IF NOT EXISTS projects_system_kind_unique ON projects(system_kind) WHERE system_kind IS NOT NULL;

-- Filing a ticket into a project gives it that project's next number. The
-- old key (for example TKT-12) keeps resolving to the task through this
-- alias; numbers are never reused because project counters only grow.
CREATE TABLE IF NOT EXISTS task_number_aliases (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number INTEGER NOT NULL CHECK (number > 0),
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, number)
);
CREATE INDEX IF NOT EXISTS task_number_aliases_task_idx ON task_number_aliases(task_id);
