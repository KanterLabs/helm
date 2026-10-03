-- Durable Tickets membership. A ticket is an ordinary task plus this row;
-- membership is independent of editable labels and the task/bug kind, so
-- relabeling never drops a ticket. Status is always derived from the task's
-- column semantic state. Alert-created tasks recorded before this migration
-- are backfilled.
CREATE TABLE IF NOT EXISTS tickets (
    task_id TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    origin TEXT NOT NULL CHECK (origin IN ('alert', 'manual')),
    created_at TEXT NOT NULL
);

INSERT OR IGNORE INTO tickets(task_id, origin, created_at)
SELECT task_id, 'alert', first_received_at FROM alert_conditions WHERE task_id IS NOT NULL;
