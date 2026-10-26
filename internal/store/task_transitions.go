package store

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Store) CompleteTask(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.CompleteTaskWithComment(ctx, id, actorID, expected, "")
}

func (s *Store) CompleteTaskWithComment(ctx context.Context, id, actorID string, expected int64, comment string) (Task, error) {
	return s.transitionTask(ctx, id, actorID, expected, "completed", "task.completed", comment, false)
}

func (s *Store) CompleteTaskWithClaim(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.CompleteTaskWithCommentWithClaim(ctx, id, actorID, expected, "")
}

func (s *Store) CompleteTaskWithCommentWithClaim(ctx context.Context, id, actorID string, expected int64, comment string) (Task, error) {
	return s.transitionTask(ctx, id, actorID, expected, "completed", "task.completed", comment, true)
}

func (s *Store) BlockTask(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.BlockTaskWithReason(ctx, id, actorID, expected, "")
}

func (s *Store) BlockTaskWithReason(ctx context.Context, id, actorID string, expected int64, reason string) (Task, error) {
	return s.transitionTask(ctx, id, actorID, expected, "blocked", "task.blocked", reason, false)
}

func (s *Store) BlockTaskWithClaim(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.BlockTaskWithReasonWithClaim(ctx, id, actorID, expected, "")
}

func (s *Store) BlockTaskWithReasonWithClaim(ctx context.Context, id, actorID string, expected int64, reason string) (Task, error) {
	return s.transitionTask(ctx, id, actorID, expected, "blocked", "task.blocked", reason, true)
}

func (s *Store) transitionTask(ctx context.Context, id, actorID string, expected int64, state, eventType, note string, requireActiveClaim bool) (Task, error) {
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if current.Kind == bugKind && state == "completed" {
		return Task{}, invalid("bugs must be completed with a resolution", nil)
	}
	if current.Kind == bugKind && current.Bug != nil && current.Bug.Resolution != nil && state != "completed" {
		return Task{}, invalid("resolved bugs must be reopened before leaving a completed column", nil)
	}
	column, err := s.StateColumn(ctx, current.ProjectID, state)
	if err != nil {
		return Task{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		checklistStatus := checklistCompletionStatus{}
		// Compare the task's pre-update column state inside the guarded UPDATE.
		// Repeating complete while already completed must preserve its original
		// timestamp; only a transition into completed gets a new one.
		query := `UPDATE tasks SET column_id=?, completed_at=CASE WHEN ? <> 'completed' THEN NULL WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id) = 'completed' THEN tasks.completed_at ELSE strftime('%Y-%m-%dT%H:%M:%fZ','now') END, claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND deleted_at IS NULL`
		args := []any{column.ID, state, id, expected}
		if requireActiveClaim {
			query += ` AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday('now')`
			args = append(args, actorID)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			mapped := mapReleaseMutationError(ctx, tx, err, current.ProjectID, id)
			return mapDependencyLifecycleError(ctx, tx, mapped, dependencyLifecycleTarget{TaskID: id})
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if requireActiveClaim {
				version, active, claimErr := taskClaimStateTx(ctx, tx, id, actorID)
				if claimErr != nil {
					return claimErr
				}
				if version == expected && !active {
					return forbidden("an active claim owned by this actor is required")
				}
			}
			return conflict("task has changed", map[string]any{"current": current})
		}
		destinationState, err := authoritativeColumnStateTx(ctx, tx, column.ID, current.ProjectID)
		if err != nil {
			return err
		}
		if destinationState != state {
			return conflict("task destination changed semantic state", map[string]any{"column_id": column.ID, "expected_state": state, "current_state": destinationState})
		}
		sourceState := destinationState
		if current.ColumnID != column.ID {
			sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
			if err != nil {
				return err
			}
		}
		beforeDependency := dependencyTaskFromTask(current, sourceState)
		if state == "completed" {
			// Keep policy evaluation after the guarded write so a require-policy
			// rejection rolls back the transition, version, and activity atomically.
			checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
			if err != nil {
				return err
			}
			if err := rejectIncompleteChecklist(checklistStatus); err != nil {
				return err
			}
		}
		if strings.TrimSpace(note) != "" {
			if len(note) > 10000 {
				return invalid("action note is too long", nil)
			}
			commentID := newID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, commentID, id, actorID, strings.TrimSpace(note)); err != nil {
				return err
			}
			if _, err := insertEvent(ctx, tx, "comment.created", actorID, current.ProjectID, id, map[string]any{"comment_id": commentID}); err != nil {
				return err
			}
		}
		eventPayload := map[string]any{"column_id": column.ID}
		if state == "completed" {
			addChecklistCompletionEventFields(eventPayload, checklistStatus)
		}
		if _, err = insertEvent(ctx, tx, eventType, actorID, current.ProjectID, id, eventPayload); err != nil {
			return err
		}
		change, stateChanged, err := dependencyTaskStateChange(ctx, tx, beforeDependency)
		if err != nil {
			return err
		}
		if stateChanged {
			return emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change})
		}
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
