package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// MemberUpdate is a complete set of optional membership changes. A nil
// ProjectIDs leaves the current project grants unchanged; a non-nil empty
// slice intentionally removes every project grant.
type MemberUpdate struct {
	Name        *string
	Description *string
	Role        *string
	ProjectIDs  *[]string
	Disabled    *bool
}

// CreateMember creates an explicitly admitted human. A blank password is
// valid for Cloudflare-only users; local-password users must provide a hash at
// the HTTP/auth boundary. Project references accept IDs, slugs, or keys.
func (s *Store) CreateMember(ctx context.Context, actor Actor, role string, projectRefs []string, passwordHash, createdBy string) (Member, error) {
	if actor.Kind == "" {
		actor.Kind = "human"
	}
	if actor.Kind != "human" {
		return Member{}, invalid("membership actors must be human", nil)
	}
	if err := validateActorName(strings.TrimSpace(actor.Name)); err != nil {
		return Member{}, err
	}
	if len([]rune(actor.Description)) > 10000 {
		return Member{}, invalid("description must be 10000 characters or fewer", nil)
	}
	role, err := normalizeWorkspaceRole(role)
	if err != nil {
		return Member{}, err
	}
	if actor.ID == "" {
		actor.ID = newID()
	}
	created := now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_setup SET completed_at=completed_at WHERE id=1`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO actors(id, kind, name, email, password_hash, admin, disabled_at, created_at, updated_at, description) VALUES (?, 'human', ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?)`, actor.ID, strings.TrimSpace(actor.Name), strings.TrimSpace(actor.EmailValue()), passwordHash, boolInt(IsWorkspaceAdminRole(role)), actor.DisabledValue(), created, created, strings.TrimSpace(actor.Description)); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "actor email already exists"}
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memberships(actor_id, role, created_at, updated_at) VALUES (?, ?, ?, ?)`, actor.ID, role, created, created); err != nil {
			return err
		}
		if err := attachProjectMemberships(ctx, tx, actor.ID, projectRefs, created); err != nil {
			return err
		}
		payload := map[string]any{"actor_id": actor.ID, "role": role, "project_count": len(uniqueStrings(projectRefs))}
		eventActor := createdBy
		if eventActor == "" {
			eventActor = actor.ID
		}
		_, err := insertEvent(ctx, tx, "member.created", eventActor, "", "", payload)
		return err
	})
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, actor.ID)
}

// createHumanActor preserves the historical CreateActor helper while making
// all newly-created humans explicit members. The public membership endpoint
// uses CreateMember so it can choose a reviewed role and project ceiling.
func (s *Store) createHumanActor(ctx context.Context, actor Actor, passwordHash string, admin bool, projectRefs []string, createdBy string) (Actor, error) {
	role := WorkspaceRoleMember
	if admin {
		role = WorkspaceRoleOwner
	}
	member, err := s.CreateMember(ctx, actor, role, projectRefs, passwordHash, createdBy)
	if err != nil {
		return Actor{}, err
	}
	return member.Actor, nil
}

// createCloudflarePending creates a verified-but-unadmitted human. The first
// configured admin on an otherwise empty workspace is the sole bootstrap
// exception; after that, Cloudflare assertions cannot grant membership.
func (s *Store) createCloudflarePending(ctx context.Context, actor Actor, adminEmail string) (Actor, error) {
	if err := validateActorName(strings.TrimSpace(actor.Name)); err != nil {
		return Actor{}, err
	}
	if strings.TrimSpace(actor.EmailValue()) == "" {
		return Actor{}, invalid("email is required", nil)
	}
	if actor.ID == "" {
		actor.ID = newID()
	}
	created := now()
	bootstrap := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_setup SET completed_at=completed_at WHERE id=1`); err != nil {
			return err
		}
		var humans int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM workspace_memberships WHERE actor_id <> 'actor-disabled-mode') + (SELECT COUNT(*) FROM auth_setup)`).Scan(&humans); err != nil {
			return err
		}
		bootstrap = humans == 0 && strings.TrimSpace(adminEmail) != "" && strings.EqualFold(strings.TrimSpace(actor.EmailValue()), strings.TrimSpace(adminEmail))
		if _, err := tx.ExecContext(ctx, `INSERT INTO actors(id, kind, name, email, password_hash, admin, created_at, updated_at, description) VALUES (?, 'human', ?, NULLIF(?, ''), NULL, ?, ?, ?, ?)`, actor.ID, strings.TrimSpace(actor.Name), strings.TrimSpace(actor.EmailValue()), boolInt(bootstrap), created, created, strings.TrimSpace(actor.Description)); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &Error{Kind: ErrAlreadyExists, Message: "actor email already exists"}
			}
			return err
		}
		if bootstrap {
			if _, err := tx.ExecContext(ctx, `INSERT INTO auth_setup(id,completed_at) VALUES (1,?)`, created); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memberships(actor_id, role, created_at, updated_at) VALUES (?, 'owner', ?, ?)`, actor.ID, created, created); err != nil {
				return err
			}
		}
		payload := map[string]any{"actor_id": actor.ID, "source": "cloudflare", "role": ""}
		if bootstrap {
			payload["role"] = WorkspaceRoleOwner
		}
		_, err := insertEvent(ctx, tx, "member.created", actor.ID, "", "", payload)
		return err
	})
	if err != nil {
		return Actor{}, err
	}
	return s.GetActor(ctx, actor.ID)
}

