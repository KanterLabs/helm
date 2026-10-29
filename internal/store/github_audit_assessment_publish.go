package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PublishGithubAssessments owns the explicit v2 publication path. It accounts
// for the entire captured scope and commits projection+assessment+terminal state
// together. Repository-wide partial coverage is deliberately not a verdict.
func (s *Store) PublishGithubAssessments(ctx context.Context, j GithubAuditJob, inputs []GithubAssessmentInput, outcome, code string) error {
	if outcome != "complete" && outcome != "partial" {
		return invalid("invalid assessment publication outcome", nil)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := githubAuditFenceTx(ctx, tx, j); err != nil {
			return err
		}
		var captureRaw string
		var evidenceRaw sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT capture_json,evidence_json FROM github_audit_jobs WHERE audit_id=?`, j.AuditID).Scan(&captureRaw, &evidenceRaw); err != nil {
			return err
		}
		var c GithubAuditCapture
		var e GithubAuditEvidence
		if err := json.Unmarshal([]byte(captureRaw), &c); err != nil {
			return err
		}
		if c.AssessmentVersion != GithubAssessmentVersion || !evidenceRaw.Valid || len(inputs) != len(c.Tasks) {
			return invalid("versioned assessments require captured evidence and the exact task set", nil)
		}
		if err := json.Unmarshal([]byte(evidenceRaw.String), &e); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, input := range inputs {
			if seen[input.TaskID] {
				return invalid("duplicate task assessment", nil)
			}
			seen[input.TaskID] = true
			assessment, projection, err := validateGithubAssessment(input, c, e)
			if err != nil {
				return err
			}
			if err = assessmentEligibilityTx(ctx, tx, j, &assessment, c); err != nil {
				return err
			}
			raw, err := json.Marshal(assessment)
			if err != nil {
				return err
			}
			refs, _ := json.Marshal(projection.EvidenceRefs)
			timestamp := now()
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,proposed_semantic_destination,confidence,reason,evidence_refs,review_state,version,created_at,updated_at,assessment_json) VALUES(?,?,?,?,?,?,?,?,?,?,'pending',1,?,?,?)`, newID(), j.AuditID, input.TaskID, input.CapturedVersion, input.SourceColumnID, projection.Verdict, nullableAuditDestinationArg(projection.ProposedSemanticDestination), projection.Confidence, input.Reason, string(refs), timestamp, timestamp, string(raw)); err != nil {
				return err
			}
		}
		return finishGithubAuditTx(ctx, tx, j, outcome, code)
	})
}
func finishGithubAuditTx(ctx context.Context, tx *sql.Tx, j GithubAuditJob, outcome, code string) error {
	timestamp := now()
	if _, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET stage='finished',outcome=?,message='Audit finished',error_code=?,finished_at=?,updated_at=?,lease_owner='',lease_until='' WHERE audit_id=?`, outcome, code, timestamp, timestamp, j.AuditID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE audit_runs SET status=?,finalized_at=?,updated_at=? WHERE id=?`, outcome, timestamp, timestamp, j.AuditID)
	return err
}
func assessmentEligibilityTx(ctx context.Context, tx *sql.Tx, j GithubAuditJob, a *GithubAssessment, c GithubAuditCapture) error {
	if a.Placement != "correction_recommended" {
		return nil
	}
	reasons := []string{}
	var version int64
	var column, claim, until string
	if err := tx.QueryRowContext(ctx, `SELECT version,column_id,COALESCE(claimed_by,''),COALESCE(claim_expires_at,'') FROM tasks WHERE id=? AND deleted_at IS NULL`, a.TaskID).Scan(&version, &column, &claim, &until); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		reasons = append(reasons, "task_unavailable")
	} else {
		if version != a.CapturedVersion || column != a.SourceColumnID {
			reasons = append(reasons, "task_changed")
		}
		if claim != "" && until > now() {
			reasons = append(reasons, "active_claim")
		}
	}
	if a.TargetColumnID != nil && a.TargetSemanticState != nil {
		var currentVersion int64
		var state string
		err := tx.QueryRowContext(ctx, `SELECT version,semantic_state FROM columns WHERE id=? AND project_id=? AND archived_at IS NULL`, *a.TargetColumnID, j.ProjectID).Scan(&currentVersion, &state)
		if err == sql.ErrNoRows {
			reasons = append(reasons, "destination_unavailable")
		} else if err != nil {
			return err
		} else {
			capturedVersion := int64(0)
			for _, col := range c.Columns {
				if col.ID == *a.TargetColumnID {
					capturedVersion = col.Version
				}
			}
			if state != *a.TargetSemanticState || currentVersion != capturedVersion {
				reasons = append(reasons, "destination_changed")
			}
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM columns WHERE project_id=? AND semantic_state=? AND archived_at IS NULL`, j.ProjectID, *a.TargetSemanticState).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				reasons = append(reasons, "destination_mapping_changed")
			}
		}
	}
	for _, task := range c.Tasks {
		if task.ID != a.TaskID {
			continue
		}
		if a.TargetSemanticState != nil && *a.TargetSemanticState == "completed" {
			if task.ChecklistPolicy == "require" || task.ChecklistPolicy == "enforce" {
				for _, condition := range task.Conditions {
					if condition.Completed != nil && !*condition.Completed {
						reasons = append(reasons, "open_required_checklist")
						break
					}
				}
			}
			for _, d := range task.Context.Dependencies {
				if d.SemanticState != "completed" {
					reasons = append(reasons, "unmet_dependencies")
					break
				}
			}
		}
	}
	if len(reasons) > 0 {
		a.ApplicationEligibility = GithubAssessmentEligibility{Status: "blocked", Reasons: reasons}
	}
	return nil
}
