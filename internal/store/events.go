package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func scanEvent(scanner interface{ Scan(...any) error }) (Event, error) {
	var event Event
	var actor, project, task, payload sql.NullString
	if err := scanner.Scan(&event.Cursor, &event.ID, &event.Type, &actor, &project, &task, &payload, &event.CreatedAt); err != nil {
		return Event{}, err
	}
	event.ActorID, event.ProjectID, event.TaskID = nullableString(actor), nullableString(project), nullableString(task)
	if payload.Valid {
		event.Payload = json.RawMessage(payload.String)
	} else {
		event.Payload = json.RawMessage(`{}`)
	}
	return event, nil
}

func (s *Store) ListEvents(ctx context.Context, filter EventFilter) ([]Event, bool, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	query := `SELECT cursor, id, type, actor_id, project_id, task_id, payload, created_at FROM events WHERE cursor > ?`
	args := []any{filter.After}
	if filter.ProjectID != "" {
		query += ` AND project_id=?`
		args = append(args, filter.ProjectID)
	}
	if filter.ProjectIDs != nil && len(filter.ProjectIDs) == 0 {
		query += ` AND 1=0`
	}
	if len(filter.ProjectIDs) > 0 {
		placeholders := make([]string, len(filter.ProjectIDs))
		for i, projectID := range filter.ProjectIDs {
			placeholders[i] = "?"
			args = append(args, projectID)
		}
		query += ` AND project_id IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY cursor LIMIT ?`
	args = append(args, filter.Limit+1)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]Event, 0, filter.Limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, false, err
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > filter.Limit
	if hasMore {
		result = result[:filter.Limit]
	}
	return result, hasMore, nil
}

func (s *Store) ListRecentEvents(ctx context.Context, projectID string, limit int) ([]Event, error) {
	return s.ListRecentEventsScoped(ctx, projectID, nil, limit)
}

func (s *Store) ListRecentEventsScoped(ctx context.Context, projectID string, projectIDs []string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	query := `SELECT cursor, id, type, actor_id, project_id, task_id, payload, created_at FROM events WHERE 1=1`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id=?`
		args = append(args, projectID)
	}
	scope, scopeArgs := portableIDsClause("project_id", projectIDs)
	query += scope
	args = append(args, scopeArgs...)
	query += ` ORDER BY cursor DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Event, 0, limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
