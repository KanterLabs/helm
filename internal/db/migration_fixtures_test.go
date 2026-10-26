package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

type stableFixture struct {
	stable        map[string]string
	counts        map[string]int
	columns       []migrationColumnSnapshot
	schemaVersion int
	projectIDs    []string
}

type auditFixture struct {
	runs     string
	findings string
}

func newRawDatabase(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "roadmap.db")
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func applyMigrationPrefix(t *testing.T, ctx context.Context, database *sql.DB, migrations []embeddedMigration, prefix int) {
	t.Helper()
	for _, migration := range migrations[:prefix] {
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(migration.contents)); err != nil {
			tx.Rollback()
			t.Fatalf("apply prefix migration %d: %v", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, 'fixture')`, migration.version); err != nil {
			tx.Rollback()
			t.Fatalf("record prefix migration %d: %v", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func populateProductionFixture(t *testing.T, ctx context.Context, database *sql.DB, prefix int) {
	t.Helper()
	exec := func(query string, args ...any) {
		if _, err := database.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("populate fixture: %v\n%s", err, query)
		}
	}
	exec(`INSERT INTO actors(id, kind, name, email, password_hash, admin, created_at, updated_at) VALUES ('owner', 'human', 'Owner', 'owner@example.test', 'hash-owner', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO actors(id, kind, name, email, password_hash, admin, created_at, updated_at) VALUES ('agent', 'agent', 'Agent', 'agent@example.test', 'hash-agent', 0, '2026-01-01T00:00:01Z', '2026-01-01T00:00:01Z')`)
	if prefix >= 5 {
		exec(`UPDATE actors SET description=CASE id WHEN 'owner' THEN 'owner description' ELSE 'agent description' END`)
	}
	exec(`INSERT INTO projects(id, key, slug, name, description, color, favorite, created_at, updated_at) VALUES ('project', 'ROAD', 'roadmap', 'Roadmap', 'Project', '#123456', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('todo', 'project', 'Todo', 'backlog', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('doing', 'project', 'Doing', 'active', 2, '2026-01-01T00:00:01Z', '2026-01-01T00:00:01Z')`)
	if prefix >= 8 {
		exec(`INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, claimed_by, claim_expires_at, version, created_at, updated_at) VALUES ('task-1', 'project', 1, 'todo', 'task', 'Task one', 'Keep this', 'high', 1.5, 'agent', '2027-01-01T00:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
		exec(`INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, version, created_at, updated_at) VALUES ('bug-1', 'project', 2, 'doing', 'bug', 'Bug one', 'Bug details', 'urgent', 2.5, 2, '2026-01-01T00:00:01Z', '2026-01-01T00:00:01Z')`)
	} else {
		exec(`INSERT INTO tasks(id, project_id, number, column_id, title, description, priority, position, claimed_by, claim_expires_at, version, created_at, updated_at) VALUES ('task-1', 'project', 1, 'todo', 'Task one', 'Keep this', 'high', 1.5, 'agent', '2027-01-01T00:00:00Z', 3, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
		exec(`INSERT INTO tasks(id, project_id, number, column_id, title, description, priority, position, version, created_at, updated_at) VALUES ('bug-1', 'project', 2, 'doing', 'Bug one', 'Bug details', 'urgent', 2.5, 2, '2026-01-01T00:00:01Z', '2026-01-01T00:00:01Z')`)
	}
	if prefix >= 8 {
		exec(`INSERT INTO bug_details(task_id, reporter_id, severity, actual_behavior) VALUES ('bug-1', 'owner', 's2', 'It fails')`)
	}
	exec(`INSERT INTO labels(id, project_id, name, color, created_at, updated_at) VALUES ('label-1', 'project', 'Important', '#ff0000', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO task_labels(task_id, label_id) VALUES ('task-1', 'label-1')`)
	exec(`INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES ('comment-1', 'task-1', 'owner', 'A stable comment', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES ('event-1', 'task.created', 'owner', 'project', 'task-1', '{"stable":true}', '2026-01-01T00:00:00Z')`)
	exec(`INSERT INTO idempotency_keys(actor_id, key, method, path, request_hash, status, response_body, created_at) VALUES ('owner', 'fixture-key', 'POST', '/api/v1/tasks', 'request-hash', 201, '{"id":"task-1"}', '2026-01-01T00:00:00Z')`)
	if prefix >= 4 {
		exec(`INSERT INTO actor_projects(actor_id, project_id) VALUES ('agent', 'project')`)
	}
	if prefix >= 6 {
		exec(`INSERT INTO actor_resource_usage(actor_id, reserved_bytes, updated_at) VALUES ('agent', 42, '2026-01-01T00:00:00Z')`)
	}
	if prefix >= 7 {
		exec(`UPDATE idempotency_keys SET response_location='/api/v1/tasks/task-1' WHERE actor_id='owner' AND key='fixture-key'`)
	}
	if prefix >= 3 {
		exec(`INSERT OR REPLACE INTO project_counters(project_id, next_number) VALUES ('project', 3)`)
	}
	if prefix >= 10 {
		populateAuditFixture(t, ctx, database)
	}
	if prefix >= 15 {
		exec(`INSERT INTO task_checklist_items(id, task_id, text, position, completed, created_at, updated_at) VALUES ('checklist-1', 'task-1', 'Preserve this acceptance criterion', 0, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`)
	}
	if prefix >= 26 {
		exec(`INSERT INTO luna_runs(id,actor_id,project_id,project_key,feature,outcome,model,effort,thread_id,turn_id,duration_ms,output_bytes,detail,started_at,completed_at) VALUES ('luna-run-1','owner','project','ROAD','task_draft','succeeded','gpt-5.6-luna','medium','thread-1','turn-1',120,64,'validated','2026-01-01T00:00:00Z','2026-01-01T00:00:00.120Z')`)
	}
}

func populateAuditFixture(t *testing.T, ctx context.Context, database *sql.DB) {
	t.Helper()
	if _, err := database.ExecContext(ctx, `INSERT INTO audit_runs(id, project_id, actor_id, scope, status, started_at, finalized_at, created_at, updated_at) VALUES ('audit-1', 'project', 'owner', 'board', 'complete', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z')`); err != nil {
		t.Fatalf("populate audit run: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO audit_findings(id, audit_id, task_id, captured_version, source_column, verdict, proposed_semantic_destination, confidence, reason, evidence_refs, review_state, version, created_at, updated_at) VALUES ('finding-1', 'audit-1', 'task-1', 3, 'todo', 'move_proposed', 'active', 0.87, 'Task belongs in active work', '["event-1"]', 'pending', 2, '2026-01-01T00:01:00Z', '2026-01-01T00:01:00Z')`); err != nil {
		t.Fatalf("populate audit finding: %v", err)
	}
}

func readStableFixture(t *testing.T, ctx context.Context, database *sql.DB, schemaVersion int) stableFixture {
	t.Helper()
	stable := map[string]string{}
	queries := map[string]string{
		"actors":      `SELECT group_concat(id || ':' || kind || ':' || name || ':' || COALESCE(email,'') || ':' || COALESCE(password_hash,''), '|') FROM (SELECT id, kind, name, email, password_hash FROM actors ORDER BY id)`,
		"projects":    `SELECT id || ':' || key || ':' || slug || ':' || name FROM projects ORDER BY id`,
		"tasks":       `SELECT group_concat(id || ':' || number || ':' || project_id || ':' || column_id || ':' || position || ':' || COALESCE(claimed_by,''), '|') FROM (SELECT id, number, project_id, column_id, position, claimed_by FROM tasks ORDER BY number, id)`,
		"comments":    `SELECT id || ':' || task_id || ':' || actor_id || ':' || body FROM comments ORDER BY id`,
		"events":      `SELECT id || ':' || type || ':' || COALESCE(actor_id,'') || ':' || COALESCE(task_id,'') || ':' || payload FROM events ORDER BY cursor`,
		"idempotency": `SELECT actor_id || ':' || key || ':' || response_body FROM idempotency_keys ORDER BY actor_id, key`,
	}
	counts := map[string]int{}
	for name, query := range queries {
		var value sql.NullString
		if err := database.QueryRowContext(ctx, query).Scan(&value); err != nil {
			// Tables introduced after this prefix are intentionally absent until
			// Migrate creates them; they are represented as empty fixture values.
			stable[name] = "<absent>"
			continue
		}
		if value.Valid {
			stable[name] = value.String
		} else {
			stable[name] = ""
		}
	}
	for _, table := range []string{"actors", "projects", "columns", "tasks", "labels", "task_labels", "comments", "events", "idempotency_keys", "schema_migrations"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err == nil {
			counts[table] = count
		}
	}
	// Inspect the actual database because several retained-binary fixtures
	// label their original migration boundary after upgrading through latest.
	_ = schemaVersion
	var actualVersion int
	if err := database.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&actualVersion); err != nil {
		t.Fatalf("read fixture schema version: %v", err)
	}
	rows, err := database.QueryContext(ctx, `SELECT id FROM projects ORDER BY id`)
	if err != nil {
		t.Fatalf("read fixture project IDs: %v", err)
	}
	var projectIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan fixture project: %v", err)
		}
		projectIDs = append(projectIDs, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate fixture projects: %v", err)
	}
	rows.Close()
	return stableFixture{stable: stable, counts: counts, columns: readMigrationColumns(t, ctx, database, actualVersion), schemaVersion: actualVersion, projectIDs: projectIDs}
}

func readAuditFixture(t *testing.T, ctx context.Context, database *sql.DB) auditFixture {
	t.Helper()
	fixture := auditFixture{}
	queries := map[string]*string{
		"runs":     &fixture.runs,
		"findings": &fixture.findings,
	}
	statements := map[string]string{
		"runs":     `SELECT group_concat(value, '|') FROM (SELECT id || ':' || project_id || ':' || actor_id || ':' || scope || ':' || status || ':' || started_at || ':' || COALESCE(finalized_at, '') || ':' || created_at || ':' || updated_at AS value FROM audit_runs ORDER BY id)`,
		"findings": `SELECT group_concat(value, '|') FROM (SELECT id || ':' || audit_id || ':' || task_id || ':' || captured_version || ':' || source_column || ':' || verdict || ':' || COALESCE(proposed_semantic_destination, '') || ':' || confidence || ':' || reason || ':' || evidence_refs || ':' || review_state || ':' || version || ':' || created_at || ':' || updated_at AS value FROM audit_findings ORDER BY id)`,
	}
	for name, destination := range queries {
		var value sql.NullString
		if err := database.QueryRowContext(ctx, statements[name]).Scan(&value); err != nil {
			*destination = "<absent>"
			continue
		}
		if value.Valid {
			*destination = value.String
		}
	}
	return fixture
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func fileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
