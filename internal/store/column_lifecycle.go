package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func nullableTimeValue(enabled bool, timestamp string) any {
	if enabled {
		return timestamp
	}
	return nil
}

func liveColumnIDsTx(ctx context.Context, tx *sql.Tx, projectID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM columns WHERE project_id=? AND archived_at IS NULL ORDER BY position, id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func assignLiveColumnPositionsTx(ctx context.Context, tx *sql.Tx, projectID string, ids []string, timestamp string, unchangedVersionID ...string) error {
	// SQLite checks the partial unique index as each row is updated. Move every
	// live position into a disjoint negative range first; a simple sign flip
	// would collide with the row currently occupying the destination slot.
	if _, err := tx.ExecContext(ctx, `UPDATE columns SET position=-1000000-position WHERE project_id=? AND archived_at IS NULL`, projectID); err != nil {
		return err
	}
	keepVersionID := ""
	if len(unchangedVersionID) > 0 {
		keepVersionID = unchangedVersionID[0]
	}
	for position, id := range ids {
		// A guarded metadata edit must also detect a concurrent reorder that
		// shifted this row. Bump versions only when the position actually
		// changed, while leaving the row whose mutation already incremented its
		// version at exactly one increment for this transaction.
		if _, err := tx.ExecContext(ctx, `UPDATE columns SET position=?, version=CASE WHEN id=? OR position=? THEN version ELSE version+1 END, updated_at=? WHERE id=? AND project_id=? AND archived_at IS NULL`, position, keepVersionID, -1000000-position, timestamp, id, projectID); err != nil {
			return err
		}
	}
	return nil
}

func moveColumnID(ids []string, target string, position int) []string {
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != target {
			ordered = append(ordered, id)
		}
	}
	if position < 0 {
		position = 0
	}
	if position > len(ordered) {
		position = len(ordered)
	}
	ordered = append(ordered, "")
	copy(ordered[position+1:], ordered[position:])
	ordered[position] = target
	return ordered
}

func liveColumnForStateTx(ctx context.Context, tx *sql.Tx, projectID, state, excludeID string) (Column, error) {
	row := tx.QueryRowContext(ctx, columnSelect+` WHERE project_id=? AND semantic_state=? AND archived_at IS NULL AND id<>? ORDER BY position, id LIMIT 1`, projectID, state, excludeID)
	column, err := columnFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, &Error{Kind: ErrConflict, Message: fmt.Sprintf("cannot archive the only active column mapped to %s", state), Details: map[string]any{"semantic_state": state}}
	}
	return column, err
}

func taskIDsInColumnTx(ctx context.Context, tx *sql.Tx, columnID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE column_id=? AND deleted_at IS NULL ORDER BY position, id`, columnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func activeClaimInColumnTx(ctx context.Context, tx *sql.Tx, columnID, timestamp string) (bool, map[string]any, error) {
	var taskID, claimedBy, expiresAt string
	err := tx.QueryRowContext(ctx, `SELECT id, claimed_by, claim_expires_at FROM tasks WHERE column_id=? AND deleted_at IS NULL AND claimed_by IS NOT NULL AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday(?) ORDER BY id LIMIT 1`, columnID, timestamp).Scan(&taskID, &claimedBy, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, map[string]any{"task_id": taskID, "claimed_by": claimedBy, "claim_expires_at": expiresAt}, nil
}

// authoritativeColumnStateTx reads a live project column after the caller has
// acquired SQLite's writer transaction. Task mutations use this to close the
// gap between their read-only preflight and guarded write when an administrator
// concurrently archives or reclassifies a column.
func authoritativeColumnStateTx(ctx context.Context, tx dependencySQL, columnID, projectID string) (string, error) {
	var actualProjectID, state string
	var archivedAt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT project_id, semantic_state, archived_at FROM columns WHERE id=?`, columnID).Scan(&actualProjectID, &state, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound("column not found")
	}
	if err != nil {
		return "", err
	}
	if actualProjectID != projectID {
		return "", invalid("column belongs to another project", nil)
	}
	if archivedAt.Valid {
		return "", conflict("column changed during the task update", map[string]any{"column_id": columnID, "archived": true})
	}
	return state, nil
}

// validateColumnBugLifecycleTransitionTx keeps the invariant between a bug's
// resolution and its column mapping when an administrator changes a live
// column's semantic state. Returning an error rolls back the guarded column
// update and every derived task change in the same transaction.
func validateColumnBugLifecycleTransitionTx(ctx context.Context, tx *sql.Tx, columnID, state string) error {
	var taskID string
	var err error
	if state == "completed" {
		err = tx.QueryRowContext(ctx, `SELECT t.id FROM tasks t LEFT JOIN bug_details b ON b.task_id=t.id WHERE t.column_id=? AND t.deleted_at IS NULL AND t.kind='bug' AND b.resolution IS NULL ORDER BY t.id LIMIT 1`, columnID).Scan(&taskID)
		if err == nil {
			return conflict("column contains an unresolved bug", map[string]any{"column_id": columnID, "task_id": taskID, "semantic_state": state})
		}
	} else {
		err = tx.QueryRowContext(ctx, `SELECT t.id FROM tasks t JOIN bug_details b ON b.task_id=t.id WHERE t.column_id=? AND t.deleted_at IS NULL AND t.kind='bug' AND b.resolution IS NOT NULL ORDER BY t.id LIMIT 1`, columnID).Scan(&taskID)
		if err == nil {
			return conflict("column contains a resolved bug", map[string]any{"column_id": columnID, "task_id": taskID, "semantic_state": state})
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (s *Store) StateColumn(ctx context.Context, projectID, state string) (Column, error) {
	row := s.DB.QueryRowContext(ctx, columnSelect+` WHERE project_id = ? AND semantic_state = ? AND archived_at IS NULL ORDER BY CASE WHEN semantic_state = 'backlog' AND lower(trim(name)) = 'bugs' THEN 1 ELSE 0 END, position, id LIMIT 1`, projectID, state)
	column, err := columnFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound(fmt.Sprintf("%s column not found", state))
	}
	return column, err
}
