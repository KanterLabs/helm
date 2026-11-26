package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxTaskOrderingPlacementLength  = 20
	maxTaskOrderingIdentifierLength = maxTaskMoveIdentifierLength
	maxTaskOrderingRevision         = int64(9223372036854775807)
)

// TaskReorderInput is an explicit alias for the guarded task movement shape.
// It is exposed separately so application callers can describe intent without
// depending on the legacy append-only operation name.
type TaskReorderInput = TaskMoveInput

// TaskReorderRequest is a descriptive alias for HTTP/application callers.
type TaskReorderRequest = TaskReorderInput

func preciseTaskMove(input TaskMoveInput) bool {
	return strings.TrimSpace(input.BeforeTaskID) != "" || strings.TrimSpace(input.AfterTaskID) != "" || strings.TrimSpace(input.Placement) != "" || input.ExpectedOrderingVersion != 0 || input.ExpectedSourceOrderingVersion != 0 || input.ExpectedDestinationOrderingVersion != 0
}

func normalizeTaskOrderingInput(input TaskMoveInput) (TaskMoveInput, error) {
	input.DestinationColumnID = strings.TrimSpace(input.DestinationColumnID)
	input.ExpectedSourceColumnID = strings.TrimSpace(input.ExpectedSourceColumnID)
	input.Source = strings.TrimSpace(input.Source)
	input.Reason = strings.TrimSpace(input.Reason)
	input.BeforeTaskID = strings.TrimSpace(input.BeforeTaskID)
	input.AfterTaskID = strings.TrimSpace(input.AfterTaskID)
	input.Placement = strings.ToLower(strings.TrimSpace(input.Placement))
	if input.DestinationColumnID == "" {
		return TaskMoveInput{}, invalid("destination_column_id is required", nil)
	}
	if len(input.DestinationColumnID) > maxTaskOrderingIdentifierLength || !utf8.ValidString(input.DestinationColumnID) {
		return TaskMoveInput{}, invalid("destination_column_id is invalid", nil)
	}
	if input.ExpectedSourceColumnID == "" {
		return TaskMoveInput{}, invalid("expected_source_column_id is required", nil)
	}
	if len(input.ExpectedSourceColumnID) > maxTaskOrderingIdentifierLength || !utf8.ValidString(input.ExpectedSourceColumnID) {
		return TaskMoveInput{}, invalid("expected_source_column_id is invalid", nil)
	}
	if input.Source == "" {
		return TaskMoveInput{}, invalid("source is required", nil)
	}
	if len(input.Source) > maxTaskMoveSourceLength || !utf8.ValidString(input.Source) {
		return TaskMoveInput{}, invalid("source is too long", nil)
	}
	if len(input.Reason) > maxTaskMoveReasonLength || !utf8.ValidString(input.Reason) {
		return TaskMoveInput{}, invalid("reason is too long", nil)
	}
	for label, value := range map[string]string{"before_task_id": input.BeforeTaskID, "after_task_id": input.AfterTaskID} {
		if value != "" && (len(value) > maxTaskOrderingIdentifierLength || !utf8.ValidString(value)) {
			return TaskMoveInput{}, invalid(label+" is invalid", nil)
		}
	}
	if len(input.Placement) > maxTaskOrderingPlacementLength || !utf8.ValidString(input.Placement) {
		return TaskMoveInput{}, invalid("placement is invalid", nil)
	}
	switch input.Placement {
	case "", "first", "last", "before", "after", "between":
	default:
		return TaskMoveInput{}, invalid("placement must be first, before, between, after, or last", nil)
	}
	if input.BeforeTaskID != "" && input.BeforeTaskID == input.AfterTaskID {
		return TaskMoveInput{}, invalid("before_task_id and after_task_id must differ", nil)
	}
	if input.Placement == "first" || input.Placement == "last" {
		if input.BeforeTaskID != "" || input.AfterTaskID != "" {
			return TaskMoveInput{}, invalid("first and last placement cannot include anchors", nil)
		}
	} else if input.Placement == "before" && (input.BeforeTaskID == "" || input.AfterTaskID != "") {
		return TaskMoveInput{}, invalid("before placement requires only before_task_id", nil)
	} else if input.Placement == "after" && (input.AfterTaskID == "" || input.BeforeTaskID != "") {
		return TaskMoveInput{}, invalid("after placement requires only after_task_id", nil)
	} else if input.Placement == "between" && (input.BeforeTaskID == "" || input.AfterTaskID == "") {
		return TaskMoveInput{}, invalid("between placement requires before_task_id and after_task_id", nil)
	}
	if input.ExpectedOrderingVersion < 0 || input.ExpectedSourceOrderingVersion < 0 || input.ExpectedDestinationOrderingVersion < 0 {
		return TaskMoveInput{}, invalid("ordering version must be non-negative", nil)
	}
	if input.ExpectedOrderingVersion > maxTaskOrderingRevision || input.ExpectedSourceOrderingVersion > maxTaskOrderingRevision || input.ExpectedDestinationOrderingVersion > maxTaskOrderingRevision {
		return TaskMoveInput{}, invalid("ordering version is invalid", nil)
	}
	if input.ExpectedOrderingVersion > 0 {
		if input.ExpectedSourceOrderingVersion == 0 {
			input.ExpectedSourceOrderingVersion = input.ExpectedOrderingVersion
		}
		if input.ExpectedDestinationOrderingVersion == 0 {
			input.ExpectedDestinationOrderingVersion = input.ExpectedOrderingVersion
		}
	}
	return input, nil
}

