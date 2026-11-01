package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// activeClaimByOtherTx reads the current row after a guarded mutation did not
// match. The mutation's SQL predicate is the authorization boundary; this
// follow-up only gives callers a useful error distinction from a stale version.
func activeClaimByOtherTx(ctx context.Context, tx *sql.Tx, id, actorID, timestamp string) (bool, map[string]any, error) {
	var claimedBy, claimExpiry sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT claimed_by, claim_expires_at FROM tasks WHERE id=? AND deleted_at IS NULL`, id).Scan(&claimedBy, &claimExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if !claimedBy.Valid || claimedBy.String == actorID || !claimExpiry.Valid {
		return false, nil, nil
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND deleted_at IS NULL AND claimed_by IS NOT NULL AND claimed_by<>? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday(?))`, id, actorID, timestamp).Scan(&active); err != nil {
		return false, nil, err
	}
	if active == 0 {
		return false, nil, nil
	}
	return true, map[string]any{"claimed_by": claimedBy.String, "claim_expires_at": claimExpiry.String}, nil
}

// taskClaimStateTx returns the current version and whether actorID owns an
// active lease. It is used only after a guarded UPDATE matched zero rows, so
// the authorization decision itself remains in the UPDATE predicate.
func taskClaimStateTx(ctx context.Context, tx *sql.Tx, id, actorID string) (int64, bool, error) {
	var version int64
	var claimedBy, claimExpiry sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT version, claimed_by, claim_expires_at FROM tasks WHERE id=? AND deleted_at IS NULL`, id).Scan(&version, &claimedBy, &claimExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var activeValue int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND deleted_at IS NULL AND claimed_by=? AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday('now'))`, id, actorID).Scan(&activeValue); err != nil {
		return 0, false, err
	}
	active := claimedBy.Valid && claimedBy.String == actorID && claimExpiry.Valid && activeValue != 0
	return version, active, nil
}

func replaceTaskLabels(ctx context.Context, tx dependencySQL, taskID, projectID string, values []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_labels WHERE task_id=?`, taskID); err != nil {
		return err
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		var id string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM labels WHERE project_id=? AND (id=? OR lower(name)=lower(?))`, projectID, value, value).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalid("label not found: "+value, nil)
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_labels(task_id,label_id) VALUES (?,?)`, taskID, id); err != nil {
			return err
		}
	}
	return nil
}

// NormalizeTaskClaimDuration applies the lease contract shared by direct
// store callers and HTTP handlers. A zero duration means the caller omitted a
// duration and receives the documented default; every explicit duration must
// be within the inclusive minimum and maximum.
func NormalizeTaskClaimDuration(duration time.Duration) (time.Duration, error) {
	if duration == 0 {
		return DefaultTaskClaimDuration, nil
	}
	if duration < MinTaskClaimDuration || duration > MaxTaskClaimDuration {
		return 0, invalid("lease duration must be between 30 and 604800 seconds", nil)
	}
	return duration, nil
}

func (s *Store) ClaimTask(ctx context.Context, id, actorID string, duration time.Duration, expected int64) (Task, error) {
	var err error
	duration, err = NormalizeTaskClaimDuration(duration)
	if err != nil {
		return Task{}, err
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	// Keep the duration as a SQLite date modifier. The clock itself is read by
	// the guarded UPDATE, after it acquires SQLite's writer lock; calculating an
	// expiry or updated_at value in Go here would let lock wait time age both
	// values before the claim is authorized.
	expiresModifier := fmt.Sprintf("+%.9f seconds", duration.Seconds())
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE tasks SET claimed_by=?, claim_expires_at=strftime('%Y-%m-%dT%H:%M:%fZ','now',?), version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND deleted_at IS NULL AND completed_at IS NULL AND (claimed_by=? OR claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) <= julianday('now'))`, actorID, expiresModifier, id, expected, actorID)
		if err != nil {
			return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: id})
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if current.Version != expected {
				return conflict("task has changed", map[string]any{"current": current})
			}
			if current.CompletedAt != nil {
				return conflict("task is already finished", nil)
			}
			return &Error{Kind: ErrClaimUnavailable, Message: "task is currently claimed", Details: map[string]any{"current": current}}
		}
		var expires string
		if err := tx.QueryRowContext(ctx, `SELECT claim_expires_at FROM tasks WHERE id=?`, id).Scan(&expires); err != nil {
			return err
		}
		_, err = insertEvent(ctx, tx, "task.claimed", actorID, current.ProjectID, id, map[string]any{"expires_at": expires})
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) RenewTask(ctx context.Context, id, actorID string, duration time.Duration, expected int64) (Task, error) {
	return s.renewTask(ctx, id, actorID, duration, expected, false)
}

