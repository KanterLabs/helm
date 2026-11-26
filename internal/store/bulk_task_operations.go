package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func bulkClaimGuard(query string, args []any, actorID, updatedAt string, allowClaimOverride bool) (string, []any) {
	if allowClaimOverride {
		return query, args
	}
	return query + ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?) OR claimed_by=?)`, append(args, updatedAt, actorID)
}

func (s *Store) applyBulkUpdateTx(ctx context.Context, tx dependencySQL, current Task, sourceColumn Column, operation string, input TaskInput, expected int64, actorID, updatedAt string, allowClaimOverride bool) (Task, error) {
	validated, err := validateTaskInput(input, false)
	if err != nil {
		return Task{}, err
	}
	if sourceColumn.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", map[string]any{"current": current})
	}
	if validated.ColumnID != nil || validated.Position != nil || validated.Kind != nil || validated.BugSet || validated.Bug != nil {
		return Task{}, invalid("bulk update does not support column, position, kind, or bug changes", nil)
	}
	title, description, priority := current.Title, current.Description, current.Priority
	if validated.Title != nil {
		title = *validated.Title
	}
	if validated.Description != nil {
		description = *validated.Description
	}
	if validated.Priority != nil {
		priority = *validated.Priority
	}
	assignee := ""
	if current.Assignee != nil {
		assignee = *current.Assignee
	}
	if validated.AssigneeSet {
		if validated.Assignee != nil {
			assignee = strings.TrimSpace(*validated.Assignee)
		} else {
			assignee = ""
		}
	}
	if assignee != "" {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=?)`, assignee).Scan(&exists); err != nil {
			return Task{}, err
		}
		if exists == 0 {
			return Task{}, invalid("assignee actor not found", nil)
		}
	}
	dueAt := ""
	if current.DueAt != nil {
		dueAt = *current.DueAt
	}
	if validated.DueAtSet {
		if validated.DueAt != nil {
			dueAt = *validated.DueAt
		} else {
			dueAt = ""
		}
	}
	query := `UPDATE tasks SET title=?, description=?, priority=?, assignee_id=NULLIF(?, ''), due_at=NULLIF(?, ''), version=version+1, updated_at=? WHERE id=? AND version=? AND deleted_at IS NULL AND version < 9223372036854775807`
	args := []any{title, description, priority, assignee, dueAt, updatedAt, current.ID, expected}
	query, args = bulkClaimGuard(query, args, actorID, updatedAt, allowClaimOverride)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return Task{}, mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: current.ID})
	}
	var changed int64
	if err := tx.QueryRowContext(ctx, `SELECT changes()`).Scan(&changed); err != nil {
		return Task{}, err
	}
	if changed == 0 {
		return Task{}, s.bulkMutationFailure(ctx, tx, current, expected, actorID, false, false, false, allowClaimOverride, updatedAt)
	}
	// The source column can be archived while this transaction waits for the
	// SQLite writer lock. Keep field-only mutations aligned with the current
	// task PATCH path: an archived column never authorizes a task write.
	if _, err := authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID); err != nil {
		return Task{}, err
	}
	if validated.LabelsSet {
		if err := replaceTaskLabels(ctx, tx, current.ID, current.ProjectID, validated.Labels); err != nil {
			return Task{}, err
		}
	}
	eventPayload := map[string]any{
		"version": expected + 1, "bulk": true, "operation": operation,
	}
	// Keep assignment transitions in the event payload aligned with direct
	// task updates.  The notification read model uses these canonical fields
	// to identify the direct assignee and suppress same-value reassignments;
	// reading the task after the write would lose the previous value.
	if validated.AssigneeSet {
		eventPayload["assignee"] = assignee
		eventPayload["previous_assignee"] = nullableStringValue(current.Assignee)
		eventPayload["assignment_changed"] = assignee != nullableStringValue(current.Assignee)
	}
	if _, err := insertEvent(ctx, tx, "task.updated", actorID, current.ProjectID, current.ID, eventPayload); err != nil {
		return Task{}, err
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func (s *Store) applyBulkMoveTx(ctx context.Context, tx dependencySQL, current Task, source Column, input TaskMoveInput, expected int64, actorID string) (Task, error) {
	validated, err := validateTaskMoveInput(input)
	if err != nil {
		return Task{}, err
	}
	destination, err := bulkColumnTx(ctx, tx, validated.DestinationColumnID)
	if err != nil {
		return Task{}, err
	}
	if destination.ProjectID != current.ProjectID {
		return Task{}, invalid("destination column belongs to another project", nil)
	}
	if source.ArchivedAt != nil || destination.ArchivedAt != nil {
		return Task{}, invalid("task columns must not be archived", map[string]any{"current": current})
	}
	if source.ID != validated.ExpectedSourceColumnID {
		return Task{}, conflict("task has changed", map[string]any{"current": current, "expected_source_column_id": validated.ExpectedSourceColumnID})
	}
	if !taskOrderingStateAllowed(source.SemanticState) {
		return Task{}, invalid("task source column is not reorderable", map[string]any{"current": current})
	}
	if destination.SemanticState != "backlog" && destination.SemanticState != "ready" {
		return Task{}, invalid("destination column must have backlog or ready semantic state", nil)
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks
		SET column_id=?,
			position=COALESCE((SELECT MAX(other.position)+1 FROM tasks other WHERE other.column_id=? AND other.deleted_at IS NULL AND other.id<>?), 0),
			claimed_by=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claimed_by ELSE NULL END,
			claim_expires_at=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claim_expires_at ELSE NULL END,
			version=version+1,
			updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id=? AND version=? AND column_id=? AND deleted_at IS NULL AND completed_at IS NULL AND version < 9223372036854775807
			AND EXISTS (SELECT 1 FROM columns source_column WHERE source_column.id=tasks.column_id AND source_column.project_id=tasks.project_id AND source_column.archived_at IS NULL AND source_column.semantic_state IN ('backlog','ready','active','blocked'))
			AND EXISTS (SELECT 1 FROM columns destination_column WHERE destination_column.id=? AND destination_column.project_id=tasks.project_id AND destination_column.archived_at IS NULL AND destination_column.semantic_state IN ('backlog','ready'))
			AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) IS NULL OR julianday(claim_expires_at)<=julianday('now'))`,
		validated.DestinationColumnID, validated.DestinationColumnID, current.ID, current.ID, expected, validated.ExpectedSourceColumnID, validated.DestinationColumnID)
	if err != nil {
		return Task{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Task{}, s.taskMoveMutationFailure(ctx, tx, current.ID, actorID, expected, validated.ExpectedSourceColumnID, validated.DestinationColumnID)
	}
	var newPosition float64
	var resultingVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT position, version FROM tasks WHERE id=?`, current.ID).Scan(&newPosition, &resultingVersion); err != nil {
		return Task{}, err
	}
	// Re-read both columns after the guarded write so activity carries the
	// same authoritative names, semantic states, and ordering revisions as the
	// current direct move/reorder paths.
	source, err = bulkColumnTx(ctx, tx, source.ID)
	if err != nil {
		return Task{}, err
	}
	destination, err = bulkColumnTx(ctx, tx, destination.ID)
	if err != nil {
		return Task{}, err
	}
	payload := map[string]any{
		"from":           map[string]any{"id": source.ID, "name": source.Name, "semantic_state": source.SemanticState},
		"to":             map[string]any{"id": destination.ID, "name": destination.Name, "semantic_state": destination.SemanticState},
		"from_column_id": source.ID, "to_column_id": destination.ID, "from_column": source.Name, "to_column": destination.Name,
		"from_semantic_state": source.SemanticState, "to_semantic_state": destination.SemanticState,
		"from_column_state": source.SemanticState, "to_column_state": destination.SemanticState,
		"old_position": current.Position, "new_position": newPosition, "version": resultingVersion, "resulting_version": resultingVersion,
		"actor": actorID, "actor_id": actorID, "source": validated.Source, "reason": validated.Reason, "bulk": true,
		"placement": "last", "rebalanced": false,
		"ordering_version": destination.OrderingVersion, "source_ordering_version": source.OrderingVersion,
	}
	if _, err := insertEvent(ctx, tx, "task.moved", actorID, current.ProjectID, current.ID, payload); err != nil {
		return Task{}, err
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func (s *Store) applyBulkLifecycleTx(ctx context.Context, tx dependencySQL, current Task, source Column, operation string, expected int64, actorID, note string, requireClaim, allowClaimOverride bool) (Task, error) {
	state := "blocked"
	eventType := "task.blocked"
	if operation == "complete" {
		state, eventType = "completed", "task.completed"
	}
	if current.Kind == bugKind && state == "completed" {
		return Task{}, invalid("bugs must be completed with a resolution", nil)
	}
	if current.Kind == bugKind {
		bug, bugErr := bugFromRow(tx.QueryRowContext(ctx, `SELECT reporter_id, severity, actual_behavior, expected_behavior, reproduction_steps, environment, affected_version, resolution, resolved_by, resolved_at, duplicate_of FROM bug_details WHERE task_id=?`, current.ID))
		if bugErr == nil && bug.Resolution != nil && state != "completed" {
			return Task{}, invalid("resolved bugs must be reopened before leaving a completed column", nil)
		}
		if bugErr != nil && !errors.Is(bugErr, sql.ErrNoRows) {
			return Task{}, bugErr
		}
	}
	if source.ArchivedAt != nil {
		return Task{}, invalid("task is assigned to an archived column", map[string]any{"current": current})
	}
	target, err := columnBySemanticTx(ctx, tx, current.ProjectID, state)
	if err != nil {
		return Task{}, err
	}
	query := `UPDATE tasks SET column_id=?, completed_at=CASE WHEN ? <> 'completed' THEN NULL WHEN (SELECT semantic_state FROM columns WHERE id=tasks.column_id) = 'completed' THEN tasks.completed_at ELSE strftime('%Y-%m-%dT%H:%M:%fZ','now') END, claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND deleted_at IS NULL`
	args := []any{target.ID, state, current.ID, expected}
	if requireClaim {
		query += ` AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday('now')`
		args = append(args, actorID)
	} else if !allowClaimOverride {
		query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at)<=julianday('now') OR claimed_by=?)`
		args = append(args, actorID)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return Task{}, mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: current.ID})
	}
	var changed int64
	if err := tx.QueryRowContext(ctx, `SELECT changes()`).Scan(&changed); err != nil {
		return Task{}, err
	}
	if changed == 0 {
		return Task{}, s.bulkMutationFailure(ctx, tx, current, expected, actorID, requireClaim, false, true, allowClaimOverride, now())
	}
	destinationState, err := authoritativeColumnStateTx(ctx, tx, target.ID, current.ProjectID)
	if err != nil {
		return Task{}, err
	}
	if destinationState != state {
		return Task{}, conflict("task destination changed semantic state", map[string]any{"column_id": target.ID, "expected_state": state, "current_state": destinationState})
	}
	sourceState := destinationState
	if current.ColumnID != target.ID {
		sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
		if err != nil {
			return Task{}, err
		}
	}
	if err := validateTaskBugLifecycleTx(ctx, tx, current.ID, current.Kind, destinationState); err != nil {
		return Task{}, err
	}
	beforeDependency := dependencyTaskFromTask(current, sourceState)
	checklistStatus := checklistCompletionStatus{}
	if operation == "complete" && destinationState == "completed" {
		checklistStatus, err = checklistCompletionStatusForTaskTx(ctx, tx, current.ProjectID, current.ID)
		if err != nil {
			return Task{}, err
		}
		if err := rejectIncompleteChecklist(checklistStatus); err != nil {
			return Task{}, err
		}
	}
	if strings.TrimSpace(note) != "" {
		commentID := newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'), strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, commentID, current.ID, actorID, strings.TrimSpace(note)); err != nil {
			return Task{}, err
		}
		if _, err := insertEvent(ctx, tx, "comment.created", actorID, current.ProjectID, current.ID, map[string]any{"comment_id": commentID, "bulk": true}); err != nil {
			return Task{}, err
		}
	}
	eventPayload := map[string]any{"column_id": target.ID, "bulk": true}
	if operation == "complete" {
		addChecklistCompletionEventFields(eventPayload, checklistStatus)
	}
	if _, err := insertEvent(ctx, tx, eventType, actorID, current.ProjectID, current.ID, eventPayload); err != nil {
		return Task{}, err
	}
	change, stateChanged, err := dependencyTaskStateChange(ctx, tx, beforeDependency)
	if err != nil {
		return Task{}, err
	}
	if stateChanged {
		if err := emitDependencyStateChanges(ctx, tx, actorID, []dependencyStateChange{change}); err != nil {
			return Task{}, err
		}
	}
	return bulkTaskAfterMutationTx(ctx, tx, current.ID)
}

func (s *Store) bulkMutationFailure(ctx context.Context, tx dependencySQL, current Task, expected int64, actorID string, requireClaim, move, lifecycle, allowClaimOverride bool, claimAt string) error {
	latest, err := taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=? AND t.deleted_at IS NULL`, current.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("task not found")
	}
	if err != nil {
		return err
	}
	if latest.Version != expected {
		return conflict("task has changed", map[string]any{"current": latest, "expected_version": expected})
	}
	if !allowClaimOverride {
		if claimAt == "" {
			claimAt = now()
		}
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday(?) AND deleted_at IS NULL)`, current.ID, claimAt).Scan(&active); err != nil {
			return err
		}
		if active != 0 {
			if requireClaim {
				var owner int
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday(?) AND deleted_at IS NULL)`, current.ID, actorID, claimAt).Scan(&owner); err != nil {
					return err
				}
				if owner == 0 {
					return forbidden("an active claim owned by this actor is required")
				}
			} else if lifecycle {
				return forbidden("only the claim owner may perform this action")
			} else {
				return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed by another actor", Details: map[string]any{"task_id": current.ID}}
			}
		}
		if requireClaim {
			return forbidden("an active claim owned by this actor is required")
		}
	}
	if move {
		return conflict("task move could not be applied", map[string]any{"current": latest, "expected_version": expected})
	}
	return conflict("task has changed", map[string]any{"current": latest, "expected_version": expected})
}