// ReorderTask places an unfinished task at a precise location. The task ETag
// guards the moved row; optional column revisions guard visible neighbors and
// make filtered-board operations fail safely when another reorder happened in
// the meantime. The operation is one SQLite transaction and emits one
// task.moved activity event with the placement metadata.
func (s *Store) ReorderTask(ctx context.Context, id string, input TaskReorderInput, expected int64, actorID string) (Task, error) {
	return s.reorderTask(ctx, id, input, expected, actorID, false)
}

func (s *Store) reorderTask(ctx context.Context, inputID string, input TaskMoveInput, expected int64, actorID string, strictDestination bool) (Task, error) {
	validated, err := normalizeTaskOrderingInput(input)
	if err != nil {
		return Task{}, err
	}
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	id := strings.TrimSpace(inputID)
	if id == "" {
		return Task{}, notFound("task not found")
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return Task{}, invalid("actor is required", nil)
	}

	var current Task
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// SQLite serializes writers, but acquire the writer lock before reading
		// anchors. Updating revisions in lexical order also keeps the operation
		// deterministic for database drivers that expose multiple connections.
		columnIDs := []string{validated.ExpectedSourceColumnID, validated.DestinationColumnID}
		sort.Strings(columnIDs)
		for index, columnID := range columnIDs {
			if index > 0 && columnID == columnIDs[index-1] {
				continue
			}
			result, lockErr := tx.ExecContext(ctx, `UPDATE columns SET ordering_version=ordering_version WHERE id=?`, columnID)
			if lockErr != nil {
				return lockErr
			}
			changed, _ := result.RowsAffected()
			if changed == 0 {
				return notFound("column not found")
			}
		}

		var readErr error
		current, readErr = taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=? AND t.deleted_at IS NULL`, id))
		if errors.Is(readErr, sql.ErrNoRows) {
			return notFound("task not found")
		}
		if readErr != nil {
			return readErr
		}
		if current.Version != expected || current.ColumnID != validated.ExpectedSourceColumnID {
			return conflict("task has changed", map[string]any{
				"current": current, "expected_version": expected,
				"expected_source_column_id": validated.ExpectedSourceColumnID,
				"destination_column_id":     validated.DestinationColumnID,
			})
		}
		if current.CompletedAt != nil {
			return invalid("finished tasks must use a lifecycle action", map[string]any{"current": current})
		}

		var source, destination Column
		if source, readErr = columnByIDTx(ctx, tx, validated.ExpectedSourceColumnID); readErr != nil {
			return readErr
		}
		if destination, readErr = columnByIDTx(ctx, tx, validated.DestinationColumnID); readErr != nil {
			return readErr
		}
		if source.ProjectID != current.ProjectID || destination.ProjectID != current.ProjectID {
			return invalid("task columns must belong to the same project", map[string]any{"current": current})
		}
		if source.ArchivedAt != nil || destination.ArchivedAt != nil {
			return invalid("task columns must not be archived", map[string]any{"current": current})
		}
		if !taskOrderingStateAllowed(source.SemanticState) {
			return invalid("task source column is not reorderable", map[string]any{"current": current})
		}
		if strictDestination {
			if !taskMoveDestinationStateAllowed(destination.SemanticState) {
				return invalid("destination column must have backlog or ready semantic state", nil)
			}
		} else if !taskOrderingStateAllowed(destination.SemanticState) {
			return invalid("destination column is not reorderable", nil)
		}
		if err := verifyTaskOrderingRevision(validated, source, destination); err != nil {
			return err
		}
		if err := activeTaskClaimConflict(ctx, tx, current, actorID); err != nil {
			return err
		}
		if source.SemanticState != "active" && destination.SemanticState == "active" {
			if err := checkProjectActiveCapacityTx(ctx, tx, current.ProjectID, 1); err != nil {
				return err
			}
			if err := requireLegacyActivePlacementTx(ctx, tx, actorID); err != nil {
				return err
			}
		}
		if source.OrderingVersion >= maxTaskOrderingRevision || destination.OrderingVersion >= maxTaskOrderingRevision {
			return invalid("task ordering revision is exhausted", nil)
		}

		items, listErr := listOrderedTasksTx(ctx, tx, current.ProjectID, destination.ID, id)
		if listErr != nil {
			return listErr
		}
		before, after, boundsErr := orderingBounds(items, validated)
		if boundsErr != nil {
			return conflict(boundsErr.Error(), map[string]any{
				"current": current, "before_task_id": validated.BeforeTaskID,
				"after_task_id":    validated.AfterTaskID,
				"ordering_version": destination.OrderingVersion,
			})
		}
		position, rebalanced, positionErr := chooseOrderingPosition(ctx, tx, items, before, after)
		if positionErr != nil {
			return positionErr
		}

		// The guarded update remains the final authority for version, source,
		// lifecycle, and claim state even though the writer lock was acquired
		// before the reads above.
		updatedAt := now()
		result, updateErr := tx.ExecContext(ctx, `UPDATE tasks
			SET column_id=?, position=?,
				claimed_by=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claimed_by ELSE NULL END,
				claim_expires_at=CASE WHEN claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) IS NOT NULL AND julianday(claim_expires_at)>julianday('now') THEN claim_expires_at ELSE NULL END,
				version=version+1, updated_at=?
			WHERE id=? AND version=? AND column_id=? AND deleted_at IS NULL AND completed_at IS NULL AND version < 9223372036854775807
				AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) IS NULL OR julianday(claim_expires_at)<=julianday('now'))`,
			validated.DestinationColumnID, position, updatedAt, id, expected, validated.ExpectedSourceColumnID)
		if updateErr != nil {
			return mapDependencyLifecycleError(ctx, tx, updateErr, dependencyLifecycleTarget{TaskID: id})
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			return conflict("task reorder could not be applied", map[string]any{"current": current})
		}

		resultingVersion := expected + 1
		if err := tx.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id=?`, id).Scan(&resultingVersion); err != nil {
			return err
		}
		var newPosition float64
		if err := tx.QueryRowContext(ctx, `SELECT position FROM tasks WHERE id=?`, id).Scan(&newPosition); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT ordering_version FROM columns WHERE id=?`, source.ID).Scan(&source.OrderingVersion); err != nil {
			return err
		}
		if source.ID == destination.ID {
			destination.OrderingVersion = source.OrderingVersion
		} else if err := tx.QueryRowContext(ctx, `SELECT ordering_version FROM columns WHERE id=?`, destination.ID).Scan(&destination.OrderingVersion); err != nil {
			return err
		}
		payload := map[string]any{
			"from":           map[string]any{"id": source.ID, "name": source.Name, "semantic_state": source.SemanticState},
			"to":             map[string]any{"id": destination.ID, "name": destination.Name, "semantic_state": destination.SemanticState},
			"from_column_id": source.ID, "to_column_id": destination.ID,
			"from_column": source.Name, "to_column": destination.Name,
			"from_semantic_state": source.SemanticState, "to_semantic_state": destination.SemanticState,
			"from_column_state": source.SemanticState, "to_column_state": destination.SemanticState,
			"old_position": current.Position, "new_position": newPosition,
			"version": resultingVersion, "resulting_version": resultingVersion,
			"actor": actorID, "actor_id": actorID, "source": validated.Source, "reason": validated.Reason,
			"before_task_id": validated.BeforeTaskID, "after_task_id": validated.AfterTaskID,
			"placement": orderingPlacement(validated), "rebalanced": rebalanced,
			"ordering_version":        destination.OrderingVersion,
			"source_ordering_version": source.OrderingVersion,
		}
		_, eventErr := insertEvent(ctx, tx, "task.moved", actorID, current.ProjectID, id, payload)
		return eventErr
	})
	if err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrClaimUnavailable) {
			if latest, latestErr := s.GetTask(ctx, id); latestErr == nil {
				attachTaskMoveCurrent(err, latest)
			}
		}
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func taskOrderingStateAllowed(state string) bool {
	return state == "backlog" || state == "ready" || state == "active" || state == "blocked"
}

func columnByIDTx(ctx context.Context, tx *sql.Tx, id string) (Column, error) {
	var column Column
	var archived sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, project_id, name, semantic_state, position, archived_at, ordering_version, created_at, updated_at, version FROM columns WHERE id=?`, id).Scan(
		&column.ID, &column.ProjectID, &column.Name, &column.SemanticState, &column.Position, &archived, &column.OrderingVersion, &column.CreatedAt, &column.UpdatedAt, &column.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("column not found")
	}
	column.ArchivedAt = nullableString(archived)
	return column, err
}

