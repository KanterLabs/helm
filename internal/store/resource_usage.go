package store

import (
	"context"
	"database/sql"
	"errors"
)

// AgentMutationUsage reads legacy accounting retained for rollback safety.
// New mutations do not reserve lifetime usage.
func (s *Store) AgentMutationUsage(ctx context.Context, actorID string) (int64, error) {
	var used int64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(reserved_bytes, 0) FROM actor_resource_usage WHERE actor_id=?`, actorID).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return used, err
}
