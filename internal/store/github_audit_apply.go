package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type GithubAuditApplyResult struct {
	FindingID string `json:"finding_id"`
	TaskID    string `json:"task_id"`
	Outcome   string `json:"outcome"`
	Message   string `json:"message"`
	Task      *Task  `json:"task,omitempty"`
}

// ApplyGithubAuditFinding performs the local half of an explicitly requested
// reconciliation after the HTTP service has checked current remote freshness.
// Approval, preview, source, version, destination, claim and selection guards
// share the writer transaction with the task mutation and deterministic receipt.
func (s *Store) ApplyGithubAuditFinding(ctx context.Context, job GithubAuditJob, findingID, actorID string, preview bool) (GithubAuditApplyResult, error) {
	result := GithubAuditApplyResult{FindingID: findingID, Outcome: "skipped", Message: "Finding is no longer eligible"}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET updated_at=updated_at WHERE audit_id=?`, job.AuditID); err != nil {
			return err
		}
		if err := githubAuditActorTx(ctx, tx, actorID); err != nil {
			return err
		}
		var outcome string
		var selection int64
		if err := tx.QueryRowContext(ctx, `SELECT outcome,selection_version FROM github_audit_jobs WHERE audit_id=?`, job.AuditID).Scan(&outcome, &selection); err != nil {
			return err
		}
		if selection <= 0 || selection != job.SelectionVersion || (outcome != "complete" && outcome != "partial") {
			result.Message = "Audit is not eligible for application"
			return nil
		}
		current, err := githubSelection(ctx, tx, job.ProjectID)
		if err != nil {
			return err
		}
		if current.Version != selection {
			result.Message = "Repository selection changed"
			return nil
		}
		finding, err := auditFindingFromRow(tx.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.id=? AND af.audit_id=?`, findingID, job.AuditID))
		if err != nil {
			return err
		}
		result.TaskID = finding.TaskID
		var applied sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT applied_at FROM github_audit_apply_previews WHERE audit_id=? AND finding_id=? AND actor_id=?`, job.AuditID, findingID, actorID).Scan(&applied)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if applied.Valid {
			result.Outcome = "applied"
			result.Message = "Already applied"
			return nil
		}
		if finding.ReviewState != "approved" || finding.Verdict != "move_proposed" || finding.ProposedSemanticDestination == nil {
			result.Message = "Approve a proposed move before applying"
			return nil
		}
		destination := *finding.ProposedSemanticDestination
		if destination != "backlog" && destination != "ready" {
			result.Outcome = "needs_follow_up"
			result.Message = "Use the normal task lifecycle for this destination"
			return nil
		}
		var destinationID string
		err = tx.QueryRowContext(ctx, `SELECT id FROM columns WHERE project_id=? AND semantic_state=? AND archived_at IS NULL ORDER BY position,id LIMIT 1`, job.ProjectID, destination).Scan(&destinationID)
		if errors.Is(err, sql.ErrNoRows) {
			result.Message = "Safe destination is unavailable"
			return nil
		}
		if err != nil {
			return err
		}
		var eligible int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks t JOIN columns c ON c.id=t.column_id WHERE t.id=? AND t.project_id=? AND t.version=? AND t.column_id=? AND t.deleted_at IS NULL AND t.completed_at IS NULL AND c.archived_at IS NULL AND c.semantic_state IN ('backlog','ready','active','blocked') AND (t.claimed_by IS NULL OR t.claim_expires_at IS NULL OR julianday(t.claim_expires_at) IS NULL OR julianday(t.claim_expires_at)<=julianday('now'))`, finding.TaskID, job.ProjectID, finding.CapturedVersion, finding.SourceColumn).Scan(&eligible)
		if err != nil {
			return err
		}
		if eligible != 1 {
			result.Message = "Task changed or has an active claim"
			return nil
		}
		if preview {
			_, err = tx.ExecContext(ctx, `INSERT INTO github_audit_apply_previews(audit_id,finding_id,actor_id,finding_version,task_version,destination,previewed_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(audit_id,finding_id,actor_id) DO UPDATE SET finding_version=excluded.finding_version,task_version=excluded.task_version,destination=excluded.destination,previewed_at=excluded.previewed_at WHERE github_audit_apply_previews.applied_at IS NULL`, job.AuditID, findingID, actorID, finding.Version, finding.CapturedVersion, destinationID, now())
			result.Outcome = "eligible"
			result.Message = "Approved unchanged task can move"
			return err
		}
		var matches int
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_audit_apply_previews WHERE audit_id=? AND finding_id=? AND actor_id=? AND finding_version=? AND task_version=? AND destination=? AND previewed_at>=? AND applied_at IS NULL`, job.AuditID, findingID, actorID, finding.Version, finding.CapturedVersion, destinationID, time.Now().UTC().Add(-5*time.Minute).Format(time.RFC3339Nano)).Scan(&matches)
		if err != nil {
			return err
		}
		if matches != 1 {
			result.Message = "Preview this unchanged approved finding before applying"
			return nil
		}
		mutation, err := tx.ExecContext(ctx, `UPDATE tasks SET column_id=?,position=COALESCE((SELECT MAX(position)+1 FROM tasks other WHERE other.column_id=? AND other.deleted_at IS NULL),0),claimed_by=NULL,claim_expires_at=NULL,version=version+1,updated_at=? WHERE id=? AND project_id=? AND version=? AND column_id=? AND deleted_at IS NULL AND completed_at IS NULL AND version<9223372036854775807 AND (claimed_by IS NULL OR claim_expires_at IS NULL OR julianday(claim_expires_at) IS NULL OR julianday(claim_expires_at)<=julianday('now'))`, destinationID, destinationID, now(), finding.TaskID, job.ProjectID, finding.CapturedVersion, finding.SourceColumn)
		if err = githubFenceResult(mutation, err); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE github_audit_apply_previews SET applied_at=? WHERE audit_id=? AND finding_id=? AND actor_id=?`, now(), job.AuditID, findingID, actorID); err != nil {
			return err
		}
		_, err = insertEvent(ctx, tx, "task.moved", actorID, job.ProjectID, finding.TaskID, map[string]any{"from_column_id": finding.SourceColumn, "to_column_id": destinationID, "version": finding.CapturedVersion + 1, "actor_id": actorID, "source": "github_audit", "reason": "Approved GitHub audit recommendation", "audit_id": job.AuditID, "finding_id": findingID})
		if err != nil {
			return err
		}
		result.Outcome = "applied"
		result.Message = "Task moved"
		return nil
	})
	if err != nil {
		return result, err
	}
	if result.Outcome == "applied" {
		task, err := s.GetTask(ctx, result.TaskID)
		if err == nil {
			result.Task = &task
		}
	}
	return result, nil
}
