package db

import (
	"context"
	"strings"
	"testing"
)

func TestSchema10AuditDDLIntegrityAndConstraintRejections(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 10 {
		t.Skip("migration 010 is not present")
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, len(migrations))
	populateProductionFixture(t, ctx, database, 10)

	for _, table := range []string{"audit_runs", "audit_findings"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("audit table %q count = %d, want 1", table, count)
		}
	}
	for _, index := range []string{
		"audit_runs_project_started_idx",
		"audit_runs_actor_started_idx",
		"audit_findings_audit_created_idx",
		"audit_findings_task_idx",
		"audit_findings_verdict_idx",
	} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("audit index %q count = %d, want 1", index, count)
		}
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("populated audit schema integrity: %v", err)
	}

	if _, err := database.ExecContext(ctx, `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES ('audit-default', 'project', 'owner', 'default values', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert audit run with defaults: %v", err)
	}
	var status string
	if err := database.QueryRowContext(ctx, `SELECT status FROM audit_runs WHERE id='audit-default'`).Scan(&status); err != nil || status != "running" {
		t.Fatalf("audit run default status = %q, %v; want running", status, err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO audit_findings(id, audit_id, task_id, captured_version, source_column, verdict, confidence, reason, created_at, updated_at) VALUES ('finding-default', 'audit-default', 'task-1', 3, 'todo', 'correct', 1, 'No issue', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert audit finding with defaults: %v", err)
	}
	var evidenceRefs, reviewState string
	var findingVersion int
	if err := database.QueryRowContext(ctx, `SELECT evidence_refs, review_state, version FROM audit_findings WHERE id='finding-default'`).Scan(&evidenceRefs, &reviewState, &findingVersion); err != nil {
		t.Fatal(err)
	}
	if evidenceRefs != "[]" || reviewState != "pending" || findingVersion != 1 {
		t.Fatalf("audit finding defaults = %q/%q/%d; want []/pending/1", evidenceRefs, reviewState, findingVersion)
	}

	auditRunCases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "project foreign key",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES (?, 'missing-project', 'owner', 'scope', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-project"},
		},
		{
			name:  "actor foreign key",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES (?, 'project', 'missing-actor', 'scope', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-actor"},
		},
		{
			name:  "empty scope",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES (?, 'project', 'owner', ?, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-scope-empty", "  "},
		},
		{
			name:  "scope too long",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES (?, 'project', 'owner', ?, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-scope-long", strings.Repeat("s", 201)},
		},
		{
			name:  "status enum",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, status, started_at, created_at, updated_at) VALUES (?, 'project', 'owner', 'scope', 'unknown', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-status"},
		},
		{
			name:  "finalized running run",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, finalized_at, started_at, created_at, updated_at) VALUES (?, 'project', 'owner', 'scope', '2026-01-02T01:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-finalized"},
		},
		{
			name:  "terminal run missing finalized timestamp",
			query: `INSERT INTO audit_runs(id, project_id, actor_id, scope, status, started_at, created_at, updated_at) VALUES (?, 'project', 'owner', 'scope', 'complete', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
			args:  []any{"audit-bad-terminal-timestamp"},
		},
	}
	for _, test := range auditRunCases {
		t.Run("audit_runs/"+test.name, func(t *testing.T) {
			if _, err := database.ExecContext(ctx, test.query, test.args...); err == nil {
				t.Fatalf("invalid audit run unexpectedly succeeded")
			}
		})
	}

	longSourceColumn := strings.Repeat("c", 201)
	longReason := strings.Repeat("r", 2001)
	longReviewState := strings.Repeat("v", 33)
	auditFindingCases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "audit foreign key",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-audit", "missing-audit", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "task foreign key",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-task", "audit-1", "missing-task", 3, "todo", "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "captured version positive",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-captured-version", "audit-1", "task-1", 0, "todo", "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "source column nonempty",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-source-empty", "audit-1", "task-1", 3, " ", "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "source column length",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-source-long", "audit-1", "task-1", 3, longSourceColumn, "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "verdict enum",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-verdict", "audit-1", "task-1", 3, "todo", "wrong", nil, 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "destination enum",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-destination", "audit-1", "task-1", 3, "todo", "move_proposed", "invalid", 0.5, "reason", "[]", "pending", 1},
		},
		{
			name:  "confidence lower bound",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-confidence-low", "audit-1", "task-1", 3, "todo", "correct", nil, -0.01, "reason", "[]", "pending", 1},
		},
		{
			name:  "confidence upper bound",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-confidence-high", "audit-1", "task-1", 3, "todo", "correct", nil, 1.01, "reason", "[]", "pending", 1},
		},
		{
			name:  "reason nonempty",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-reason-empty", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, " ", "[]", "pending", 1},
		},
		{
			name:  "reason length",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-reason-long", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, longReason, "[]", "pending", 1},
		},
		{
			name:  "evidence refs JSON",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-evidence-json", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "not-json", "pending", 1},
		},
		{
			name:  "evidence refs array",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-evidence-object", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "{}", "pending", 1},
		},
		{
			name:  "review state nonempty",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-review-empty", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", " ", 1},
		},
		{
			name:  "review state length",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-review-long", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", longReviewState, 1},
		},
		{
			name:  "review state enum",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-review-enum", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", "reviewed", 1},
		},
		{
			name:  "finding version positive",
			query: validAuditFindingInsert,
			args:  []any{"finding-bad-version", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", "pending", 0},
		},
		{
			name:  "audit task unique",
			query: validAuditFindingInsert,
			args:  []any{"finding-duplicate", "audit-1", "task-1", 3, "todo", "correct", nil, 0.5, "reason", "[]", "pending", 1},
		},
	}
	for _, test := range auditFindingCases {
		t.Run("audit_findings/"+test.name, func(t *testing.T) {
			if _, err := database.ExecContext(ctx, test.query, test.args...); err == nil {
				t.Fatalf("invalid audit finding unexpectedly succeeded")
			}
		})
	}

	if _, err := database.ExecContext(ctx, `DELETE FROM audit_runs WHERE id='audit-default'`); err != nil {
		t.Fatalf("delete audit run for cascade test: %v", err)
	}
	var findings int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_findings WHERE audit_id='audit-default'`).Scan(&findings); err != nil {
		t.Fatal(err)
	}
	if findings != 0 {
		t.Fatalf("audit finding cascade count = %d, want 0", findings)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO actors(id, kind, name, email, password_hash, admin, created_at, updated_at) VALUES ('audit-actor', 'agent', 'Audit actor', 'audit-actor@example.test', 'hash', 0, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO audit_runs(id, project_id, actor_id, scope, started_at, created_at, updated_at) VALUES ('audit-restrict-actor', 'project', 'audit-actor', 'scope', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM actors WHERE id='audit-actor'`); err == nil {
		t.Fatal("actor referenced by audit run was deleted")
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, version, created_at, updated_at) VALUES ('audit-task', 'project', 99, 'todo', 'task', 'Audit task', '', 'normal', 99, 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, validAuditFindingInsert, "finding-restrict-task", "audit-1", "audit-task", 1, "todo", "correct", nil, 1.0, "retained", "[]", "pending", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM tasks WHERE id='audit-task'`); err == nil {
		t.Fatal("task referenced by audit finding was deleted")
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("audit schema integrity after constraint checks: %v", err)
	}
}

const validAuditFindingInsert = `INSERT INTO audit_findings(id, audit_id, task_id, captured_version, source_column, verdict, proposed_semantic_destination, confidence, reason, evidence_refs, review_state, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`
