package store

import (
	"context"
	"strings"
)

// GithubTaskProjects resolves immutable task IDs in one bounded local query.
func (s *Store) GithubTaskProjects(ctx context.Context, ids []string) (map[string]string, error) {
	result := map[string]string{}
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > 200 {
		return nil, invalid("at most 200 task IDs may be queried", nil)
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,project_id FROM tasks WHERE deleted_at IS NULL AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, project string
		if err := rows.Scan(&id, &project); err != nil {
			return nil, err
		}
		result[id] = project
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			return nil, ErrNotFound
		}
	}
	return result, nil
}
