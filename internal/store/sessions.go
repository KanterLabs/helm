package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) CreateSession(ctx context.Context, actorID, raw string, expires time.Time) error {
	_, err := s.CreateSessionWithMetadata(ctx, actorID, raw, expires, "", "")
	return err
}

// CreateSessionWithMetadata stores only a hash of the cookie value. The
// metadata is bounded and is never copied into the audit payload.
func (s *Store) CreateSessionWithMetadata(ctx context.Context, actorID, raw string, expires time.Time, ipAddress, userAgent string, expectedHash ...string) (Session, error) {
	if strings.TrimSpace(raw) == "" {
		return Session{}, invalid("session secret is required", nil)
	}
	if len(ipAddress) > 128 {
		ipAddress = ipAddress[:128]
	}
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	id := newID()
	created := now()
	expiresAt := expires.UTC().Format(time.RFC3339Nano)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if len(expectedHash) > 0 && expectedHash[0] != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE auth_setup SET completed_at=completed_at WHERE id=1`); err != nil {
				return err
			}
			if err := verifyCredentialSnapshot(ctx, tx, actorID, expectedHash[0]); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id, actor_id, token_hash, expires_at, created_at, last_seen_at, ip_address, user_agent) VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`, id, actorID, HashSecret(raw), expiresAt, created, created, strings.TrimSpace(ipAddress), userAgent); err != nil {
			return err
		}
		// Do not include the secret, hash, or user-agent/IP in events. A session
		// ID is an opaque non-secret handle suitable for audit correlation.
		_, err := insertEvent(ctx, tx, "session.created", actorID, "", "", map[string]any{"session_id": id, "expires_at": expiresAt})
		return err
	})
	if err != nil {
		return Session{}, err
	}
	return Session{ID: id, ActorID: actorID, CreatedAt: created, LastSeenAt: created, ExpiresAt: expiresAt, IPAddress: ipAddress, UserAgent: userAgent}, nil
}

func (s *Store) SessionForToken(ctx context.Context, raw string) (Actor, Session, error) {
	var actorID, sessionID string
	var lastSeen sql.NullString
	var createdAt, expiresAt string
	var revokedAt, ipAddress, userAgent sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, actor_id, created_at, last_seen_at, expires_at, revoked_at, ip_address, user_agent FROM sessions WHERE token_hash = ? AND expires_at > ? AND revoked_at IS NULL`, HashSecret(raw), now()).Scan(&sessionID, &actorID, &createdAt, &lastSeen, &expiresAt, &revokedAt, &ipAddress, &userAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, Session{}, ErrNotFound
	}
	if err != nil {
		return Actor{}, Session{}, err
	}
	lastSeenCheck := time.Now().UTC()
	if authTimestampNeedsRefresh(lastSeen, lastSeenCheck) {
		seenAt := lastSeenCheck.Format(time.RFC3339Nano)
		if lastSeen.Valid {
			_, _ = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ? AND last_seen_at = ? AND revoked_at IS NULL`, seenAt, HashSecret(raw), lastSeen.String)
		} else {
			_, _ = s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ? AND last_seen_at IS NULL AND revoked_at IS NULL`, seenAt, HashSecret(raw))
		}
		lastSeen.String, lastSeen.Valid = seenAt, true
	}
	actor, err := s.GetActor(ctx, actorID)
	if err != nil {
		return Actor{}, Session{}, err
	}
	return actor, Session{ID: sessionID, ActorID: actorID, CreatedAt: createdAt, LastSeenAt: lastSeen.String, ExpiresAt: expiresAt, RevokedAt: nullableString(revokedAt), IPAddress: ipAddress.String, UserAgent: userAgent.String}, nil
}

func (s *Store) ActorForSession(ctx context.Context, raw string) (Actor, error) {
	actor, _, err := s.SessionForToken(ctx, raw)
	return actor, err
}

func (s *Store) DeleteSession(ctx context.Context, raw string) error {
	return s.revokeSessionByToken(ctx, raw)
}

func (s *Store) revokeSessionByToken(ctx context.Context, raw string) error {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var actorID, sessionID string
		if err := tx.QueryRowContext(ctx, `SELECT id, actor_id FROM sessions WHERE token_hash=? AND revoked_at IS NULL`, HashSecret(raw)).Scan(&sessionID, &actorID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=?, expires_at='1970-01-01T00:00:00Z' WHERE id=? AND revoked_at IS NULL`, now(), sessionID); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, "session.revoked", actorID, "", "", map[string]any{"session_id": sessionID, "reason": "logout"})
		return err
	})
	return err
}

func (s *Store) ListSessions(ctx context.Context, actorID string, includeExpired bool) ([]Session, error) {
	query := `SELECT id, actor_id, created_at, last_seen_at, expires_at, revoked_at, ip_address, user_agent FROM sessions WHERE actor_id=?`
	if !includeExpired {
		query += ` AND expires_at > ?`
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT 200`
	args := []any{actorID}
	if !includeExpired {
		args = append(args, now())
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Session, 0)
	for rows.Next() {
		var item Session
		var revoked, ipAddress, userAgent sql.NullString
		if err := rows.Scan(&item.ID, &item.ActorID, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &revoked, &ipAddress, &userAgent); err != nil {
			return nil, err
		}
		item.RevokedAt, item.IPAddress, item.UserAgent = nullableString(revoked), ipAddress.String, userAgent.String
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, actorID, sessionID, revokedBy string) error {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if actorID == "" || sessionID == "" {
			return invalid("session and actor are required", nil)
		}
		if revokedBy != actorID {
			var role sql.NullString
			if err := tx.QueryRowContext(ctx, `SELECT wm.role FROM workspace_memberships wm JOIN actors a ON a.id=wm.actor_id WHERE wm.actor_id=? AND a.kind='human' AND a.disabled_at IS NULL`, revokedBy).Scan(&role); err != nil || !role.Valid || !IsWorkspaceAdminRole(role.String) {
				return &Error{Kind: ErrForbidden, Message: "session access is not allowed"}
			}
		}
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT actor_id FROM sessions WHERE id=?`, sessionID).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("session not found")
			}
			return err
		}
		if owner != actorID {
			return notFound("session not found")
		}
		result, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=?, expires_at='1970-01-01T00:00:00Z' WHERE id=? AND revoked_at IS NULL`, now(), sessionID)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 0 {
			return nil
		}
		eventActor := revokedBy
		if eventActor == "" {
			eventActor = actorID
		}
		_, err = insertEvent(ctx, tx, "session.revoked", eventActor, "", "", map[string]any{"session_id": sessionID, "actor_id": actorID, "reason": "operator"})
		return err
	})
	return err
}

// Expiry is set as well as revoked_at because retained schema35/40 readers
// authenticate using expires_at alone. Keep non-secret handles for audit.
func revokeActorSessions(ctx context.Context, tx *sql.Tx, actorID, changedBy, reason, keepID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM sessions WHERE actor_id=? AND revoked_at IS NULL AND id<>?`, actorID, keepID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if changedBy == "" {
		changedBy = actorID
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET revoked_at=?,expires_at='1970-01-01T00:00:00Z' WHERE id=? AND revoked_at IS NULL`, now(), id); err != nil {
			return err
		}
		if _, err := insertEvent(ctx, tx, "session.revoked", changedBy, "", "", map[string]any{"actor_id": actorID, "session_id": id, "reason": reason}); err != nil {
			return err
		}
	}
	return nil
}