func normalizeWorkspaceRole(role string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		role = WorkspaceRoleMember
	}
	switch role {
	case WorkspaceRoleOwner, WorkspaceRoleAdmin, WorkspaceRoleMember:
		return role, nil
	default:
		return "", invalid("role must be owner, admin, or member", nil)
	}
}

func attachProjectMemberships(ctx context.Context, tx *sql.Tx, actorID string, references []string, timestamp string) error {
	seenProjects := make(map[string]struct{}, len(references))
	for _, reference := range uniqueStrings(references) {
		var projectID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=? OR slug=? OR key=? LIMIT 1`, reference, strings.ToLower(reference), strings.ToUpper(reference)).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalid("member project not found: "+reference, nil)
			}
			return err
		}
		if _, exists := seenProjects[projectID]; exists {
			continue
		}
		seenProjects[projectID] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_memberships(actor_id, project_id, created_at, updated_at) VALUES (?, ?, ?, ?)`, actorID, projectID, timestamp, timestamp); err != nil {
			return err
		}
	}
	return nil
}

// enrichWorkspaceMembership projects reviewed membership onto the API actor
// shape. The legacy Admin bit is deliberately overwritten for humans so a
// stale Cloudflare-era bit cannot silently widen authorization.
func (s *Store) enrichWorkspaceMembership(ctx context.Context, actor *Actor) error {
	actor.ProjectIDs = make([]string, 0)
	var role string
	err := s.DB.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id=?`, actor.ID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		if actor.Kind == "human" {
			actor.Admin = false
			actor.WorkspaceRole = ""
		}
		return nil
	}
	if err != nil {
		return err
	}
	actor.WorkspaceRole = role
	actor.Admin = IsWorkspaceAdminRole(role)
	rows, err := s.DB.QueryContext(ctx, `SELECT project_id FROM project_memberships WHERE actor_id=? ORDER BY project_id`, actor.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := make(map[string]struct{}, len(actor.ProjectIDs))
	for _, projectID := range actor.ProjectIDs {
		seen[projectID] = struct{}{}
	}
	for rows.Next() {
		var projectID string
		if err := rows.Scan(&projectID); err != nil {
			return err
		}
		if _, exists := seen[projectID]; !exists {
			actor.ProjectIDs = append(actor.ProjectIDs, projectID)
			seen[projectID] = struct{}{}
		}
	}
	return rows.Err()
}

func (s *Store) GetMember(ctx context.Context, actorID string) (Member, error) {
	actor, err := s.GetActor(ctx, actorID)
	if err != nil {
		return Member{}, err
	}
	if actor.Kind != "human" {
		return Member{}, notFound("human member not found")
	}
	member := Member{Actor: actor}
	var lastSeen sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT MAX(last_seen_at) FROM sessions WHERE actor_id=?`, actorID).Scan(&lastSeen); err != nil {
		return Member{}, err
	}
	member.LastSeenAt = nullableString(lastSeen)
	return member, nil
}

