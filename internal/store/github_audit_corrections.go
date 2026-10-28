package store

import (
	"context"
	"database/sql"
	"errors"
)

// ApplyGithubAuditCorrection composes approval, ordinary lifecycle and receipt
// under one writer transaction. Each batch item is independent. fence checks
// process-local provider generation/cancellation after acquiring the writer lock.
func (s *Store) ApplyGithubAuditCorrection(ctx context.Context, auditID, actorID, key, requestHash string, input GithubAuditCorrectionInput, expectedFingerprint, unavailable string, fence func() error) (GithubAuditCorrectionResult, error) {
	r := correctionResult(auditID, actorID, input)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// Acquire the writer before inspecting any facts. A deferred read snapshot
		// must never authorize against a writer that won the race while we waited.
		if _, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET updated_at=updated_at WHERE audit_id=?`, auditID); err != nil {
			return err
		}
		var projectID string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM github_audit_jobs WHERE audit_id=?`, auditID).Scan(&projectID); err != nil {
			return err
		}
		if err := correctionHumanTx(ctx, tx, projectID, actorID); err != nil {
			return err
		}
		existing, ok, err := correctionReceiptTx(ctx, tx, auditID, actorID, key, requestHash, input.FindingID)
		if err != nil {
			return err
		}
		if ok {
			r = existing
			return nil
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO github_audit_correction_requests(audit_id,actor_id,request_key,request_hash,created_at) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, auditID, actorID, key, requestHash, now()); err != nil {
			return err
		}
		var c correctionContext
		r, c, err = s.inspectCorrectionTx(ctx, tx, auditID, actorID, input)
		if err != nil {
			return err
		}
		r.RequestKey = key
		if unavailable != "" {
			r.Outcome = "skipped"
			r.Message = unavailable
		}
		if r.Outcome == "eligible" && (expectedFingerprint == "" || expectedFingerprint != r.Fingerprint) {
			r.Outcome = "skipped"
			r.Message = "Preview expired, changed or belongs to another reviewer; refresh the preview"
		}
		if fence != nil {
			if err = fence(); err != nil {
				if errors.Is(err, ErrConflict) {
					r.Outcome = "skipped"
					r.Message = err.Error()
					return saveCorrectionReceiptTx(ctx, tx, r, c, input.ConfirmationToken)
				}
				return err
			}
		}
		if r.Outcome != "eligible" {
			return saveCorrectionReceiptTx(ctx, tx, r, c, input.ConfirmationToken)
		}
		if _, err = tx.ExecContext(ctx, `SAVEPOINT correction_lifecycle`); err != nil {
			return err
		}
		err = s.applyCorrectionLifecycleTx(ctx, tx, c, actorID, input)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE audit_findings SET review_state='approved',version=version+1,updated_at=? WHERE id=? AND version=?`, now(), c.finding.ID, c.finding.Version)
		}
		if err == nil {
			_, err = insertEvent(ctx, tx, "audit.correction_applied", actorID, projectID, r.TaskID, map[string]any{"audit_id": auditID, "finding_id": input.FindingID, "action": r.Action, "from_column_id": r.SourceColumnID, "to_column_id": r.TargetColumnID, "captured_version": r.CapturedVersion, "assessment_hash": c.assessmentHash, "source_hash": c.sourceHash, "request_key": key})
		}
		if err != nil {
			if _, rollbackErr := tx.ExecContext(ctx, `ROLLBACK TO correction_lifecycle`); rollbackErr != nil {
				return rollbackErr
			}
			r.Outcome = "failed"
			r.Message = "Lifecycle operation failed; retry with this request key to inspect the durable result"
			var business *Error
			if errors.As(err, &business) {
				r.Outcome = "skipped"
				r.Message = business.Message + "; open the card or re-audit"
			}
		} else {
			r.Outcome = "applied"
			r.Message = "Reviewed correction applied"
			task, e := taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=?`, r.TaskID))
			if e != nil {
				return e
			}
			task.Key = c.finding.Assessment.TaskKey
			if task.Kind == bugKind {
				bug, e := bugFromRow(tx.QueryRowContext(ctx, `SELECT reporter_id,severity,actual_behavior,expected_behavior,reproduction_steps,environment,affected_version,resolution,resolved_by,resolved_at,duplicate_of FROM bug_details WHERE task_id=?`, task.ID))
				if e != nil {
					return e
				}
				task.Bug = &bug
			}
			r.Task = &GithubAuditCorrectionTaskSnapshot{ID: task.ID, Key: task.Key, ProjectID: task.ProjectID, Kind: task.Kind, ColumnID: task.ColumnID, Version: task.Version, Position: task.Position, CompletedAt: task.CompletedAt, ClaimedBy: task.ClaimedBy, ClaimExpiresAt: task.ClaimExpiresAt, Bug: task.Bug}
		}
		if _, err = tx.ExecContext(ctx, `RELEASE correction_lifecycle`); err != nil {
			return err
		}
		return saveCorrectionReceiptTx(ctx, tx, r, c, input.ConfirmationToken)
	})
	return r, err
}
func (s *Store) applyCorrectionLifecycleTx(ctx context.Context, tx *sql.Tx, c correctionContext, actorID string, input GithubAuditCorrectionInput) error {
	a := c.finding.Assessment
	t := c.task
	target := c.target
	switch a.LifecycleAction {
	case "complete":
		return s.transitionTaskTx(ctx, tx, t, target, actorID, t.Version, "completed", "task.completed", "", false)
	case "block":
		return s.transitionTaskTx(ctx, tx, t, target, actorID, t.Version, "blocked", "task.blocked", a.Reason, false)
	case "reopen", "start":
		return s.updateTaskColumnTx(ctx, tx, t, target, t.Version, actorID, false)
	case "move":
		return s.moveTaskTx(ctx, tx, t, TaskMoveInput{DestinationColumnID: target.ID, ExpectedSourceColumnID: t.ColumnID, Source: "github_audit", Reason: a.Reason}, t.Version, actorID)
	case "bug_resolve":
		return s.resolveBugTx(ctx, tx, t, target, actorID, t.Version, input.Resolution, "", "", now(), false)
	case "bug_reopen":
		return s.reopenBugTx(ctx, tx, t, target, actorID, t.Version, input.Reason, now(), target.SemanticState, false)
	}
	return invalid("unsupported audit lifecycle operation", nil)
}
