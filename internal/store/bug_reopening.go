package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) ReopenBug(ctx context.Context, id, reason string, expected int64, actorID string) (Task, error) {
	return s.ReopenBugWithClaimOverride(ctx, id, reason, expected, actorID, false)
}

func (s *Store) ReopenBugWithClaimOverride(ctx context.Context, id, reason string, expected int64, actorID string, allowClaimOverride bool) (Task, error) {
	if expected <= 0 {
		return Task{}, ErrPrecondition
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Task{}, invalid("reopen reason is required", nil)
	}
	if len(reason) > 10000 {
		return Task{}, invalid("reopen reason is too long", nil)
	}
	if strings.TrimSpace(actorID) == "" {
		return Task{}, invalid("a reopening actor is required", nil)
	}
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return Task{}, invalid("reopening actor not found", nil)
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if current.Kind != bugKind || current.Bug == nil {
		return Task{}, invalid("task is not a bug", nil)
	}
	if current.Bug.Resolution == nil {
		return Task{}, invalid("bug is not resolved", nil)
	}
	backlogColumn, err := s.backlogIntakeColumn(ctx, current.ProjectID, bugKind)
	if err != nil {
		return Task{}, err
	}
	timestamp := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		return s.reopenBugTx(ctx, tx, current, backlogColumn, actorID, expected, reason, timestamp, "backlog", allowClaimOverride)
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) resolveDuplicateTargetTx(ctx context.Context, tx *sql.Tx, projectID, sourceID, reference string) (string, error) {
	var targetID, targetProject, targetKind string
	err := tx.QueryRowContext(ctx, `SELECT t.id, t.project_id, t.kind FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.deleted_at IS NULL AND (t.id=? OR lower(p.key || '-' || CAST(t.number AS TEXT))=lower(?)) LIMIT 1`, reference, reference).Scan(&targetID, &targetProject, &targetKind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound("duplicate target not found")
	}
	if err != nil {
		return "", err
	}
	if targetID == sourceID {
		return "", invalid("a bug cannot duplicate itself", nil)
	}
	if targetProject != projectID {
		return "", invalid("duplicate target must be in the same project", nil)
	}
	if targetKind != bugKind {
		return "", invalid("duplicate target must be a bug", nil)
	}
	var cycle int
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE duplicate_chain(task_id) AS (
		SELECT target_task_id FROM task_links WHERE source_task_id=? AND link_type='duplicate'
		UNION
		SELECT l.target_task_id FROM task_links l JOIN duplicate_chain c ON l.source_task_id=c.task_id WHERE l.link_type='duplicate'
	) SELECT EXISTS(SELECT 1 FROM duplicate_chain WHERE task_id=?)`, targetID, sourceID).Scan(&cycle); err != nil {
		return "", err
	}
	if cycle != 0 {
		return "", invalid("duplicate link would create a cycle", nil)
	}
	return targetID, nil
}

func bugMutationFailure(ctx context.Context, tx *sql.Tx, id, actorID string, expected int64, timestamp string, current Task, allowClaimOverride bool) error {
	if !allowClaimOverride {
		claimed, details, err := activeClaimByOtherTx(ctx, tx, id, actorID, timestamp)
		if err != nil {
			return err
		}
		if claimed {
			return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed by another actor", Details: details}
		}
	}
	return conflict("task has changed", map[string]any{"current": current})
}

// reopenBugTx shares the normal lifecycle writer transaction with composed operations.
func (s *Store) reopenBugTx(ctx context.Context, tx *sql.Tx, current Task, backlogColumn Column, actorID string, expected int64, reason, timestamp, targetState string, allowClaimOverride bool) error {
	id := current.ID

	query := `UPDATE tasks SET column_id=?, completed_at=NULL, claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=? WHERE id=? AND kind='bug' AND version=? AND deleted_at IS NULL`
	args := []any{backlogColumn.ID, timestamp, id, expected}
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
	destinationState, err := authoritativeColumnStateTx(ctx, tx, backlogColumn.ID, current.ProjectID)
	if err != nil {
		return err
	}
	if destinationState != targetState {
		return conflict("bug destination changed semantic state", map[string]any{"column_id": backlogColumn.ID, "expected_state": targetState, "current_state": destinationState})
	}
	sourceState := destinationState
	if current.ColumnID != backlogColumn.ID {
		sourceState, err = authoritativeColumnStateTx(ctx, tx, current.ColumnID, current.ProjectID)
		if err != nil {
			return err
		}
	}
	beforeDependency := dependencyTaskFromTask(current, sourceState)
	if _, err := tx.ExecContext(ctx, `UPDATE bug_details SET resolution=NULL, resolved_by=NULL, resolved_at=NULL, duplicate_of=NULL WHERE task_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_links WHERE source_task_id=? AND link_type='duplicate'`, id); err != nil {
		return err
	}
	commentID := newID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, commentID, id, actorID, reason, timestamp, timestamp); err != nil {
		return err
	}
	if _, err := insertEvent(ctx, tx, "comment.created", actorID, current.ProjectID, id, map[string]any{"comment_id": commentID}); err != nil {
		return err
	}
	if _, err = insertEvent(ctx, tx, "bug.reopened", actorID, current.ProjectID, id, map[string]any{"version": expected + 1}); err != nil {
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
