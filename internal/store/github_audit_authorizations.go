package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// GithubAuditAuthorization is a local, bounded permission to spend one human
// account on an agent's exact token and selected project repositories.
type GithubAuditAuthorization struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"project_id"`
	AgentID          string  `json:"agent_id"`
	TokenID          string  `json:"token_id"`
	AccountOwnerID   string  `json:"account_owner_id"`
	SelectionVersion int64   `json:"selection_version"`
	MaxTasks         int     `json:"max_tasks"`
	CreatedAt        string  `json:"created_at"`
	ExpiresAt        string  `json:"expires_at"`
	RevokedAt        *string `json:"revoked_at,omitempty"`
}

type CreateGithubAuditAuthorizationInput struct {
	AgentID   string `json:"agent_id"`
	TokenID   string `json:"token_id"`
	ExpiresAt string `json:"expires_at"`
	MaxTasks  int    `json:"max_tasks"`
}

type GithubAuditAuthorizationInput = CreateGithubAuditAuthorizationInput

const githubAuditAuthorizationColumns = `id,project_id,agent_id,token_id,account_owner_id,selection_version,max_tasks,created_at,expires_at,revoked_at`

func scanGithubAuditAuthorization(row interface{ Scan(...any) error }) (GithubAuditAuthorization, error) {
	var a GithubAuditAuthorization
	var revoked sql.NullString
	err := row.Scan(&a.ID, &a.ProjectID, &a.AgentID, &a.TokenID, &a.AccountOwnerID, &a.SelectionVersion, &a.MaxTasks, &a.CreatedAt, &a.ExpiresAt, &revoked)
	a.RevokedAt = nullableString(revoked)
	if errors.Is(err, sql.ErrNoRows) {
		err = notFound("GitHub audit authorization not found")
	}
	return a, err
}

func (s *Store) GetGithubAuditAuthorization(ctx context.Context, id string) (GithubAuditAuthorization, error) {
	return scanGithubAuditAuthorization(s.DB.QueryRowContext(ctx, `SELECT `+githubAuditAuthorizationColumns+` FROM github_audit_authorizations WHERE id=?`, id))
}
func (s *Store) ListGithubAuditAuthorizations(ctx context.Context, projectID string) ([]GithubAuditAuthorization, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+githubAuditAuthorizationColumns+` FROM github_audit_authorizations WHERE project_id=? ORDER BY created_at DESC,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []GithubAuditAuthorization{}
	for rows.Next() {
		a, err := scanGithubAuditAuthorization(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

func (s *Store) CreateGithubAuditAuthorization(ctx context.Context, projectID, ownerID string, input CreateGithubAuditAuthorizationInput) (GithubAuditAuthorization, error) {
	expiry, err := time.Parse(time.RFC3339, input.ExpiresAt)
	at := time.Now().UTC()
	if err != nil || !expiry.After(at) || expiry.After(at.Add(24*time.Hour)) {
		return GithubAuditAuthorization{}, invalid("authorization expiry must be RFC3339 and within the next 24 hours", nil)
	}
	if input.MaxTasks == 0 {
		input.MaxTasks = 10
	}
	if input.MaxTasks < 1 || input.MaxTasks > 100 {
		return GithubAuditAuthorization{}, invalid("max_tasks must be between 1 and 100", nil)
	}
	a := GithubAuditAuthorization{ID: newID(), ProjectID: projectID, AgentID: input.AgentID, TokenID: input.TokenID, AccountOwnerID: ownerID, MaxTasks: input.MaxTasks, CreatedAt: now(), ExpiresAt: expiry.UTC().Format(time.RFC3339Nano)}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, projectID); err != nil {
			return err
		}
		if err := githubAuditAdminTx(ctx, tx, ownerID); err != nil {
			return err
		}
		if err := githubAuditAgentTokenTx(ctx, tx, projectID, a.AgentID, a.TokenID); err != nil {
			return err
		}
		selection, err := githubSelection(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if len(selection.Repositories) == 0 {
			return invalid("select at least one repository", nil)
		}
		a.SelectionVersion = selection.Version
		_, err = tx.ExecContext(ctx, `INSERT INTO github_audit_authorizations(`+githubAuditAuthorizationColumns+`) VALUES(?,?,?,?,?,?,?,?,?,NULL)`, a.ID, a.ProjectID, a.AgentID, a.TokenID, a.AccountOwnerID, a.SelectionVersion, a.MaxTasks, a.CreatedAt, a.ExpiresAt)
		return err
	})
	return a, err
}

