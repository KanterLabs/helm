package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const githubAuditJobColumns = `audit_id,project_id,actor_id,stage,outcome,created_at,updated_at,finished_at,task_count,captured_files,omitted_files,message,error_code,COALESCE(retry_of,''),fence,lease_owner,selection_version,provider_generation,account_generation,model,effort,COALESCE(NULLIF(account_owner_id,''),actor_id),authorization_id,request_token_id`

func scanGithubAuditJob(row interface{ Scan(...any) error }) (GithubAuditJob, error) {
	var j GithubAuditJob
	var finished sql.NullString
	err := row.Scan(&j.AuditID, &j.ProjectID, &j.ActorID, &j.Stage, &j.Outcome, &j.CreatedAt, &j.UpdatedAt, &finished, &j.TaskCount, &j.CapturedFiles, &j.OmittedFiles, &j.Message, &j.ErrorCode, &j.RetryOf, &j.Fence, &j.LeaseOwner, &j.SelectionVersion, &j.ProviderGeneration, &j.AccountGeneration, &j.Model, &j.Effort, &j.AccountOwnerID, &j.AuthorizationID, &j.RequestTokenID)
	j.FinishedAt = nullableString(finished)
	if errors.Is(err, sql.ErrNoRows) {
		err = notFound("server audit job not found")
	}
	return j, err
}
func (s *Store) GetGithubAuditJob(ctx context.Context, id string) (GithubAuditJob, error) {
	return scanGithubAuditJob(s.DB.QueryRowContext(ctx, `SELECT `+githubAuditJobColumns+` FROM github_audit_jobs WHERE audit_id=?`, id))
}
func (s *Store) GetGithubAuditCapture(ctx context.Context, id string) (GithubAuditCapture, error) {
	var raw string
	var c GithubAuditCapture
	err := s.DB.QueryRowContext(ctx, `SELECT capture_json FROM github_audit_jobs WHERE audit_id=?`, id).Scan(&raw)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c)
	return c, err
}
func (s *Store) GetGithubAuditEvidence(ctx context.Context, id string) (GithubAuditEvidence, error) {
	var raw sql.NullString
	var e GithubAuditEvidence
	err := s.DB.QueryRowContext(ctx, `SELECT evidence_json FROM github_audit_jobs WHERE audit_id=? UNION ALL SELECT evidence_json FROM github_audit_retained_evidence WHERE audit_id=? LIMIT 1`, id, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return e, notFound("server audit job not found")
	}
	if err != nil {
		return e, err
	}
	if !raw.Valid {
		return e, conflict("audit evidence is not captured yet", nil)
	}
	err = json.Unmarshal([]byte(raw.String), &e)
	return e, err
}

