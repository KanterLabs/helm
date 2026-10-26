package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) DeleteTask(ctx context.Context, id string, expected int64, actorID string) error {
	return s.DeleteTaskWithClaimOverride(ctx, id, expected, actorID, false)
}

// DeleteTaskWithClaimOverride is the delete counterpart to
// UpdateTaskWithClaimOverride.
func (s *Store) DeleteTaskWithClaimOverride(ctx context.Context, id string, expected int64, actorID string, allowClaimOverride bool) error {
	if expected <= 0 {
		return ErrPrecondition
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		updated := now()
		query := `UPDATE tasks SET deleted_at=?, updated_at=?, version=version+1 WHERE id=? AND version=? AND deleted_at IS NULL`
		args := []any{updated, updated, id, expected}
		if !allowClaimOverride {
			query += ` AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday(?) OR claimed_by=?)`
			args = append(args, updated, actorID)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			mapped := mapReleaseMutationError(ctx, tx, err, current.ProjectID, id)
			mapped = mapDependencyLifecycleError(ctx, tx, mapped, dependencyLifecycleTarget{TaskID: id, AllDependents: true})
			child := hierarchyTask{ID: current.ID, ProjectID: current.ProjectID, Number: current.Number, Key: current.Key}
			return mapHierarchyMutationError(mapped, child, hierarchyTask{})
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if !allowClaimOverride {
				claimed, details, claimErr := activeClaimByOtherTx(ctx, tx, id, actorID, updated)
				if claimErr != nil {
					return claimErr
				}
				if claimed {
					return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed by another actor", Details: details}
				}
			}
			return conflict("task has changed", map[string]any{"current": current})
		}
		_, err = insertEvent(ctx, tx, "task.deleted", actorID, current.ProjectID, id, map[string]any{"number": current.Number})
		return err
	})
	return err
}

// RestoreTask reverses one soft delete with the deleted version as its
// optimistic-concurrency token. The guarded UPDATE and restore event are one
// transaction, so an Undo cannot resurrect a task after another writer has
// changed or restored it. Retained claims are cleared on restore; an undo is
// not allowed to revive a lease whose owner may no longer be present.
func (s *Store) RestoreTask(ctx context.Context, id string, expected int64, actorID string) (Task, error) {
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	id = strings.TrimSpace(id)
	actorID = strings.TrimSpace(actorID)
	if id == "" {
		return Task{}, notFound("task not found")
	}
	if actorID == "" {
		return Task{}, invalid("actor is required", nil)
	}

	current, deleted, err := s.GetTaskForRestore(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if !deleted {
		return Task{}, conflict("task has already been restored", map[string]any{"current": current})
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, updateErr := tx.ExecContext(ctx, `UPDATE tasks
			SET deleted_at=NULL,
				claimed_by=NULL,
				claim_expires_at=NULL,
				version=version+1,
				updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
			WHERE id=? AND version=? AND deleted_at IS NOT NULL
				AND version < 9223372036854775807`, id, expected)
		if updateErr != nil {
			return mapDependencyLifecycleError(ctx, tx, updateErr, dependencyLifecycleTarget{TaskID: id})
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			latest, latestErr := taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=?`, id))
			if errors.Is(latestErr, sql.ErrNoRows) {
				return notFound("task not found")
			}
			if latestErr != nil {
				return latestErr
			}
			return conflict("task has changed", map[string]any{"current": latest})
		}
		_, eventErr := insertEvent(ctx, tx, "task.restored", actorID, current.ProjectID, id, map[string]any{
			"number":  current.Number,
			"version": expected + 1,
		})
		return eventErr
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