func githubAuditAdminTx(ctx context.Context, q dependencySQL, ownerID string) error {
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM actors WHERE id=? AND kind='human' AND admin=1 AND disabled_at IS NULL`, ownerID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return forbidden("authorization requires an enabled human administrator")
	}
	return nil
}

// Evaluate both independent project ceilings. Empty lists mean unrestricted,
// matching LookupToken, while a nonempty actor ceiling can never be expanded by
// a token. Never read token hashes or bearer secrets into permission records.
func githubAuditAgentTokenTx(ctx context.Context, q dependencySQL, projectID, agentID, tokenID string) error {
	var kind, scopesRaw, projectsRaw string
	var disabled, expiry sql.NullString
	err := q.QueryRowContext(ctx, `SELECT a.kind,a.disabled_at,t.scopes,t.project_ids,t.expires_at FROM actors a JOIN tokens t ON t.actor_id=a.id WHERE a.id=? AND t.id=?`, agentID, tokenID).Scan(&kind, &disabled, &scopesRaw, &projectsRaw, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return forbidden("authorization agent or token is unavailable")
	}
	if err != nil {
		return err
	}
	if kind != "agent" || disabled.Valid {
		return forbidden("authorization requires an enabled agent")
	}
	if expiry.Valid {
		at, err := time.Parse(time.RFC3339Nano, expiry.String)
		if err != nil || !at.After(time.Now().UTC()) {
			return forbidden("authorization token expired")
		}
	}
	var scopes, projects []string
	if json.Unmarshal([]byte(scopesRaw), &scopes) != nil || json.Unmarshal([]byte(projectsRaw), &projects) != nil {
		return forbidden("authorization token is invalid")
	}
	hasRead := false
	for _, scope := range scopes {
		if scope == "tasks:read" {
			hasRead = true
		}
	}
	if !hasRead {
		return forbidden("authorization token requires tasks:read")
	}
	if len(projects) > 0 {
		found := false
		for _, id := range projects {
			if id == projectID {
				found = true
			}
		}
		if !found {
			return forbidden("authorization token cannot access project")
		}
	}
	var actorProjects, matching int
	if err = q.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN project_id=? THEN 1 ELSE 0 END),0) FROM actor_projects WHERE actor_id=?`, projectID, agentID).Scan(&actorProjects, &matching); err != nil {
		return err
	}
	if actorProjects > 0 && matching == 0 {
		return forbidden("authorization agent cannot access project")
	}
	return nil
}

func validateGithubAuditAuthorizationTx(ctx context.Context, q dependencySQL, id, projectID, agentID, tokenID string) (GithubAuditAuthorization, error) {
	a, err := scanGithubAuditAuthorization(q.QueryRowContext(ctx, `SELECT `+githubAuditAuthorizationColumns+` FROM github_audit_authorizations WHERE id=?`, id))
	if err != nil {
		return a, err
	}
	expiry, parseErr := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if a.ProjectID != projectID || a.AgentID != agentID || a.TokenID != tokenID || a.RevokedAt != nil || parseErr != nil || !expiry.After(time.Now().UTC()) {
		return a, forbidden("GitHub audit authorization is unavailable or does not match request")
	}
	if err = githubAuditAdminTx(ctx, q, a.AccountOwnerID); err != nil {
		return a, err
	}
	if err = githubAuditAgentTokenTx(ctx, q, projectID, agentID, tokenID); err != nil {
		return a, err
	}
	selection, err := githubSelection(ctx, q, projectID)
	if err != nil {
		return a, err
	}
	if selection.Version != a.SelectionVersion || len(selection.Repositories) == 0 {
		return a, conflict("authorization repository selection changed", nil)
	}
	return a, nil
}

func (s *Store) ValidateGithubAuditAuthorization(ctx context.Context, id, projectID, agentID, tokenID string) (GithubAuditAuthorization, error) {
	return validateGithubAuditAuthorizationTx(ctx, s.DB, id, projectID, agentID, tokenID)
}

func (s *Store) RevokeGithubAuditAuthorization(ctx context.Context, id, ownerID string) (GithubAuditAuthorization, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE github_audit_authorizations SET id=id WHERE id=?`, id); err != nil {
			return err
		}
		a, err := scanGithubAuditAuthorization(tx.QueryRowContext(ctx, `SELECT `+githubAuditAuthorizationColumns+` FROM github_audit_authorizations WHERE id=?`, id))
		if err != nil {
			return err
		}
		if err = githubAuditAdminTx(ctx, tx, ownerID); err != nil {
			return err
		}
		if a.AccountOwnerID != ownerID {
			return forbidden("authorization belongs to another account owner")
		}
		timestamp := now()
		if _, err = tx.ExecContext(ctx, `UPDATE github_audit_authorizations SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, timestamp, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE github_audit_jobs SET outcome='cancelled',stage='finished',fence=fence+1,lease_owner='',lease_until='',updated_at=?,finished_at=?,message='Audit authorization changed',error_code='authorization_changed' WHERE authorization_id=? AND outcome=''`, timestamp, timestamp, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE audit_runs SET status=CASE WHEN EXISTS(SELECT 1 FROM audit_findings WHERE audit_id=audit_runs.id) THEN 'partial' ELSE 'failed' END,updated_at=?,finalized_at=? WHERE status IN ('queued','running') AND id IN(SELECT audit_id FROM github_audit_jobs WHERE authorization_id=? AND outcome='cancelled')`, timestamp, timestamp, id)
		return err
	})
	if err != nil {
		return GithubAuditAuthorization{}, err
	}
	return s.GetGithubAuditAuthorization(ctx, id)
}

func githubAuditStartIdentityTx(ctx context.Context, tx *sql.Tx, projectID, actorID string, input GithubAuditStartInput) (string, error) {
	if input.AuthorizationID == "" {
		if err := githubAuditActorTx(ctx, tx, actorID); err != nil {
			return "", err
		}
		if input.AccountOwnerID != "" && input.AccountOwnerID != actorID {
			return "", forbidden("audit account owner does not match requester")
		}
		return actorID, nil
	}
	a, err := validateGithubAuditAuthorizationTx(ctx, tx, input.AuthorizationID, projectID, actorID, input.RequestTokenID)
	if err != nil {
		return "", err
	}
	if input.AccountOwnerID != "" && input.AccountOwnerID != a.AccountOwnerID {
		return "", forbidden("audit account owner does not match authorization")
	}
	if len(input.TaskIDs) == 0 || len(input.TaskIDs) > a.MaxTasks || len(input.States) > 0 {
		return "", invalid("authorized agent audit requires explicit task_ids within max_tasks", nil)
	}
	if input.SelectionVersion != a.SelectionVersion {
		return "", conflict("authorization repository selection changed", nil)
	}
	return a.AccountOwnerID, nil
}
