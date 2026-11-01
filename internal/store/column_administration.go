package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) UpdateColumn(ctx context.Context, id string, input ColumnInput, actorID string) (Column, error) {
	return s.UpdateColumnWithVersion(ctx, id, input, actorID, false, nil)
}

// UpdateColumnWithClaimOverride updates a column and synchronizes the derived
// completion fields on all of its live tasks when the semantic state changes.
// A human administrator may explicitly override active claims; all other
// callers are rejected before any column or task row is changed.
func (s *Store) UpdateColumnWithClaimOverride(ctx context.Context, id string, input ColumnInput, actorID string, allowClaimOverride bool) (Column, error) {
	return s.UpdateColumnWithVersion(ctx, id, input, actorID, allowClaimOverride, nil)
}

// UpdateColumnWithVersion updates a column's label, semantic mapping, board
// order, or archive state in one transaction. Active task rows are rehomed to
// another column with the same semantic state before an archived column is
// hidden. The five semantic states must always retain at least one active
// column, and expected guards make stale administration edits recoverable.
func (s *Store) UpdateColumnWithVersion(ctx context.Context, id string, input ColumnInput, actorID string, allowClaimOverride bool, expected *int64) (Column, error) {
	validated, err := validateColumn(input, false)
	if err != nil {
		return Column{}, err
	}
	if expected != nil && *expected <= 0 {
		return Column{}, ErrPrecondition
	}
	updated := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, columnSelect+` WHERE id=?`, id)
		current, err := columnFromRow(row)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("column not found")
		}
		if err != nil {
			return err
		}
		name, state, position := current.Name, current.SemanticState, current.Position
		archived := current.ArchivedAt != nil
		if validated.Name != nil {
			name = *validated.Name
		}
		if validated.SemanticState != nil {
			state = *validated.SemanticState
		}
		if validated.Position != nil {
			position = *validated.Position
		}
		if validated.Archived != nil {
			archived = *validated.Archived
		}
		stateChanged := state != current.SemanticState
		willBeLive := !archived
		wasLive := current.ArchivedAt == nil
		// The unguarded legacy entry point historically allowed semantic
		// remapping (including the final column for a state). Keep that contract
		// for existing automation, while versioned administration edits enforce
		// the invariant so the new UI cannot remove a state's only mapping.
		if wasLive && (!willBeLive || (stateChanged && expected != nil)) {
			var stateCount int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM columns WHERE project_id=? AND semantic_state=? AND archived_at IS NULL`, current.ProjectID, current.SemanticState).Scan(&stateCount); err != nil {
				return err
			}
			if stateCount <= 1 {
				return &Error{Kind: ErrConflict, Message: fmt.Sprintf("cannot remove the only active column mapped to %s", current.SemanticState), Details: map[string]any{"column_id": id, "semantic_state": current.SemanticState}}
			}
		}
		if stateChanged && wasLive && !allowClaimOverride {
			claimed, details, claimErr := activeClaimInColumnTx(ctx, tx, id, updated)
			if claimErr != nil {
				return claimErr
			}
			if claimed {
				return &Error{Kind: ErrClaimUnavailable, Message: "column contains actively claimed tasks", Details: details}
			}
		}
		// A temporary negative position lets the partial unique index accept a
		// reorder/restore before all active rows are assigned their final slots.
		query := `UPDATE columns SET name=?, semantic_state=?, position=-1, archived_at=?, version=version+1, updated_at=? WHERE id=?`
		args := []any{name, state, nullableTimeValue(archived, updated), updated, id}
		if expected != nil {
			query += ` AND version=?`
			args = append(args, *expected)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{ColumnID: id, NextSemanticState: state})
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if expected != nil {
				var currentVersion int64
				if versionErr := tx.QueryRowContext(ctx, `SELECT version FROM columns WHERE id=?`, id).Scan(&currentVersion); errors.Is(versionErr, sql.ErrNoRows) {
					return ErrNotFound
				} else if versionErr != nil {
					return versionErr
				}
				return conflict("column has changed", map[string]any{"column_id": id, "expected_version": *expected, "current_version": currentVersion})
			}
			return ErrNotFound
		}
		var dependencyChanges []dependencyStateChange
		checklistStatus := checklistCompletionStatus{}
		if stateChanged && wasLive && willBeLive {
			if err := validateColumnBugLifecycleTransitionTx(ctx, tx, id, state); err != nil {
				return err
			}
		}
		if stateChanged && wasLive && willBeLive && state == "completed" {
			// The guarded column update has acquired the writer transaction, so
			// require-policy rejection rolls back the column itself before any
			// task, dependency, archive, or activity row is changed.
			checklistStatus, err = checklistCompletionStatusForColumnTx(ctx, tx, current.ProjectID, id)
			if err != nil {
				return err
			}
			if err := rejectIncompleteChecklist(checklistStatus); err != nil {
				return err
			}
		}
		// Changing the mapping of a live column updates the derived completion
		// state of its tasks. An archived column is drained below, so applying
		// the intermediate mapping to those tasks would briefly (and
		// incorrectly) mark them complete before moving them to the fallback
		// column.
		if stateChanged && wasLive && willBeLive {
			dependencyChanges, err = dependencyColumnStateChanges(ctx, tx, current, state, updated)
			if err != nil {
				return err
			}
			var taskQuery string
			var taskArgs []any
			switch {
			case state == "completed":
				taskQuery = `UPDATE tasks SET completed_at=?, updated_at=?, version=version+1 WHERE column_id=? AND deleted_at IS NULL`
				taskArgs = []any{updated, updated, id}
			case current.SemanticState == "completed":
				taskQuery = `UPDATE tasks SET completed_at=NULL, updated_at=?, version=version+1 WHERE column_id=? AND deleted_at IS NULL`
				taskArgs = []any{updated, id}
			default:
				taskQuery = `UPDATE tasks SET updated_at=?, version=version+1 WHERE column_id=? AND deleted_at IS NULL`
				taskArgs = []any{updated, id}
			}
			if _, err := tx.ExecContext(ctx, taskQuery, taskArgs...); err != nil {
				return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{ColumnID: id, NextSemanticState: state})
			}
		}
		var taskCount int
		if !willBeLive && wasLive {
			destination, destinationErr := liveColumnForStateTx(ctx, tx, current.ProjectID, current.SemanticState, id)
			if destinationErr != nil {
				return destinationErr
			}
			taskIDs, taskErr := taskIDsInColumnTx(ctx, tx, id)
			if taskErr != nil {
				return taskErr
			}
			taskCount = len(taskIDs)
			var nextDestinationPosition float64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position)+1, 0) FROM tasks WHERE column_id=? AND deleted_at IS NULL`, destination.ID).Scan(&nextDestinationPosition); err != nil {
				return err
			}
			for index, taskID := range taskIDs {
				if _, err := tx.ExecContext(ctx, `UPDATE tasks SET column_id=?, position=?, version=version+1, updated_at=? WHERE id=? AND deleted_at IS NULL`, destination.ID, nextDestinationPosition+float64(index), updated, taskID); err != nil {
					return err
				}
			}
		}
		if willBeLive {
			active, activeErr := liveColumnIDsTx(ctx, tx, current.ProjectID)
			if activeErr != nil {
				return activeErr
			}
			ordered := moveColumnID(active, id, position)
			if err := assignLiveColumnPositionsTx(ctx, tx, current.ProjectID, ordered, updated, id); err != nil {
				return err
			}
		} else {
			// Removing a live column leaves a gap unless the remaining board rows
			// are compacted before the transaction commits. Keep active positions
			// contiguous so a subsequent restore can use an intentional position
			// and board clients never render misleading ordinal labels.
			active, activeErr := liveColumnIDsTx(ctx, tx, current.ProjectID)
			if activeErr != nil {
				return activeErr
			}
			if err := assignLiveColumnPositionsTx(ctx, tx, current.ProjectID, active, updated); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE columns SET position=?, updated_at=? WHERE id=?`, position, updated, id); err != nil {
				return err
			}
		}
		eventPayload := map[string]any{"column_id": id, "name": name, "semantic_state": state, "archived": archived, "position": position, "task_count": taskCount, "version": current.Version + 1}
		if stateChanged && wasLive && willBeLive && state == "completed" {
			addChecklistCompletionEventFields(eventPayload, checklistStatus)
		}
		if _, err = insertEvent(ctx, tx, "column.updated", actorID, current.ProjectID, "", eventPayload); err != nil {
			return err
		}
		return emitDependencyStateChanges(ctx, tx, actorID, dependencyChanges)
	})
	if err != nil {
		return Column{}, err
	}
	return s.GetColumn(ctx, id)
}
