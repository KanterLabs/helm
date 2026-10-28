package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

func correctionHash(v any) string {
	raw, _ := json.Marshal(v)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// Human sessions have project access under the normal auth contract; unlike
// agent ceilings they have no actor_projects restriction. Re-read enabled actor
// and project existence inside the writer transaction, including receipt reads.
func correctionHumanTx(ctx context.Context, q dependencySQL, projectID, actorID string) error {
	if err := githubAuditActorTx(ctx, q, actorID); err != nil {
		return err
	}
	var id string
	return q.QueryRowContext(ctx, `SELECT id FROM projects WHERE id=?`, projectID).Scan(&id)
}

type correctionContext struct {
	finding                    AuditFinding
	task                       Task
	target                     Column
	job                        GithubAuditJob
	assessmentHash, sourceHash string
}

func correctionResult(auditID, actorID string, input GithubAuditCorrectionInput) GithubAuditCorrectionResult {
	return GithubAuditCorrectionResult{AuditID: auditID, ActorID: actorID, FindingID: input.FindingID, Outcome: "skipped", OutcomeKnown: true, Message: "Re-audit this card before applying"}
}

// inspectCorrectionTx performs only reads. Publication eligibility is never used
// as authority. The same live checks execute after taking the apply writer lock.
func (s *Store) inspectCorrectionTx(ctx context.Context, tx *sql.Tx, auditID, actorID string, input GithubAuditCorrectionInput) (GithubAuditCorrectionResult, correctionContext, error) {
	r := correctionResult(auditID, actorID, input)
	c := correctionContext{}
	if err := githubAuditActorTx(ctx, tx, actorID); err != nil {
		return r, c, err
	}
	job, err := scanGithubAuditJob(tx.QueryRowContext(ctx, `SELECT `+githubAuditJobColumns+` FROM github_audit_jobs WHERE audit_id=?`, auditID))
	if err != nil {
		return r, c, err
	}
	c.job = job
	if err = correctionHumanTx(ctx, tx, job.ProjectID, actorID); err != nil {
		return r, c, err
	}
	f, err := auditFindingFromRow(tx.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.id=? AND af.audit_id=?`, input.FindingID, auditID))
	if err != nil {
		return r, c, err
	}
	c.finding = f
	r.TaskID = f.TaskID
	a := f.Assessment
	if a == nil || a.AssessmentVersion != 2 || a.Placement != "correction_recommended" || a.TargetColumnID == nil || a.TargetSemanticState == nil || a.LifecycleAction == "none" {
		r.Message = "An operational v2 correction is required"
		return r, c, nil
	}
	c.assessmentHash = correctionHash(a)
	r.Baselines = a.Baselines
	r.Action = a.LifecycleAction
	r.TargetColumnID = *a.TargetColumnID
	r.TargetSemanticState = *a.TargetSemanticState
	r.SourceColumnID = a.SourceColumnID
	r.CapturedVersion = a.CapturedVersion
	if f.ReviewState == "dismissed" {
		r.Message = "Finding was rejected; review the current assessment"
		return r, c, nil
	}
	if job.Outcome != "complete" && job.Outcome != "partial" {
		r.Message = "Audit has not finished successfully"
		return r, c, nil
	}
	selection, err := githubSelection(ctx, tx, job.ProjectID)
	if err != nil {
		return r, c, err
	}
	if selection.Version != job.SelectionVersion || job.SelectionVersion <= 0 {
		r.Message = "Repository selection changed; re-audit this card"
		return r, c, nil
	}
	if err = githubAuditActorTx(ctx, tx, job.AccountOwnerID); err != nil {
		r.Message = "Source account authorization changed; reconnect and re-audit"
		return r, c, nil
	}
	if job.AuthorizationID != "" {
		grant, e := validateGithubAuditAuthorizationTx(ctx, tx, job.AuthorizationID, job.ProjectID, job.ActorID, job.RequestTokenID)
		if e != nil || grant.AccountOwnerID != job.AccountOwnerID || grant.SelectionVersion != job.SelectionVersion {
			r.Message = "Audit source authorization changed; re-audit this card"
			return r, c, nil
		}
	} else if job.ActorID != job.AccountOwnerID {
		return r, c, forbidden("audit account owner changed")
	}
	var captureRaw, evidenceRaw string
	if err = tx.QueryRowContext(ctx, `SELECT capture_json,COALESCE(evidence_json,'') FROM github_audit_jobs WHERE audit_id=?`, auditID).Scan(&captureRaw, &evidenceRaw); err != nil {
		return r, c, err
	}
	c.sourceHash = correctionHash(evidenceRaw)
	var capture GithubAuditCapture
	if err = json.Unmarshal([]byte(captureRaw), &capture); err != nil {
		return r, c, err
	}
	if capture.AssessmentVersion != 2 || evidenceRaw == "" {
		r.Message = "Operational v2 capture and source are required"
		return r, c, nil
	}
	live, err := captureGithubAuditTasks(ctx, tx, job.ProjectID, GithubAuditStartInput{TaskIDs: []string{f.TaskID}})
	if err != nil {
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
			r.Message = "Task is unavailable or changed; open the card or re-audit"
			return r, c, nil
		}
		return r, c, err
	}
	var original *GithubAuditTask
	for i := range capture.Tasks {
		if capture.Tasks[i].ID == f.TaskID {
			original = &capture.Tasks[i]
			break
		}
	}
	if original == nil || len(live.Tasks) != 1 || !reflect.DeepEqual(*original, live.Tasks[0]) {
		r.Message = "Task, criteria, claim, dependency or project policy changed; re-audit this card"
		return r, c, nil
	}
	// Compare mapping identities and versions, excluding ordering revisions. Another
	// independent correction may append a card without invalidating this preview.
	type mapping struct {
		ID, Name, State string
		Version         int64
	}
	maps := func(cols []Column) []mapping {
		v := []mapping{}
		for _, col := range cols {
			v = append(v, mapping{col.ID, col.Name, col.SemanticState, col.Version})
		}
		return v
	}
	if !reflect.DeepEqual(maps(capture.Columns), maps(live.Columns)) {
		r.Message = "Column mapping or version changed; re-audit this card"
		return r, c, nil
	}
	for _, col := range live.Columns {
		if col.ID == r.TargetColumnID {
			c.target = col
		}
	}
	if c.target.ID == "" || c.target.SemanticState != r.TargetSemanticState {
		r.Message = "Captured destination is unavailable; re-audit this card"
		return r, c, nil
	}
	matches := 0
	for _, col := range live.Columns {
		if col.SemanticState == r.TargetSemanticState {
			matches++
		}
	}
	if matches != 1 {
		r.Message = "Destination mapping is ambiguous; choose a mapping and re-audit"
		return r, c, nil
	}
	task, err := taskFromRow(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.id=? AND t.deleted_at IS NULL`, f.TaskID))
	if err != nil {
		return r, c, err
	}
	c.task = task
	foreign, _, err := activeClaimByOtherTx(ctx, tx, task.ID, actorID, now())
	if err != nil {
		return r, c, err
	}
	if foreign {
		r.Message = "Card is claimed by another actor; open the card after its owner releases it"
		return r, c, nil
	}
	if r.Action == "move" {
		var active int
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND claimed_by IS NOT NULL AND julianday(claim_expires_at)>julianday('now'))`, task.ID).Scan(&active)
		if err != nil {
			return r, c, err
		}
		if active != 0 {
			r.Message = "Release your claim explicitly before moving this card"
			return r, c, nil
		}
	}
	if r.TargetSemanticState == "completed" {
		status, e := checklistCompletionStatusForTaskTx(ctx, tx, job.ProjectID, task.ID)
		if e != nil {
			return r, c, e
		}
		if e = rejectIncompleteChecklist(status); e != nil {
			r.Message = "Required checklist items are incomplete; open the card"
			return r, c, nil
		}
	}
	dep := dependencyTaskFromTask(task, original.SemanticState)
	if r.TargetSemanticState == "completed" || r.TargetSemanticState == "active" {
		unmet, e := dependencyUnmetPrerequisites(ctx, tx, dep)
		if e != nil {
			return r, c, e
		}
		if len(unmet) > 0 {
			r.Message = "Prerequisite cards are unfinished; open the card"
			return r, c, nil
		}
	}
	if original.SemanticState == "completed" && r.TargetSemanticState != "completed" {
		dependents, e := dependencyLiveDependents(ctx, tx, dep, false)
		if e != nil {
			return r, c, e
		}
		if len(dependents) > 0 {
			r.Message = "Reopening would block active dependents; open the dependent cards first"
			return r, c, nil
		}
	}
	switch r.Action {
	case "bug_resolve":
		r.RequiredInputs = []string{"resolution"}
		if input.Resolution != "fixed" {
			r.Message = "Select fixed to confirm this bug recommendation"
			return r, c, nil
		}
	case "bug_reopen":
		r.RequiredInputs = []string{"reason"}
		if strings.TrimSpace(input.Reason) == "" {
			r.Message = "Enter a bug reopening reason"
			return r, c, nil
		}
	case "complete", "reopen", "start", "block", "move":
		if input.Resolution != "" || input.Reason != "" {
			r.Message = "This correction does not accept bug policy inputs"
			return r, c, nil
		}
	default:
		r.Message = "Unsupported lifecycle action; re-audit this card"
		return r, c, nil
	}
	if len(input.Reason) > 10000 {
		r.Message = "Reopening reason is too long"
		return r, c, nil
	}
	r.Outcome = "eligible"
	r.Message = "Review the exact correction, then Apply"
	c.assessmentHash = correctionHash(a)
	c.sourceHash = correctionHash(evidenceRaw)
	r.Fingerprint = correctionHash([]any{actorID, job, f, live.Tasks[0], maps(live.Columns), c.assessmentHash, c.sourceHash, input.Resolution, input.Reason})
	return r, c, nil
}

func (s *Store) PreviewGithubAuditCorrection(ctx context.Context, auditID, actorID string, input GithubAuditCorrectionInput) (GithubAuditCorrectionResult, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GithubAuditCorrectionResult{}, err
	}
	defer tx.Rollback()
	r, _, err := s.inspectCorrectionTx(ctx, tx, auditID, actorID, input)
	return r, err
}
