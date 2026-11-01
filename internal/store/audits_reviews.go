package store

import (
	"context"
	"database/sql"
	"errors"
)

// UpdateAuditFindingReview changes only review_state and, when explicitly
// supplied, the proposed destination. It uses the finding version as a
// strong optimistic-concurrency validator and does not alter the captured
// task snapshot or any task-side table.
func (s *Store) UpdateAuditFindingReview(ctx context.Context, findingID string, input AuditFindingReviewInput, expectedVersion int64, _ ...string) (AuditFinding, error) {
	if expectedVersion <= 0 {
		return AuditFinding{}, ErrPrecondition
	}
	validated, err := validateAuditFindingReviewInput(input)
	if err != nil {
		return AuditFinding{}, err
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var currentDestination sql.NullString
		var currentVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT version, proposed_semantic_destination FROM audit_findings WHERE id=?`, findingID).Scan(&currentVersion, &currentDestination); errors.Is(err, sql.ErrNoRows) {
			return notFound("audit finding not found")
		} else if err != nil {
			return err
		}
		destination := any(nil)
		if validated.ProposedSemanticDestinationSet {
			destination = nullableAuditDestinationArg(validated.ProposedSemanticDestination)
		} else if currentDestination.Valid {
			destination = currentDestination.String
		}
		query := `UPDATE audit_findings SET review_state=?, proposed_semantic_destination=?, version=version+1, updated_at=? WHERE id=? AND version=?`
		if validated.ReviewState == "approved" {
			// Approval is a selection boundary for later reconciliation. The agent
			// must first finish the run, and the task snapshot must still match.
			// Guard both facts in the same writer statement so a queued/in-flight
			// or stale finding can never become newly approved.
			query += ` AND EXISTS (
				SELECT 1 FROM audit_runs ar
				WHERE ar.id=audit_findings.audit_id
					AND ar.status IN ('complete', 'partial')
			) AND EXISTS (
				SELECT 1 FROM tasks t
				WHERE t.id=audit_findings.task_id
					AND t.deleted_at IS NULL
					AND t.version=audit_findings.captured_version
					AND t.column_id=audit_findings.source_column
			)`
		}
		result, err := tx.ExecContext(ctx, query, validated.ReviewState, destination, now(), findingID, expectedVersion)
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
		latest, err := auditFindingFromRow(tx.QueryRowContext(ctx, `SELECT `+auditFindingColumns+` FROM audit_findings af WHERE af.id=?`, findingID))
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("audit finding not found")
		}
		if err != nil {
			return err
		}
		if latest.Version == expectedVersion && validated.ReviewState == "approved" {
			var runStatus string
			if err := tx.QueryRowContext(ctx, `SELECT status FROM audit_runs WHERE id=?`, latest.AuditID).Scan(&runStatus); err != nil {
				return err
			}
			if runStatus != "complete" && runStatus != "partial" {
				return conflict("audit run must be complete or partial before approval", map[string]any{"current": latest})
			}
			latest.ChangedSinceAudit = true
			return conflict("audit finding task has changed since capture", map[string]any{"current": latest})
		}
		return conflict("audit finding has changed", map[string]any{"current": latest})
	})
	if err != nil {
		return AuditFinding{}, err
	}
	return s.GetAuditFinding(ctx, findingID)
}

// ReviewAuditFinding is a compatibility alias for callers that use the
// action-oriented name.
func (s *Store) ReviewAuditFinding(ctx context.Context, findingID string, input AuditFindingReviewInput, expectedVersion int64, actorID ...string) (AuditFinding, error) {
	return s.UpdateAuditFindingReview(ctx, findingID, input, expectedVersion, actorID...)
}
