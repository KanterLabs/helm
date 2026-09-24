-- Safe, prompt-free execution milestones for the Luna history view. The
-- normalized rows let an active run append bounded milestones while existing
-- readers continue to see the parent luna_runs row.
CREATE TABLE IF NOT EXISTS luna_run_steps (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES luna_runs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence >= 1 AND sequence <= 32),
    kind TEXT NOT NULL CHECK (kind IN ('thread_started', 'turn_started', 'response_generated', 'validation', 'outcome')),
    at TEXT NOT NULL CHECK (length(at) <= 64),
    UNIQUE(run_id, sequence)
);

CREATE INDEX IF NOT EXISTS luna_run_steps_run_sequence_idx
    ON luna_run_steps(run_id, sequence);