func verifyTaskOrderingRevision(input TaskMoveInput, source, destination Column) error {
	if input.ExpectedSourceOrderingVersion > 0 && source.OrderingVersion != input.ExpectedSourceOrderingVersion {
		return conflict("source column order has changed", map[string]any{"expected_ordering_version": input.ExpectedSourceOrderingVersion, "current_ordering_version": source.OrderingVersion, "column_id": source.ID})
	}
	if input.ExpectedDestinationOrderingVersion > 0 && destination.OrderingVersion != input.ExpectedDestinationOrderingVersion {
		return conflict("destination column order has changed", map[string]any{"expected_ordering_version": input.ExpectedDestinationOrderingVersion, "current_ordering_version": destination.OrderingVersion, "column_id": destination.ID})
	}
	return nil
}

func activeTaskClaimConflict(ctx context.Context, tx *sql.Tx, current Task, actorID string) error {
	if current.ClaimedBy == nil || current.ClaimExpiresAt == nil {
		return nil
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND deleted_at IS NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at)>julianday('now'))`, current.ID).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return &Error{Kind: ErrClaimUnavailable, Message: "task has an active claim; release it before reordering", Details: map[string]any{
			"claimed_by": *current.ClaimedBy, "claim_expires_at": *current.ClaimExpiresAt, "owned_by_caller": *current.ClaimedBy == actorID,
		}}
	}
	return nil
}
