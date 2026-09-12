-- A column state change is also a lifecycle change for every live task in the
-- column: UpdateColumn changes the column first and then derives task
-- completion fields from the new state.  A released task must remain frozen,
-- including when the writer is a retained pre-023 binary that knows nothing
-- about this trigger.
--
-- Keep this migration additive and retry-safe.  The trigger deliberately
-- rejects only an actual semantic-state transition and leaves ordinary column
-- metadata edits available for released releases.
CREATE TRIGGER IF NOT EXISTS releases_guard_released_column_lifecycle
BEFORE UPDATE OF semantic_state ON columns
WHEN OLD.semantic_state IS NOT NEW.semantic_state
 AND EXISTS (
     SELECT 1
     FROM tasks task
     JOIN releases release ON release.id = task.release_id
     WHERE task.project_id = OLD.project_id
       AND task.column_id = OLD.id
       AND task.deleted_at IS NULL
       AND release.project_id = OLD.project_id
       AND release.released_at IS NOT NULL
 )
BEGIN
    SELECT RAISE(ABORT, 'release_frozen');
END;
