package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func auditFindingFromRow(scanner interface{ Scan(...any) error }) (AuditFinding, error) {
	var finding AuditFinding
	var destination sql.NullString
	var refsJSON string
	if err := scanner.Scan(&finding.ID, &finding.AuditID, &finding.TaskID, &finding.CapturedVersion, &finding.SourceColumn, &finding.Verdict, &destination, &finding.Confidence, &finding.Reason, &refsJSON, &finding.ReviewState, &finding.Version, &finding.CreatedAt, &finding.UpdatedAt); err != nil {
		return AuditFinding{}, err
	}
	if destination.Valid {
		finding.ProposedSemanticDestination = &destination.String
	}
	if err := json.Unmarshal([]byte(refsJSON), &finding.EvidenceRefs); err != nil {
		return AuditFinding{}, fmt.Errorf("decode audit evidence_refs: %w", err)
	}
	if finding.EvidenceRefs == nil {
		finding.EvidenceRefs = []string{}
	}
	finding.SourceColumnID = finding.SourceColumn
	return finding, nil
}

const auditFindingColumns = `af.id, af.audit_id, af.task_id, af.captured_version,
	af.source_column, af.verdict, af.proposed_semantic_destination, af.confidence,
	af.reason, af.evidence_refs, af.review_state, af.version, af.created_at, af.updated_at`

// AppendAuditFinding appends one immutable finding while the run is queued or
// running.
// Repeating the exact finding for the same task is idempotent; changing any
// snapshot or review field for an existing task returns a conflict. The task
// itself is never updated, and no event/comment/claim row is written.
func (s *Store) AppendAuditFinding(ctx context.Context, auditID string, input AuditFindingInput) (AuditFinding, error) {
	validated, err := validateAuditFindingInput(input)
	if err != nil {
		return AuditFinding{}, err
	}
	var findingID string
	var existing bool
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// This guarded no-op UPDATE acquires SQLite's writer lock before checking
		// status, preventing a concurrent finalize from accepting a late finding.
		result, err := tx.ExecContext(ctx, `UPDATE audit_runs SET updated_at=updated_at WHERE id=? AND status IN ('queued', 'running')`, auditID)
		if err != nil {
			return err
		}
		if changed, rowsErr := result.RowsAffected(); rowsErr != nil {
			return rowsErr
		} else if changed == 0 {
			var status string
			err = tx.QueryRowContext(ctx, `SELECT status FROM audit_runs WHERE id=?`, auditID).Scan(&status)
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("audit run not found")
			}
			if err != nil {
				return err
			}
			return conflict("audit run is finalized", nil)
		}

		var existingFinding AuditFinding
		existingFinding, err = auditFindingFromRow(tx.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.audit_id=? AND af.task_id=?`, auditID, validated.TaskID))
		if err == nil {
			if !auditFindingMatchesInput(existingFinding, validated) {
				return conflict("audit finding is immutable", map[string]any{"finding_id": existingFinding.ID})
			}
			findingID, existing = existingFinding.ID, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var projectID string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM audit_runs WHERE id=?`, auditID).Scan(&projectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("audit run not found")
			}
			return err
		}
		var snapshot auditTaskSnapshot
		err = tx.QueryRowContext(ctx, `SELECT t.version, t.column_id, c.name, c.semantic_state FROM tasks t JOIN columns c ON c.id=t.column_id WHERE t.id=? AND t.project_id=? AND t.deleted_at IS NULL`, validated.TaskID, projectID).Scan(&snapshot.Version, &snapshot.ColumnID, &snapshot.ColumnName, &snapshot.SemanticState)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("task not found in audit project")
		}
		if err != nil {
			return err
		}
		if validated.CapturedVersion > snapshot.Version {
			return invalid("captured_version cannot be newer than the task", nil)
		}
		// Source columns are opaque IDs. A finding may legitimately be stale
		// because the task moved after capture, but the captured ID must have
		// belonged to this project when appended.
		if !auditSourceColumnMatches(validated.SourceColumn, snapshot) {
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM columns WHERE project_id=? AND id=?`, projectID, validated.SourceColumn).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				return invalid("source_column must be a column ID in the audit project", nil)
			}
		}

		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM audit_findings WHERE audit_id=?`, auditID).Scan(&count); err != nil {
			return err
		}
		if count >= maxAuditRunFindings {
			return invalid("audit run contains too many findings", nil)
		}
		refsJSON, err := json.Marshal(validated.EvidenceRefs)
		if err != nil {
			return err
		}
		findingID = newID()
		timestamp := now()
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_findings(id, audit_id, task_id, captured_version, source_column, verdict, proposed_semantic_destination, confidence, reason, evidence_refs, review_state, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, findingID, auditID, validated.TaskID, validated.CapturedVersion, validated.SourceColumn, validated.Verdict, nullableAuditDestinationArg(validated.ProposedSemanticDestination), validated.Confidence, validated.Reason, string(refsJSON), validated.ReviewState, timestamp, timestamp)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return conflict("audit finding already exists", nil)
			}
			return err
		}
		return nil
	})
	if err != nil {
		return AuditFinding{}, err
	}
	if existing {
		return s.getAuditFinding(ctx, auditID, findingID)
	}
	return s.getAuditFinding(ctx, auditID, findingID)
}

