package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func correctionReceiptTx(ctx context.Context, q dependencySQL, auditID, actorID, key, requestHash, findingID string) (GithubAuditCorrectionResult, bool, error) {
	var hash string
	err := q.QueryRowContext(ctx, `SELECT request_hash FROM github_audit_correction_requests WHERE audit_id=? AND actor_id=? AND request_key=?`, auditID, actorID, key).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GithubAuditCorrectionResult{}, false, err
	}
	if err == nil && hash != requestHash {
		return GithubAuditCorrectionResult{}, false, conflict("Idempotency-Key was used for a different correction request", nil)
	}
	var raw string
	err = q.QueryRowContext(ctx, `SELECT result_json FROM github_audit_correction_receipts WHERE audit_id=? AND actor_id=? AND request_key=? AND finding_id=?`, auditID, actorID, key, findingID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		// Another human or logical request may already have applied this immutable
		// finding. Return its original task result, with the original actor/provenance.
		err = q.QueryRowContext(ctx, `SELECT result_json FROM github_audit_correction_receipts WHERE audit_id=? AND finding_id=? AND outcome='applied'`, auditID, findingID).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return GithubAuditCorrectionResult{}, false, nil
	}
	if err != nil {
		return GithubAuditCorrectionResult{}, false, err
	}
	var r GithubAuditCorrectionResult
	if err = json.Unmarshal([]byte(raw), &r); err != nil {
		return r, false, err
	}
	if r.Outcome == "applied" {
		r.Outcome = "already_applied"
		r.Message = "Correction was already applied; original committed result returned"
	}
	return r, true, nil
}

// ReadGithubAuditCorrectionReceipt intentionally requires current human/project
// authorization but no fresh remote source: a lost-response retry must retain its
// durable outcome even if the source advanced or was subsequently disconnected.
func (s *Store) ReadGithubAuditCorrectionReceipt(ctx context.Context, auditID, actorID, key, requestHash, findingID string) (GithubAuditCorrectionResult, bool, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GithubAuditCorrectionResult{}, false, err
	}
	defer tx.Rollback()
	var projectID string
	if err = tx.QueryRowContext(ctx, `SELECT project_id FROM github_audit_jobs WHERE audit_id=?`, auditID).Scan(&projectID); err != nil {
		return GithubAuditCorrectionResult{}, false, err
	}
	if err = correctionHumanTx(ctx, tx, projectID, actorID); err != nil {
		return GithubAuditCorrectionResult{}, false, err
	}
	return correctionReceiptTx(ctx, tx, auditID, actorID, key, requestHash, findingID)
}
func saveCorrectionReceiptTx(ctx context.Context, tx *sql.Tx, r GithubAuditCorrectionResult, c correctionContext, preview string) error {
	if c.assessmentHash == "" && c.finding.Assessment != nil {
		c.assessmentHash = correctionHash(c.finding.Assessment)
	}
	if c.sourceHash == "" {
		var source string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(evidence_json,'') FROM github_audit_jobs WHERE audit_id=?`, r.AuditID).Scan(&source); err != nil {
			return err
		}
		c.sourceHash = correctionHash(source)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_audit_correction_receipts(audit_id,finding_id,actor_id,request_key,task_id,action,assessment_hash,source_hash,preview_hash,outcome,result_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, r.AuditID, r.FindingID, r.ActorID, r.RequestKey, r.TaskID, r.Action, c.assessmentHash, c.sourceHash, correctionHash(preview), r.Outcome, string(raw), now())
	return err
}
