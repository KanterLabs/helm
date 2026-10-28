package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func auditRunFromRow(scanner interface{ Scan(...any) error }) (AuditRun, error) {
	var run AuditRun
	var finalized sql.NullString
	var summary GithubAssessmentSummary
	if err := scanner.Scan(&run.ID, &run.ProjectID, &run.ActorID, &run.Scope, &run.Status, &run.StartedAt, &finalized, &run.CreatedAt, &run.UpdatedAt, &run.FindingCount, &summary.Corrections, &summary.Correct, &summary.UnableToVerify); err != nil {
		return AuditRun{}, err
	}
	summary.Checked = summary.Corrections + summary.Correct + summary.UnableToVerify
	if summary.Checked > 0 {
		run.AssessmentSummary = &summary
	}
	run.FinalizedAt = nullableString(finalized)
	run.Findings = []AuditFinding{}
	return run, nil
}

const auditRunColumns = `ar.id, ar.project_id, ar.actor_id, ar.scope, ar.status,
	ar.started_at, ar.finalized_at, ar.created_at, ar.updated_at,
	(SELECT COUNT(1) FROM audit_findings afc WHERE afc.audit_id = ar.id),
 (SELECT COUNT(*) FROM audit_findings f WHERE f.audit_id=ar.id AND json_extract(f.assessment_json,'$.placement')='correction_recommended'),
 (SELECT COUNT(*) FROM audit_findings f WHERE f.audit_id=ar.id AND json_extract(f.assessment_json,'$.placement')='column_correct'),
 (SELECT COUNT(*) FROM audit_findings f WHERE f.audit_id=ar.id AND json_extract(f.assessment_json,'$.placement')='unable_to_verify')`

// CreateAuditRun starts an audit without emitting an event or changing any
// task-side state. The actor and project are checked before insertion so a
// dangling audit cannot be created by a direct store caller.
func (s *Store) CreateAuditRun(ctx context.Context, projectID, actorID string, input AuditRunInput) (AuditRun, error) {
	validated, err := validateAuditRunInput(input)
	if err != nil {
		return AuditRun{}, err
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return AuditRun{}, err
	}
	if _, err := s.GetActor(ctx, actorID); err != nil {
		return AuditRun{}, err
	}
	id, timestamp := newID(), now()
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		// Acquire the writer lock before counting so concurrent creators cannot
		// cross the retained-run ceiling. Refuse rather than silently delete
		// historical audits; cleanup remains an explicit operator/user action.
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, projectID); err != nil {
			return err
		}
		var retained int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM audit_runs WHERE project_id=?`, projectID).Scan(&retained); err != nil {
			return err
		}
		if retained >= maxAuditRunsPerProject {
			return invalid("project has reached the retained audit run limit", map[string]any{"limit": maxAuditRunsPerProject})
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_runs(id, project_id, actor_id, scope, status, started_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, projectID, actorID, validated.Scope, validated.Status, timestamp, timestamp, timestamp)
		return err
	}); err != nil {
		return AuditRun{}, err
	}
	return s.getAuditRunSummary(ctx, id)
}

// GetAuditRun returns bounded run metadata. Consumers fetch findings through
// ListAuditFindings so a detail request can never expand to an unbounded body.
func (s *Store) GetAuditRun(ctx context.Context, id string) (AuditRun, error) {
	return s.getAuditRunSummary(ctx, id)
}

func (s *Store) getAuditRunSummary(ctx context.Context, id string) (AuditRun, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+auditRunColumns+` FROM audit_runs ar WHERE ar.id=?`, id)
	run, err := auditRunFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditRun{}, notFound("audit run not found")
	}
	return run, err
}

// ListAuditRuns returns one bounded page for a project. Use
// ListAuditRunsForProjects for a token-scoped global collection.
func (s *Store) ListAuditRuns(ctx context.Context, projectID string, limit, offset int) ([]AuditRun, bool, error) {
	return s.listAuditRuns(ctx, []string{projectID}, false, limit, offset)
}

// ListAuditRunsForProjects lists runs across an explicit project ceiling. A
// nil projectIDs slice means unscoped/all projects; a non-nil empty slice
// intentionally matches no rows.
func (s *Store) ListAuditRunsForProjects(ctx context.Context, projectIDs []string, limit, offset int) ([]AuditRun, bool, error) {
	return s.listAuditRuns(ctx, projectIDs, true, limit, offset)
}

// ListAudits is a compatibility alias for callers using the shorter resource
// name. It retains the same project-scoped pagination semantics.
func (s *Store) ListAudits(ctx context.Context, projectID string, limit, offset int) ([]AuditRun, bool, error) {
	return s.ListAuditRuns(ctx, projectID, limit, offset)
}

func (s *Store) listAuditRuns(ctx context.Context, projectIDs []string, allowAll bool, limit, offset int) ([]AuditRun, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT ` + auditRunColumns + ` FROM audit_runs ar`
	args := make([]any, 0, len(projectIDs)+2)
	if !allowAll || projectIDs != nil {
		if len(projectIDs) == 0 {
			query += ` WHERE 1=0`
		} else {
			placeholders := make([]string, len(projectIDs))
			for i, projectID := range projectIDs {
				placeholders[i] = "?"
				args = append(args, projectID)
			}
			query += ` WHERE ar.project_id IN (` + strings.Join(placeholders, ",") + `)`
		}
	}
	query += ` ORDER BY ar.started_at DESC, ar.id DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	runs := make([]AuditRun, 0, limit)
	for rows.Next() {
		run, err := auditRunFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(runs) > limit
	if more {
		runs = runs[:limit]
	}
	return runs, more, nil
}

func validAuditTerminalStatus(value string) bool {
	switch value {
	case "complete", "partial", "failed":
		return true
	default:
		return false
	}
}

// FinalizeAuditRun closes a run with one of the terminal statuses. The
// variadic form accepts both the current (id, status, actorID) call shape and
// the older (id, actorID) shape, defaulting the latter to complete. It is
// idempotent only when a repeated finalize requests the same terminal status.
func (s *Store) FinalizeAuditRun(ctx context.Context, id string, args ...string) (AuditRun, error) {
	requestedStatus := "complete"
	if len(args) > 0 && validAuditTerminalStatus(strings.TrimSpace(args[0])) {
		requestedStatus = strings.TrimSpace(args[0])
	}
	if !validAuditTerminalStatus(requestedStatus) {
		return AuditRun{}, invalid("audit status must be complete, partial, or failed", nil)
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := s.RequireExternalAudit(ctx, tx, id); err != nil {
			return err
		}
		timestamp := now()
		result, err := tx.ExecContext(ctx, `UPDATE audit_runs SET status=?, finalized_at=?, updated_at=? WHERE id=? AND status IN ('queued', 'running')`, requestedStatus, timestamp, timestamp, id)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 0 {
			return nil
		}
		var currentStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM audit_runs WHERE id=?`, id).Scan(&currentStatus); errors.Is(err, sql.ErrNoRows) {
			return notFound("audit run not found")
		} else if err != nil {
			return err
		}
		if currentStatus != requestedStatus {
			return conflict("audit run has already been finalized with a different status", nil)
		}
		return nil
	})
	if err != nil {
		return AuditRun{}, err
	}
	return s.getAuditRunSummary(ctx, id)
}

// FinalizeAudit is a compatibility alias for resource-oriented callers.
func (s *Store) FinalizeAudit(ctx context.Context, id string, actorID ...string) (AuditRun, error) {
	return s.FinalizeAuditRun(ctx, id, actorID...)
}