// ClaimGithubAudit uses a writer transaction and a monotonically increasing
// fence. Expired in-progress attempts are never returned for inference retry.
func (s *Store) ClaimGithubAudit(ctx context.Context, owner string, lease time.Duration) (GithubAuditJob, error) {
	var id string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET fence=fence WHERE stage='queued' AND outcome=''`); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT audit_id FROM github_audit_jobs WHERE stage='queued' AND outcome='' ORDER BY created_at,audit_id LIMIT 1`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("no queued audit")
		}
		if err != nil {
			return err
		}
		timestamp := now()
		until := time.Now().UTC().Add(lease).Format(time.RFC3339Nano)
		if _, err = tx.ExecContext(ctx, `UPDATE github_audit_jobs SET stage='fetching',lease_owner=?,lease_until=?,fence=fence+1,updated_at=?,message='Reading GitHub evidence' WHERE audit_id=? AND stage='queued' AND outcome=''`, owner, until, timestamp, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE audit_runs SET status='running',updated_at=? WHERE id=?`, timestamp, id)
		return err
	})
	if err != nil {
		return GithubAuditJob{}, err
	}
	return s.GetGithubAuditJob(ctx, id)
}
func (s *Store) HeartbeatGithubAudit(ctx context.Context, j GithubAuditJob, stage string) error {
	if stage != "fetching" && stage != "analyzing" && stage != "validating" {
		return invalid("invalid audit stage", nil)
	}
	message := map[string]string{"fetching": "Reading GitHub evidence", "analyzing": "Analyzing captured evidence", "validating": "Checking findings"}[stage]
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := githubAuditFenceTx(ctx, tx, j); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET stage=?,message=?,updated_at=?,lease_until=? WHERE audit_id=? AND fence=? AND lease_owner=? AND outcome='' AND lease_until>? AND deadline>?`, stage, message, now(), time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano), j.AuditID, j.Fence, j.LeaseOwner, now(), now())
		return githubFenceResult(result, err)
	})
}
func githubFenceResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return conflict("audit attempt was fenced or expired", nil)
	}
	return nil
}
func githubAuditFenceTx(ctx context.Context, tx *sql.Tx, j GithubAuditJob) error {
	result, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET updated_at=updated_at WHERE audit_id=? AND fence=? AND lease_owner=? AND outcome='' AND lease_until>? AND deadline>?`, j.AuditID, j.Fence, j.LeaseOwner, now(), now())
	if err = githubFenceResult(result, err); err != nil {
		return err
	}
	// Read identity from the durable row rather than trusting a caller's copy.
	stored, err := scanGithubAuditJob(tx.QueryRowContext(ctx, `SELECT `+githubAuditJobColumns+` FROM github_audit_jobs WHERE audit_id=?`, j.AuditID))
	if err != nil {
		return err
	}
	if err = githubAuditActorTx(ctx, tx, stored.AccountOwnerID); err != nil {
		return err
	}
	if stored.AuthorizationID != "" {
		a, err := validateGithubAuditAuthorizationTx(ctx, tx, stored.AuthorizationID, stored.ProjectID, stored.ActorID, stored.RequestTokenID)
		if err != nil {
			return err
		}
		if a.AccountOwnerID != stored.AccountOwnerID || a.SelectionVersion != stored.SelectionVersion {
			return forbidden("audit authorization identity changed")
		}
	} else if stored.ActorID != stored.AccountOwnerID {
		return forbidden("delegated audit requires authorization")
	}
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM github_project_settings WHERE project_id=?),1)`, j.ProjectID).Scan(&generation)
	if err != nil {
		return err
	}
	if generation != j.SelectionVersion {
		return conflict("repository selection changed", nil)
	}
	return nil
}
func (s *Store) SaveGithubAuditEvidence(ctx context.Context, j GithubAuditJob, e GithubAuditEvidence) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(raw) > 384*1024 {
		return invalid("audit evidence exceeds capture limit", nil)
	}
	files := 0
	for _, item := range e.Items {
		if item.Kind == "file" {
			files++
		}
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := githubAuditFenceTx(ctx, tx, j); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET evidence_json=?,captured_files=?,omitted_files=?,updated_at=? WHERE audit_id=? AND evidence_json IS NULL`, string(raw), files, e.OmittedFiles, now(), j.AuditID)
		return githubFenceResult(result, err)
	})
}
func (s *Store) CancelGithubAudit(ctx context.Context, id, outcome string) (GithubAuditJob, error) {
	return s.terminateGithubAudit(ctx, id, outcome, outcome, -1)
}

func (s *Store) FailGithubAudit(ctx context.Context, j GithubAuditJob, code string) error {
	_, err := s.terminateGithubAudit(ctx, j.AuditID, "failed", code, j.Fence)
	return err
}

func (s *Store) terminateGithubAudit(ctx context.Context, id, outcome, code string, fence int64) (GithubAuditJob, error) {
	if outcome != "cancelled" && outcome != "interrupted" && outcome != "failed" {
		return GithubAuditJob{}, invalid("invalid termination outcome", nil)
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		timestamp := now()
		_, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET outcome=?,stage='finished',fence=fence+1,lease_owner='',lease_until='',updated_at=?,finished_at=?,message=?,error_code=? WHERE audit_id=? AND outcome='' AND (?<0 OR fence=?)`, outcome, timestamp, timestamp, githubAuditFailureMessage(code), code, id, fence, fence)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE audit_runs SET status=CASE WHEN EXISTS(SELECT 1 FROM audit_findings WHERE audit_id=?) THEN 'partial' ELSE 'failed' END,updated_at=?,finalized_at=? WHERE id=? AND status IN ('queued','running') AND EXISTS(SELECT 1 FROM github_audit_jobs WHERE audit_id=? AND outcome<>'')`, id, timestamp, timestamp, id, id)
		return err
	})
	if err != nil {
		return GithubAuditJob{}, err
	}
	return s.GetGithubAuditJob(ctx, id)
}
func (s *Store) RecoverGithubAudits(ctx context.Context) error {
	// Evaluate expiry under the same writer lock as fencing. A heartbeat that
	// renews before this statement owns the lock must not be interrupted by an
	// earlier, stale recovery read from another server process.
	return s.withTx(ctx, func(tx *sql.Tx) error {
		timestamp := now()
		rows, err := tx.QueryContext(ctx, `UPDATE github_audit_jobs SET outcome='interrupted',stage='finished',fence=fence+1,lease_owner='',lease_until='',updated_at=?,finished_at=?,message=?,error_code='interrupted' WHERE outcome='' AND ((stage<>'queued' AND julianday(lease_until)<=julianday('now')) OR julianday(deadline)<=julianday('now')) RETURNING audit_id`, timestamp, timestamp, githubAuditFailureMessage("interrupted"))
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
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
		for _, id := range ids {
			if _, err = tx.ExecContext(ctx, `UPDATE audit_runs SET status=CASE WHEN EXISTS(SELECT 1 FROM audit_findings WHERE audit_id=?) THEN 'partial' ELSE 'failed' END,updated_at=?,finalized_at=? WHERE id=? AND status IN ('queued','running')`, id, timestamp, timestamp, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) RequireExternalAudit(ctx context.Context, tx *sql.Tx, id string) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM github_audit_jobs WHERE audit_id=?)+(SELECT COUNT(*) FROM audit_imports WHERE audit_id=?)`, id, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return conflict("server audit results are owned by the worker", nil)
	}
	return nil
}

// GithubAuditProtected distinguishes source-derived runs from manual external
// audits, including imported retained manifests without executable jobs.
func (s *Store) GithubAuditProtected(ctx context.Context, id string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM github_audit_jobs WHERE audit_id=?)+(SELECT COUNT(*) FROM github_audit_retained_evidence WHERE audit_id=?)`, id, id).Scan(&count)
	return count > 0, err
}

// CheckGithubAuditAttempt is a read-only phase fence. Cancellation from another
// process must stop extra planning/reads before a later heartbeat notices it.
func (s *Store) CheckGithubAuditAttempt(ctx context.Context, j GithubAuditJob) error {
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_audit_jobs WHERE audit_id=? AND fence=? AND lease_owner=? AND outcome='' AND lease_until>? AND deadline>?`, j.AuditID, j.Fence, j.LeaseOwner, now(), now()).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return conflict("audit attempt was fenced or expired", nil)
	}
	return nil
}
