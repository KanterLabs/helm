package store

import (
	"context"
	"database/sql"
	"errors"
)

// backlogIntakeColumn chooses a conventional Bugs column only for bugs.
// Naming is intentionally optional: renaming, archiving or reclassifying the
// column restores normal backlog selection, without recreating user columns.
func (s *Store) backlogIntakeColumn(ctx context.Context, projectID, kind string) (Column, error) {
	preferBugs := kind == bugKind
	row := s.DB.QueryRowContext(ctx, columnSelect+` WHERE project_id = ? AND semantic_state = 'backlog' AND archived_at IS NULL
		ORDER BY (lower(trim(name)) = 'bugs') = ? DESC, position, id LIMIT 1`, projectID, preferBugs)
	column, err := columnFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("backlog column not found")
	}
	return column, err
}

// bugIntakeColumnTx returns only the optional preferred bug destination.
// Callers retain their existing status-matching fallback when it is absent.
func bugIntakeColumnTx(ctx context.Context, tx *sql.Tx, projectID string) (string, bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM columns WHERE project_id = ?
		AND archived_at IS NULL AND semantic_state = 'backlog' AND lower(trim(name)) = 'bugs'
		ORDER BY position, id LIMIT 1`, projectID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}