// RenewTaskWithClaim is the bearer-token variant. RenewTask already checks
// ownership and lease expiry in SQL; this variant additionally reports a
// missing/expired lease as forbidden rather than a generic version conflict.
func (s *Store) RenewTaskWithClaim(ctx context.Context, id, actorID string, duration time.Duration, expected int64) (Task, error) {
	return s.renewTask(ctx, id, actorID, duration, expected, true)
}

func (s *Store) renewTask(ctx context.Context, id, actorID string, duration time.Duration, expected int64, requireActiveClaim bool) (Task, error) {
	var err error
	duration, err = NormalizeTaskClaimDuration(duration)
	if err != nil {
		return Task{}, err
	}
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	// The expiry modifier is fixed input; SQLite supplies the clock when the
	// guarded UPDATE executes after writer-lock acquisition.
	expiresModifier := fmt.Sprintf("+%.9f seconds", duration.Seconds())
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE tasks SET claim_expires_at=strftime('%Y-%m-%dT%H:%M:%fZ','now',?), version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND claimed_by=? AND julianday(claim_expires_at) > julianday('now') AND deleted_at IS NULL AND completed_at IS NULL`, expiresModifier, id, expected, actorID)
		if err != nil {
			return mapDependencyLifecycleError(ctx, tx, err, dependencyLifecycleTarget{TaskID: id})
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if current.Version == expected && current.CompletedAt != nil {
				return conflict("task is already finished", nil)
			}
			if requireActiveClaim {
				version, active, claimErr := taskClaimStateTx(ctx, tx, id, actorID)
				if claimErr != nil {
					return claimErr
				}
				if version == expected && !active {
					return forbidden("an active claim owned by this actor is required")
				}
			}
			return conflict("claim cannot be renewed", map[string]any{"current": current})
		}
		var expires string
		if err := tx.QueryRowContext(ctx, `SELECT claim_expires_at FROM tasks WHERE id=?`, id).Scan(&expires); err != nil {
			return err
		}
		_, err = insertEvent(ctx, tx, "task.claim_renewed", actorID, current.ProjectID, id, map[string]any{"expires_at": expires})
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

func (s *Store) ReleaseTask(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.releaseTask(ctx, id, actorID, expected, false)
}

// ReleaseTaskWithClaim requires the caller to own an unexpired lease. Human
// callers use ReleaseTask so the UI can clean up an expired lease as well.
func (s *Store) ReleaseTaskWithClaim(ctx context.Context, id, actorID string, expected int64) (Task, error) {
	return s.releaseTask(ctx, id, actorID, expected, true)
}

func (s *Store) releaseTask(ctx context.Context, id, actorID string, expected int64, requireActiveClaim bool) (Task, error) {
	current, err := s.GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		query := `UPDATE tasks SET claimed_by=NULL, claim_expires_at=NULL, version=version+1, updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND version=? AND claimed_by=? AND deleted_at IS NULL`
		args := []any{id, expected, actorID}
		if requireActiveClaim {
			query += ` AND claim_expires_at IS NOT NULL AND julianday(claim_expires_at) > julianday('now')`
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			if requireActiveClaim {
				version, active, claimErr := taskClaimStateTx(ctx, tx, id, actorID)
				if claimErr != nil {
					return claimErr
				}
				if version == expected && !active {
					return forbidden("an active claim owned by this actor is required")
				}
			}
			return conflict("claim cannot be released", map[string]any{"current": current})
		}
		_, err = insertEvent(ctx, tx, "task.released", actorID, current.ProjectID, id, nil)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}
