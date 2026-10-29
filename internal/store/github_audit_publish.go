package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PublishGithubAudit validates every identity against the immutable capture,
// then commits findings and terminal outcome atomically under the attempt fence.
func (s *Store) PublishGithubAudit(ctx context.Context, j GithubAuditJob, inputs []AuditFindingInput, outcome, code string) error {
	if outcome != "complete" && outcome != "partial" && outcome != "failed" {
		return invalid("invalid publication outcome", nil)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := githubAuditFenceTx(ctx, tx, j); err != nil {
			return err
		}
		var capturedRaw string
		var evidenceRaw sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT capture_json,evidence_json FROM github_audit_jobs WHERE audit_id=?`, j.AuditID).Scan(&capturedRaw, &evidenceRaw); err != nil {
			return err
		}
		var capture GithubAuditCapture
		var evidence GithubAuditEvidence
		if err := json.Unmarshal([]byte(capturedRaw), &capture); err != nil {
			return err
		}
		if evidenceRaw.Valid {
			if err := json.Unmarshal([]byte(evidenceRaw.String), &evidence); err != nil {
				return err
			}
		}
		if capture.AssessmentVersion != 0 {
			return invalid("versioned capture requires assessment publication", nil)
		}
		if outcome == "complete" && (evidence.Partial || !evidenceRaw.Valid || len(inputs) != len(capture.Tasks)) {
			return invalid("incomplete evidence cannot publish a complete audit", nil)
		}
		tasks := map[string]GithubAuditTask{}
		refs := map[string]bool{}
		seen := map[string]bool{}
		for _, task := range capture.Tasks {
			tasks[task.ID] = task
		}
		for _, item := range evidence.Items {
			refs[item.ID] = true
		}
		for _, input := range inputs {
			task, ok := tasks[input.TaskID]
			if !ok || seen[input.TaskID] {
				return invalid("finding task is outside captured scope or duplicated", nil)
			}
			seen[input.TaskID] = true
			input.CapturedVersion = task.Version
			input.SourceColumn = task.ColumnID
			input.SourceColumnID = task.ColumnID
			input.ReviewState = "pending"
			if input.ProposedSemanticDestination != nil && *input.ProposedSemanticDestination == task.SemanticState {
				return invalid("proposed move must change the captured semantic state", nil)
			}
			validated, err := validateAuditFindingInput(input)
			if err != nil {
				return err
			}
			if len(validated.EvidenceRefs) == 0 && validated.Verdict != "needs_attention" {
				return invalid("a conclusive finding requires captured evidence", nil)
			}
			if evidence.Partial && validated.Verdict == "correct" {
				return invalid("partial coverage cannot establish a correct task", nil)
			}
			for _, ref := range validated.EvidenceRefs {
				if !refs[ref] {
					return invalid("finding cites unknown evidence", nil)
				}
			}
			raw, _ := json.Marshal(validated.EvidenceRefs)
			timestamp := now()
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,proposed_semantic_destination,confidence,reason,evidence_refs,review_state,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,'pending',1,?,?)`, newID(), j.AuditID, task.ID, task.Version, task.ColumnID, validated.Verdict, nullableAuditDestinationArg(validated.ProposedSemanticDestination), validated.Confidence, validated.Reason, string(raw), timestamp, timestamp); err != nil {
				return err
			}
		}
		timestamp := now()
		if _, err := tx.ExecContext(ctx, `UPDATE github_audit_jobs SET stage='finished',outcome=?,message='Audit finished',error_code=?,finished_at=?,updated_at=?,lease_owner='',lease_until='' WHERE audit_id=?`, outcome, code, timestamp, timestamp, j.AuditID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE audit_runs SET status=?,finalized_at=?,updated_at=? WHERE id=?`, outcome, timestamp, timestamp, j.AuditID)
		return err
	})
}
