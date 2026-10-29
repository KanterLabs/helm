package store

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Store) ResolveBug(ctx context.Context, id string, input ResolveBugInput, expected int64, actorID string) (Task, error) {
	return s.ResolveBugWithClaimOverride(ctx, id, input, expected, actorID, false)
}

func (s *Store) ResolveBugWithClaimOverride(ctx context.Context, id string, input ResolveBugInput, expected int64, actorID string, allowClaimOverride bool) (Task, error) {
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	resolution := strings.ToLower(strings.TrimSpace(input.Resolution))
	if !validBugResolution(resolution) {
		return Task{}, invalid("resolution is invalid", nil)
	}
	if strings.TrimSpace(actorID) == "" {
		return Task{}, invalid("a resolver actor is required", nil)
	}
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return Task{}, invalid("resolver actor not found", nil)
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if current.Kind != bugKind || current.Bug == nil {
		return Task{}, invalid("task is not a bug", nil)
	}
	var duplicateOf string
	if input.DuplicateOf != nil {
		duplicateOf = strings.TrimSpace(*input.DuplicateOf)
	}
	if resolution == "duplicate" {
		if duplicateOf == "" {
			return Task{}, invalid("duplicate_of is required for duplicate resolution", nil)
		}
	} else if duplicateOf != "" {
		return Task{}, invalid("duplicate_of is only valid for duplicate resolution", nil)
	}
	note := strings.TrimSpace(input.Note)
	if len(note) > 10000 {
		return Task{}, invalid("resolution note is too long", nil)
	}
	completedColumn, err := s.StateColumn(ctx, current.ProjectID, "completed")
	if err != nil {
		return Task{}, err
	}
	timestamp := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		return s.resolveBugTx(ctx, tx, current, completedColumn, actorID, expected, resolution, duplicateOf, note, timestamp, allowClaimOverride)
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// resolveBugTx shares the normal lifecycle writer transaction with composed operations.
func (s *Store) resolveBugTx(ctx context.Context, tx *sql.Tx, current Task, completedColumn Column, actorID string, expected int64, resolution, duplicateOf, note, timestamp string, allowClaimOverride bool) error {
	id := current.ID

	checklistStatus := checklistCompletionStatus{}
	duplicateTargetID := ""
	query := `UPDATE tasks SET column_id=?, completed_at=CASE WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id)='completed' AND tasks.completed_at IS NOT NULL THEN tasks.completed_at ELSE ? END, claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=? WHERE id=? AND kind='bug' AND version=? AND deleted_at IS NULL`
	args := []any{completedColumn.ID, timestamp, timestamp, id, expected}
	if !allowClaimOverride {
		query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at)<=julianday(?) OR claimed_by=?)`
		args = append(args, timestamp, actorID)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: id})
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return bugMutationFailure(ctx, tx, id, actorID, expected, timestamp, current, allowClaimOverride)
	}
	// Resolve duplicate targets only after the guarded task UPDATE has
	// acquired SQLite's writer lock. A pre-write lookup in a deferred
	// transaction can retain a stale WAL snapshot and fail with
	// SQLITE_BUSY_SNAPSHOT when the writer arrives; any invalid target still
	// rolls back the task update through withTx.
	if resolution == "duplicate" {
		target, targetErr := s.resolveDuplicateTargetTx(ctx, tx, current.ProjectID, id, duplicateOf)
		if targetErr != nil {
			return targetErr
		}
		duplicateTargetID = target
	}
	destinationState, err := authoritativeColumnStateTx(ctx, tx, completedColumn.ID, current.ProjectID)
	if err != nil {
		return err
	}
	if destinationState != "completed" {
		return conflict("bug destination changed semantic state", map[string]any{"column_id": completedColumn.ID, "expected_state": "completed", "current_state": destinationState})
	}
	sourceState := destinationState
	if current.ColumnID != completedColumn.ID {
		sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
		if err != nil {
			return err
		}
	}
	beforeDependency := dependencyTaskFromTask(current, sourceState)
	// ResolveBug is another completion path. Evaluate the project policy
	// after the guarded write so require-policy rejection rolls back the bug
	// row, resolution metadata, links, comments, and activity atomically.
	checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
	if err != nil {
		return err
	}
	if err := rejectIncompleteChecklist(checklistStatus); err != nil {
		return err
	}
	var duplicateValue any
	if duplicateTargetID != "" {
		duplicateValue = duplicateTargetID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE bug_details SET resolution=?, resolved_by=?, resolved_at=?, duplicate_of=? WHERE task_id=?`, resolution, actorID, timestamp, duplicateValue, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_links WHERE source_task_id=? AND link_type='duplicate'`, id); err != nil {
		return err
	}
	if duplicateTargetID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_links(source_task_id,target_task_id,link_type,created_at) VALUES (?,?,?,?)`, id, duplicateTargetID, "duplicate", timestamp); err != nil {
			return err
		}
	}
	if note != "" {
		commentID := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, commentID, id, actorID, note, timestamp, timestamp); err != nil {
			return err
		}
		if _, err := insertEvent(ctx, tx, "comment.created", actorID, current.ProjectID, id, map[string]any{"comment_id": commentID}); err != nil {
			return err
		}
	}
	eventType := "bug.resolved"
	if resolution == "duplicate" {
		eventType = "bug.duplicated"
	}
	eventPayload := map[string]any{"version": expected + 1, "resolution": resolution}
	addChecklistCompletionEventFields(eventPayload, checklistStatus)
	if _, err = insertEvent(ctx, tx, eventType, actorID, current.ProjectID, id, eventPayload); err != nil {
		return err
	}
	change, changed, err := dependencyTaskStateChange(ctx, tx, beforeDependency)
	if err != nil {
		return err
	}
	if changed {
		return emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change})
	}
	return nil
}