func nullableAuditDestinationArg(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func auditFindingMatchesInput(finding AuditFinding, input AuditFindingInput) bool {
	if finding.TaskID != input.TaskID || finding.CapturedVersion != input.CapturedVersion || finding.SourceColumn != input.SourceColumn || finding.Verdict != input.Verdict || finding.Confidence != input.Confidence || finding.Reason != input.Reason || finding.ReviewState != input.ReviewState {
		return false
	}
	if (finding.ProposedSemanticDestination == nil) != (input.ProposedSemanticDestination == nil) {
		return false
	}
	if finding.ProposedSemanticDestination != nil && *finding.ProposedSemanticDestination != *input.ProposedSemanticDestination {
		return false
	}
	if len(finding.EvidenceRefs) != len(input.EvidenceRefs) {
		return false
	}
	for i := range finding.EvidenceRefs {
		if finding.EvidenceRefs[i] != input.EvidenceRefs[i] {
			return false
		}
	}
	return true
}

func (s *Store) getAuditFinding(ctx context.Context, auditID, findingID string) (AuditFinding, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.audit_id=? AND af.id=?`, auditID, findingID)
	finding, err := auditFindingFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditFinding{}, notFound("audit finding not found")
	}
	if err != nil {
		return AuditFinding{}, err
	}
	if err := s.populateAuditFindingDrift(ctx, &finding); err != nil {
		return AuditFinding{}, err
	}
	return finding, nil
}

// GetAuditFinding fetches a finding by its opaque ID and calculates drift at
// read time. The associated run/project are intentionally available to HTTP
// callers through GetAuditFindingProject so a token ceiling can be enforced
// before exposing the finding.
func (s *Store) GetAuditFinding(ctx context.Context, findingID string) (AuditFinding, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.id=?`, findingID)
	finding, err := auditFindingFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditFinding{}, notFound("audit finding not found")
	}
	if err != nil {
		return AuditFinding{}, err
	}
	if err := s.populateAuditFindingDrift(ctx, &finding); err != nil {
		return AuditFinding{}, err
	}
	return finding, nil
}

// GetAuditFindingProject returns the project owning a finding's run. It is a
// narrow authorization helper and does not expose task metadata.
func (s *Store) GetAuditFindingProject(ctx context.Context, findingID string) (string, error) {
	var projectID string
	err := s.DB.QueryRowContext(ctx, `SELECT ar.project_id FROM audit_findings af JOIN audit_runs ar ON ar.id=af.audit_id WHERE af.id=?`, findingID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound("audit finding not found")
	}
	return projectID, err
}

// ListAuditFindings returns one cursor page and whether another row follows.
func (s *Store) ListAuditFindings(ctx context.Context, auditID string, limit, offset int) ([]AuditFinding, bool, error) {
	if _, err := s.getAuditRunSummary(ctx, auditID); err != nil {
		return nil, false, err
	}
	return s.listAuditFindings(ctx, auditID, offset, limit, true)
}

func (s *Store) listAuditFindings(ctx context.Context, auditID string, offset, limit int, sentinel bool) ([]AuditFinding, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	fetch := limit
	if sentinel {
		fetch++
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.audit_id=? ORDER BY af.created_at, af.id LIMIT ? OFFSET ?`, auditID, fetch, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	findings := make([]AuditFinding, 0, limit)
	for rows.Next() {
		finding, err := auditFindingFromRow(rows)
		if err != nil {
			return nil, false, err
		}
		if err := s.populateAuditFindingDrift(ctx, &finding); err != nil {
			return nil, false, err
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := sentinel && len(findings) > limit
	if more {
		findings = findings[:limit]
	}
	return findings, more, nil
}

func (s *Store) populateAuditFindingDrift(ctx context.Context, finding *AuditFinding) error {
	var snapshot auditTaskSnapshot
	err := s.DB.QueryRowContext(ctx, `SELECT t.version, t.column_id, c.name, c.semantic_state FROM tasks t JOIN columns c ON c.id=t.column_id WHERE t.id=?`, finding.TaskID).Scan(&snapshot.Version, &snapshot.ColumnID, &snapshot.ColumnName, &snapshot.SemanticState)
	if errors.Is(err, sql.ErrNoRows) {
		finding.ChangedSinceAudit = true
		return nil
	}
	if err != nil {
		return err
	}
	finding.ChangedSinceAudit = snapshot.Version != finding.CapturedVersion || !auditSourceColumnMatches(finding.SourceColumn, snapshot)
	return nil
}

func auditSourceColumnMatches(source string, snapshot auditTaskSnapshot) bool {
	return source == snapshot.ColumnID
}
