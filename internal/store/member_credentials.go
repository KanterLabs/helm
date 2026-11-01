package store

import (
	"context"
	"database/sql"
	"errors"
)

// SetMemberPassword changes the local credential and invalidates every active
// session for the member. Callers that are changing their own password should
// use SetMemberPasswordExcept with their current session ID so that the
// response remains authenticated.
func (s *Store) SetMemberPassword(ctx context.Context, actorID, passwordHash, changedBy string) error {
	return s.SetMemberPasswordExcept(ctx, actorID, passwordHash, changedBy, "")
}

func (s *Store) SetMemberPasswordExcept(ctx context.Context, actorID, passwordHash, changedBy, keepSessionID string, expectedHash ...string) error {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_setup SET completed_at=completed_at WHERE id=1`); err != nil {
			return err
		}
		if len(expectedHash) > 0 && expectedHash[0] != "" {
			if err := verifyCredentialSnapshot(ctx, tx, actorID, expectedHash[0]); err != nil {
				return err
			}
		}
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM actors WHERE id=?`, actorID).Scan(&kind); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("member not found")
			}
			return err
		}
		if kind != "human" {
			return invalid("only human actors can have local passwords", nil)
		}
		var role string
		if err := tx.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id=?`, actorID).Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("member not found")
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE actors SET password_hash=?, updated_at=? WHERE id=?`, passwordHash, now(), actorID); err != nil {
			return err
		}
		eventActor := changedBy
		if eventActor == "" {
			eventActor = actorID
		}
		if _, err := insertEvent(ctx, tx, "member.password_changed", eventActor, "", "", map[string]any{"actor_id": actorID}); err != nil {
			return err
		}
		return revokeActorSessions(ctx, tx, actorID, eventActor, "password_changed", keepSessionID)
	})
	return err
}

func (s *Store) DisableMember(ctx context.Context, actorID, changedBy string) (Member, error) {
	disabled := true
	return s.UpdateMember(ctx, actorID, MemberUpdate{Disabled: &disabled}, changedBy)
}

func (s *Store) ReactivateMember(ctx context.Context, actorID, changedBy string) (Member, error) {
	disabled := false
	return s.UpdateMember(ctx, actorID, MemberUpdate{Disabled: &disabled}, changedBy)
}

// LastSeenAtFromSessions is used by list APIs that need a stable, redacted
// activity summary without exposing session tokens or hashes.
func (s *Store) LastSeenAtFromSessions(ctx context.Context, actorID string) *string {
	var value sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(last_seen_at) FROM sessions WHERE actor_id=?`, actorID).Scan(&value); err != nil || !value.Valid {
		return nil
	}
	return &value.String
}

// verifyCredentialSnapshot runs only after the transaction has reserved the
// SQLite writer. Reset and disable cannot commit between this check and the
// credential/session write, including when they use another connection pool.
func verifyCredentialSnapshot(ctx context.Context, tx *sql.Tx, actorID, expectedHash string) error {
	var actual string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(a.password_hash,'') FROM actors a JOIN workspace_memberships wm ON wm.actor_id=a.id WHERE a.id=? AND a.kind='human' AND a.disabled_at IS NULL`, actorID).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actual != expectedHash) {
		return forbidden("invalid email or password")
	}
	return err
}
