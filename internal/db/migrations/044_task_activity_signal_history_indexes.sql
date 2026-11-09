-- Additive lookup indexes for the task timeline source queries
-- (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D3, D4; TC-379). The timeline excludes a
-- progress event or generated comment by finding the history row that owns
-- it; without these indexes each lookup scans task_agent_work_history once
-- per event or comment. The neglect-signal projector replays a task's whole
-- timeline on every rebuild, which made that scan the dominant cost on the
-- populated benchmark database. No data changes; a retained binary that does
-- not know these indexes keeps working.
CREATE INDEX IF NOT EXISTS task_agent_work_history_progress_event_idx
    ON task_agent_work_history(progress_event_cursor);
CREATE INDEX IF NOT EXISTS task_agent_work_history_generated_comment_idx
    ON task_agent_work_history(generated_comment_id);
