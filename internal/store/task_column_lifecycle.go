package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
)

func taskColumnOnlyInput(input TaskInput) bool {
	if input.ColumnID == nil {
		return false
	}
	input.ColumnID = nil
	return reflect.DeepEqual(input, TaskInput{})
}

// updateTaskColumnTx is the column-only human patch lifecycle. It retains normal
// task.updated/dependency events, claim ownership and destination-tail ordering.
// Composed writers must acquire the writer lock before reading current Task.
func (s *Store) updateTaskColumnTx(ctx context.Context, tx *sql.Tx, current Task, column Column, expected int64, actorID string, override bool) error {
	if column.ProjectID != current.ProjectID || column.ArchivedAt != nil {
		return invalid("destination column is unavailable", nil)
	}
	query := `UPDATE tasks SET column_id=?,position=CASE WHEN column_id<>? THEN (SELECT COALESCE(MAX(position)+1,0) FROM tasks other WHERE other.column_id=? AND other.deleted_at IS NULL AND other.id<>?) ELSE position END,completed_at=CASE WHEN ?='completed' THEN CASE WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id)='completed' THEN completed_at ELSE ? END ELSE NULL END,version=version+1,updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL`
	timestamp := now()
	args := []any{column.ID, column.ID, column.ID, current.ID, column.SemanticState, timestamp, timestamp, current.ID, expected}
	if !override {
		query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at)<=julianday(?) OR claimed_by=?)`
		args = append(args, timestamp, actorID)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return mapDependencyLifecycleError(ctx, tx, mapReleaseMutationError(ctx, tx, err, current.ProjectID, current.ID), dependencyLifecycleTarget{TaskID: current.ID})
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return bugMutationFailure(ctx, tx, current.ID, actorID, expected, timestamp, current, override)
	}
	sourceState, err := authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
	if err != nil {
		return err
	}
	state, err := authoritativeColumnStateTx(ctx, tx, column.ID, current.ProjectID)
	if err != nil {
		return err
	}
	if state != column.SemanticState {
		return conflict("task destination changed semantic state", nil)
	}
	if err = validateTaskBugLifecycleTx(ctx, tx, current.ID, current.Kind, state); err != nil {
		return err
	}
	status := checklistCompletionStatus{}
	if state == "completed" && sourceState != "completed" {
		status, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
		if err != nil {
			return err
		}
		if err = rejectIncompleteChecklist(status); err != nil {
			return err
		}
	}
	payload := map[string]any{"version": expected + 1}
	if state == "completed" && sourceState != "completed" {
		addChecklistCompletionEventFields(payload, status)
	}
	if _, err = insertEvent(ctx, tx, "task.updated", actorID, current.ProjectID, current.ID, payload); err != nil {
		return err
	}
	change, changed, err := dependencyTaskStateChange(ctx, tx, dependencyTaskFromTask(current, sourceState))
	if err != nil {
		return err
	}
	if changed {
		return emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change})
	}
	return nil
}

func validateTaskBugLifecycleTx(ctx context.Context, tx dependencySQL, taskID, kind, destinationState string) error {
	if kind != bugKind {
		return nil
	}
	var resolution sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT resolution FROM bug_details WHERE task_id=?`, taskID).Scan(&resolution)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	resolved := err == nil && resolution.Valid && strings.TrimSpace(resolution.String) != ""
	if destinationState == "completed" && !resolved {
		return invalid("bugs must be resolved before entering a completed column", nil)
	}
	if destinationState != "completed" && resolved {
		return invalid("resolved bugs must be reopened before leaving a completed column", nil)
	}
	return nil
}
