package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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
func captureGithubAuditTasks(ctx context.Context, tx *sql.Tx, projectID string, input GithubAuditStartInput) (GithubAuditCapture, error) {
	capture := GithubAuditCapture{Request: input, Tasks: []GithubAuditTask{}, Omissions: []string{}}
	query := `SELECT t.id,p.key||'-'||t.number,t.title,t.description,t.version,t.column_id,c.semantic_state,c.name FROM tasks t JOIN projects p ON p.id=t.project_id JOIN columns c ON c.id=t.column_id WHERE t.project_id=? AND t.deleted_at IS NULL`
	args := []any{projectID}
	if len(input.TaskIDs) > 0 {
		parts := make([]string, len(input.TaskIDs))
		seen := map[string]bool{}
		for i, id := range input.TaskIDs {
			if seen[id] {
				return capture, invalid("duplicate task ID", nil)
			}
			seen[id] = true
			parts[i] = "?"
			args = append(args, id)
		}
		query += ` AND t.id IN (` + strings.Join(parts, ",") + `)`
	} else {
		states := input.States
		if len(states) == 0 {
			states = []string{"backlog", "ready", "active"}
		}
		parts := make([]string, len(states))
		for i, state := range states {
			if !validState(state) {
				return capture, invalid("invalid audit task state", nil)
			}
			parts[i] = "?"
			args = append(args, state)
		}
		query += ` AND c.semantic_state IN (` + strings.Join(parts, ",") + `)`
	}
	query += ` ORDER BY t.id LIMIT 1001`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return capture, err
	}
	for rows.Next() {
		var task GithubAuditTask
		if err = rows.Scan(&task.ID, &task.Key, &task.Title, &task.Description, &task.Version, &task.ColumnID, &task.SemanticState, &task.ColumnName); err != nil {
			rows.Close()
			return capture, err
		}
		task.Criteria = []string{}
		capture.Tasks = append(capture.Tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return capture, err
	}
	if len(capture.Tasks) > 1000 {
		capture.Tasks = capture.Tasks[:1000]
		capture.Omissions = append(capture.Omissions, "Task scope exceeded 1000 tasks; remaining tasks omitted.")
	}
	if len(input.TaskIDs) > 0 && len(capture.Tasks) != len(input.TaskIDs) {
		return capture, invalid("a requested task is outside this project or unavailable", nil)
	}
	for i := range capture.Tasks {
		rows, err = tx.QueryContext(ctx, `SELECT text FROM task_checklist_items WHERE task_id=? ORDER BY position,id`, capture.Tasks[i].ID)
		if err != nil {
			return capture, err
		}
		for rows.Next() {
			var criterion string
			if err = rows.Scan(&criterion); err != nil {
				rows.Close()
				return capture, err
			}
			capture.Tasks[i].Criteria = append(capture.Tasks[i].Criteria, criterion)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return capture, err
		}
	}
	return capture, nil
}
