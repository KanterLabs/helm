package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// StartGithubAudit captures the complete bounded task scope under the same
// writer transaction as run/job creation and idempotency ownership.
func (s *Store) StartGithubAudit(ctx context.Context, projectID, actorID string, input GithubAuditStartInput) (AuditRun, GithubAuditJob, error) {
	var id string
	if input.RequestKey == "" || len(input.RequestKey) > 200 || len(input.TaskIDs) > 1000 || len(input.States) > 5 || len(input.Repositories) > 10 {
		return AuditRun{}, GithubAuditJob{}, invalid("invalid audit request limits or idempotency key", nil)
	}
	if len(input.TaskIDs) > 0 && len(input.States) > 0 {
		return AuditRun{}, GithubAuditJob{}, invalid("task_ids and states are alternative scopes", nil)
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, projectID); err != nil {
			return err
		}
		ownerID, err := githubAuditStartIdentityTx(ctx, tx, projectID, actorID, input)
		if err != nil {
			return err
		}
		input.AccountOwnerID = ownerID
		var fingerprint, priorToken, priorAuthorization string
		err = tx.QueryRowContext(ctx, `SELECT audit_id,fingerprint,request_token_id,authorization_id FROM github_audit_jobs WHERE actor_id=? AND project_id=? AND request_key=?`, actorID, projectID, input.RequestKey).Scan(&id, &fingerprint, &priorToken, &priorAuthorization)
		if err == nil {
			if fingerprint != input.Fingerprint || priorToken != input.RequestTokenID || priorAuthorization != input.AuthorizationID {
				return conflict("idempotency key was used for a different audit request", nil)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT audit_id FROM github_audit_jobs WHERE project_id=? AND (actor_id=? OR COALESCE(NULLIF(account_owner_id,''),actor_id)=?) AND outcome=''`, projectID, actorID, ownerID).Scan(&existing)
		if err == nil {
			return conflict("an audit is already active", map[string]any{"audit_id": existing})
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var queued int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_audit_jobs WHERE outcome=''`).Scan(&queued); err != nil {
			return err
		}
		if queued >= 100 {
			return conflict("audit queue is full", nil)
		}
		var retained int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_runs WHERE project_id=?`, projectID).Scan(&retained); err != nil {
			return err
		}
		if retained >= maxAuditRunsPerProject {
			return invalid("project has reached the retained audit run limit", map[string]any{"limit": maxAuditRunsPerProject})
		}
		selected, err := githubSelection(ctx, tx, projectID)
		if err != nil {
			return err
		}
		if selected.Version != input.SelectionVersion {
			return conflict("repository selection changed", nil)
		}
		if len(input.Repositories) == 0 {
			return invalid("select at least one repository", nil)
		}
		for _, repo := range input.Repositories {
			found := false
			for _, allowed := range selected.Repositories {
				if repo == allowed {
					found = true
				}
			}
			if !found {
				return invalid("repository/ref is not selected for this project", nil)
			}
		}
		capture, err := captureGithubAuditTasks(ctx, tx, projectID, input)
		if err != nil {
			return err
		}
		if len(capture.Tasks) == 0 {
			return invalid("audit scope contains no tasks", nil)
		}
		raw, err := json.Marshal(capture)
		if err != nil {
			return err
		}
		if len(raw) > 192*1024 {
			return invalid("task scope is too large; select fewer tasks", nil)
		}
		timestamp := now()
		id = newID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,created_at,updated_at) VALUES(?,?,?,'github','queued',?,?,?)`, id, projectID, actorID, timestamp, timestamp, timestamp); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_audit_jobs(audit_id,project_id,actor_id,request_key,fingerprint,selection_version,provider_generation,account_generation,model,effort,capture_json,deadline,task_count,retry_of,created_at,updated_at,account_owner_id,authorization_id,request_token_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, projectID, actorID, input.RequestKey, input.Fingerprint, input.SelectionVersion, input.ProviderGeneration, input.AccountGeneration, input.Model, input.Effort, string(raw), time.Now().UTC().Add(15*time.Minute).Format(time.RFC3339Nano), len(capture.Tasks), nullableGithubRetry(input.RetryOf), timestamp, timestamp, ownerID, input.AuthorizationID, input.RequestTokenID)
		return err
	})
	if err != nil {
		return AuditRun{}, GithubAuditJob{}, err
	}
	run, err := s.GetAuditRun(ctx, id)
	if err != nil {
		return run, GithubAuditJob{}, err
	}
	job, err := s.GetGithubAuditJob(ctx, id)
	return run, job, err
}
func nullableGithubRetry(id string) any {
	if id == "" {
		return nil
	}
	return id
}
func githubAuditActorTx(ctx context.Context, q dependencySQL, actorID string) error {
	var kind string
	var disabled sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT kind,disabled_at FROM actors WHERE id=?`, actorID).Scan(&kind, &disabled); err != nil {
		return err
	}
	if kind != "human" || disabled.Valid {
		return forbidden("audit requires an enabled human account")
	}
	return nil
}
