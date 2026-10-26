package store

import (
	"context"
	"database/sql"
	"strings"
)

func (s *Store) TriageBug(ctx context.Context, id string, input TriageBugInput, expected int64, actorID string) (Task, error) {
	return s.TriageBugWithClaimOverride(ctx, id, input, expected, actorID, false)
}

func (s *Store) TriageBugWithClaimOverride(ctx context.Context, id string, input TriageBugInput, expected int64, actorID string, allowClaimOverride bool) (Task, error) {
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if current.Kind != bugKind || current.Bug == nil {
		return Task{}, invalid("task is not a bug", nil)
	}
	if current.Bug.Resolution != nil {
		return Task{}, invalid("resolved bugs must be reopened before triage", nil)
	}
	provided := input.SeveritySet || input.Severity != nil
	if !provided {
		return Task{}, invalid("severity is required", nil)
	}
	var severity any
	if input.Severity != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Severity))
		if !validBugSeverity(value) {
			return Task{}, invalid("severity must be s1, s2, s3, or s4", nil)
		}
		severity = value
	}
	priority := current.Priority
	if input.Priority != nil {
		priority = strings.ToLower(strings.TrimSpace(*input.Priority))
		if !validPriority(priority) {
			return Task{}, invalid("priority must be low, normal, high, or urgent", nil)
		}
	}
	assignee := ""
	if current.Assignee != nil {
		assignee = *current.Assignee
	}
	if input.AssigneeSet || input.Assignee != nil {
		assignee = ""
		if input.Assignee != nil {
			assignee = strings.TrimSpace(*input.Assignee)
		}
		if assignee != "" {
			if _, err := s.GetActor(ctx, assignee); err != nil {
				return Task{}, invalid("assignee actor not found", nil)
			}
		}
	}
	columnID := current.ColumnID
	if input.ColumnID != nil {
		columnID = strings.TrimSpace(*input.ColumnID)
	}
	column, err := s.GetColumn(ctx, columnID)
	if err != nil {
		return Task{}, err
	}
	if column.ProjectID != current.ProjectID {
		return Task{}, invalid("column belongs to another project", nil)
	}
	if column.ArchivedAt != nil {
		return Task{}, invalid("column is archived", nil)
	}
	if column.SemanticState == "completed" && current.Bug.Resolution == nil {
		return Task{}, invalid("bugs must be resolved before entering a completed column", nil)
	}
	sourceColumn := column
	if current.ColumnID != column.ID {
		sourceColumn, err = s.GetColumn(ctx, current.ColumnID)
		if err != nil {
			return Task{}, err
		}
	}
	if sourceColumn.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", nil)
	}
	timestamp := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// Derive completion from the authoritative destination row in the guarded
		// UPDATE. The destination can be reclassified or archived while this
		// operation waits for SQLite's writer lock, so the preflight snapshot is
		// only an early validation hint.
		query := `UPDATE tasks SET priority=?, assignee_id=NULLIF(?, ''), column_id=?, completed_at=CASE WHEN (SELECT semantic_state FROM columns WHERE id=?)='completed' THEN CASE WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id)='completed' THEN tasks.completed_at ELSE ? END ELSE NULL END, version=version+1, updated_at=? WHERE id=? AND kind='bug' AND version=? AND deleted_at IS NULL`
		args := []any{priority, assignee, columnID, columnID, timestamp, timestamp, id, expected}
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
		destinationState, err := authoritativeColumnStateTx(ctx, tx, columnID, current.ProjectID)
		if err != nil {
			return err
		}
		sourceState := destinationState
		if current.ColumnID != columnID {
			sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
			if err != nil {
				return err
			}
		}
		if err := validateTaskBugLifecycleTx(ctx, tx, id, bugKind, destinationState); err != nil {
			return err
		}
		beforeDependency := dependencyTaskFromTask(current, sourceState)
		checklistStatus := checklistCompletionStatus{}
		completionTransition := sourceState != "completed" && destinationState == "completed"
		if completionTransition {
			checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
			if err != nil {
				return err
			}
			if err := rejectIncompleteChecklist(checklistStatus); err != nil {
				return err
			}
		}
		result, err = tx.ExecContext(ctx, `UPDATE bug_details SET severity=? WHERE task_id=?`, severity, id)
		if err != nil {
			return err
		}
		if changed, _ := result.RowsAffected(); changed == 0 {
			return invalid("bug details not found", nil)
		}
		eventPayload := map[string]any{"version": expected + 1}
		if completionTransition {
			addChecklistCompletionEventFields(eventPayload, checklistStatus)
		}
		if _, err = insertEvent(ctx, tx, "bug.triaged", actorID, current.ProjectID, id, eventPayload); err != nil {
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
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
