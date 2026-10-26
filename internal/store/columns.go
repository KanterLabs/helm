package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) ListColumns(ctx context.Context, projectID string) ([]Column, error) {
	return s.listColumns(ctx, projectID, false)
}

// ListColumnsIncludingArchived returns active columns followed by archived
// columns in stable board order. It is used by administration surfaces; the
// ordinary board-facing ListColumns call intentionally hides archived rows.
func (s *Store) ListColumnsIncludingArchived(ctx context.Context, projectID string) ([]Column, error) {
	return s.listColumns(ctx, projectID, true)
}

func (s *Store) listColumns(ctx context.Context, projectID string, includeArchived bool) ([]Column, error) {
	query := columnSelect + ` WHERE project_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY archived_at IS NOT NULL, position, id`
	rows, err := s.DB.QueryContext(ctx, query, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Column, 0)
	for rows.Next() {
		column, err := columnFromRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, column)
	}
	return result, rows.Err()
}

// ListColumnsPage returns one cursor page and whether another row follows it.
// The query fetches one sentinel row so callers can determine the next cursor
// without issuing a count query. The public page size is capped at 200.

func (s *Store) ListColumnsPage(ctx context.Context, projectID string, limit, offset int) ([]Column, bool, error) {
	return s.ListColumnsPageFiltered(ctx, projectID, limit, offset, false)
}

// ListColumnsPageFiltered is the cursor-paginated administration variant. A
// false includeArchived value retains the existing board API behavior.
func (s *Store) ListColumnsPageFiltered(ctx context.Context, projectID string, limit, offset int, includeArchived bool) ([]Column, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := columnSelect + ` WHERE project_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	query += ` ORDER BY archived_at IS NOT NULL, position, id LIMIT ? OFFSET ?`
	rows, err := s.DB.QueryContext(ctx, query, projectID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Column, 0, limit)
	for rows.Next() {
		column, err := columnFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, column)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

func (s *Store) GetColumn(ctx context.Context, id string) (Column, error) {
	row := s.DB.QueryRowContext(ctx, columnSelect+` WHERE id = ?`, id)
	column, err := columnFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Column{}, notFound("column not found")
	}
	return column, err
}

func validateColumn(input ColumnInput, creating bool) (ColumnInput, error) {
	if creating && input.Name == nil {
		return ColumnInput{}, invalid("name is required", nil)
	}
	if creating && input.SemanticState == nil {
		return ColumnInput{}, invalid("semantic_state is required", nil)
	}
	if input.Name != nil {
		value := strings.TrimSpace(*input.Name)
		if value == "" || len(value) > 100 {
			return ColumnInput{}, invalid("column name must be between 1 and 100 characters", nil)
		}
		input.Name = &value
	}
	if input.SemanticState != nil {
		value := *input.SemanticState
		if !validState(value) {
			return ColumnInput{}, invalid("semantic_state is invalid", nil)
		}
		input.SemanticState = &value
	}
	if input.Position != nil && *input.Position < 0 {
		return ColumnInput{}, invalid("position must be non-negative", nil)
	}
	return input, nil
}

func validState(value string) bool {
	switch value {
	case "backlog", "ready", "active", "blocked", "completed":
		return true
	}
	return false
}

func (s *Store) CreateColumn(ctx context.Context, projectID string, input ColumnInput, actorID string) (Column, error) {
	validated, err := validateColumn(input, true)
	if err != nil {
		return Column{}, err
	}
	state := "backlog"
	if validated.SemanticState != nil {
		state = *validated.SemanticState
	}
	id, created := newID(), now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var projectExists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM projects WHERE id=?`, projectID).Scan(&projectExists); err != nil {
			return err
		}
		if projectExists == 0 {
			return notFound("project not found")
		}
		active, err := liveColumnIDsTx(ctx, tx, projectID)
		if err != nil {
			return err
		}
		position := len(active)
		if validated.Position != nil {
			position = *validated.Position
			if position > len(active) {
				position = len(active)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES (?, ?, ?, ?, -1, ?, ?)`, id, projectID, *validated.Name, state, created, created); err != nil {
			return err
		}
		active = append(active, "")
		copy(active[position+1:], active[position:])
		active[position] = id
		if err := assignLiveColumnPositionsTx(ctx, tx, projectID, active, created, id); err != nil {
			return err
		}
		_, err = insertEvent(ctx, tx, "column.created", actorID, projectID, "", map[string]any{"column_id": id, "semantic_state": state, "position": position, "version": 1})
		return err
	})
	if err != nil {
		return Column{}, err
	}
	return s.GetColumn(ctx, id)
}