func (s *Store) ListMembers(ctx context.Context, limit, offset int, includeDisabled bool) ([]Member, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	actors, more, err := s.ListActorsPage(ctx, "human", limit, offset, includeDisabled)
	if err != nil {
		return nil, false, err
	}
	result := make([]Member, 0, len(actors))
	for _, actor := range actors {
		member := Member{Actor: actor}
		var lastSeen sql.NullString
		if err := s.DB.QueryRowContext(ctx, `SELECT MAX(last_seen_at) FROM sessions WHERE actor_id=?`, actor.ID).Scan(&lastSeen); err != nil {
			return nil, false, err
		}
		member.LastSeenAt = nullableString(lastSeen)
		result = append(result, member)
	}
	return result, more, nil
}

// UpdateMember applies role, project, profile, and disabled-state changes in a
// single transaction. A no-op write acquires SQLite's writer lock
// before counting active administrators, so two concurrent demotions cannot
// both remove the last active admin.
func (s *Store) UpdateMember(ctx context.Context, actorID string, input MemberUpdate, updatedBy string) (Member, error) {
	if input.Name != nil {
		if err := validateActorName(strings.TrimSpace(*input.Name)); err != nil {
			return Member{}, err
		}
	}
	if input.Description != nil && len([]rune(*input.Description)) > 10000 {
		return Member{}, invalid("description must be 10000 characters or fewer", nil)
	}
	var role string
	if input.Role != nil {
		var err error
		role, err = normalizeWorkspaceRole(*input.Role)
		if err != nil {
			return Member{}, err
		}
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE auth_setup SET completed_at=completed_at WHERE id=1`); err != nil {
			return err
		}
		var kind string
		var currentRole sql.NullString
		var currentDisabled sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT kind, disabled_at FROM actors WHERE id=?`, actorID).Scan(&kind, &currentDisabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("member not found")
			}
			return err
		}
		if kind != "human" {
			return invalid("only human actors can be members", nil)
		}
		if err := tx.QueryRowContext(ctx, `SELECT role FROM workspace_memberships WHERE actor_id=?`, actorID).Scan(&currentRole); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if role == "" {
			if currentRole.Valid {
				role = currentRole.String
			} else {
				role = WorkspaceRoleMember
			}
		}
		oldAdmin := currentRole.Valid && IsWorkspaceAdminRole(currentRole.String) && !currentDisabled.Valid
		newDisabled := currentDisabled.Valid
		if input.Disabled != nil {
			newDisabled = *input.Disabled
		}
		newAdmin := IsWorkspaceAdminRole(role) && !newDisabled
		if oldAdmin && !newAdmin {
			var others int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM workspace_memberships wm JOIN actors a ON a.id=wm.actor_id WHERE a.kind='human' AND a.disabled_at IS NULL AND wm.actor_id<>? AND wm.role IN ('owner','admin')`, actorID).Scan(&others); err != nil {
				return err
			}
			if others == 0 {
				return &Error{Kind: ErrConflict, Message: "cannot disable or demote the last active administrator"}
			}
		}
		stamp := now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_memberships(actor_id, role, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(actor_id) DO UPDATE SET role=excluded.role, updated_at=excluded.updated_at`, actorID, role, stamp, stamp); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE actors SET admin=?, disabled_at=CASE WHEN ? THEN COALESCE(disabled_at, ?) ELSE NULL END, name=COALESCE(?, name), description=COALESCE(?, description), updated_at=? WHERE id=?`, boolInt(IsWorkspaceAdminRole(role)), boolInt(newDisabled), stamp, nullableInput(input.Name), nullableInput(input.Description), stamp, actorID); err != nil {
			return err
		}
		if newDisabled {
			if err := revokeActorSessions(ctx, tx, actorID, updatedBy, "disabled", ""); err != nil {
				return err
			}
		}
		if input.ProjectIDs != nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM project_memberships WHERE actor_id=?`, actorID); err != nil {
				return err
			}
			if err := attachProjectMemberships(ctx, tx, actorID, *input.ProjectIDs, stamp); err != nil {
				return err
			}
		}
		payload := map[string]any{"actor_id": actorID, "role": role, "disabled": newDisabled}
		eventActor := updatedBy
		if eventActor == "" {
			eventActor = actorID
		}
		_, err := insertEvent(ctx, tx, "member.updated", eventActor, "", "", payload)
		return err
	})
	if err != nil {
		return Member{}, err
	}
	return s.GetMember(ctx, actorID)
}

func nullableInput(value *string) any {
	if value == nil {
		return nil
	}
	return strings.TrimSpace(*value)
}
