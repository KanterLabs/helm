package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Migration 043 (neglect signal, docs/FOCUS_NEGLECT_SIGNAL_PLAN.md) must be
// additive: it creates two empty derived tables and changes nothing else. A
// retained pre-043 binary keeps writing tasks, events and comments while the
// tables stay unused, and a rollback leaves them in place.
const taskActivitySignalMigration = 43

func populateTaskActivitySignalHistory(t *testing.T, ctx context.Context, database *sql.DB, tasks, eventsPerTask int) {
	t.Helper()
	exec := func(query string, args ...any) {
		if _, err := database.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("populate history: %v\n%s", err, query)
		}
	}
	for task := 0; task < tasks; task++ {
		id := fmt.Sprintf("history-%03d", task)
		at := fmt.Sprintf("2026-02-%02dT00:00:00Z", 1+task%27)
		exec(`INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, version, created_at, updated_at) VALUES (?, 'project', ?, 'todo', 'task', 'History', '', 'normal', ?, 1, ?, ?)`, id, 100+task, float64(task), at, at)
		for event := 0; event < eventsPerTask; event++ {
			payload := `{"changed_fields":["title"]}`
			if event%3 == 0 {
				payload = `{"version":2}` // legacy task.updated, no changed_fields
			}
			exec(`INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, 'task.updated', 'owner', 'project', ?, ?, ?)`, fmt.Sprintf("%s-event-%02d", id, event), id, payload, at)
		}
		exec(`INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES (?, ?, 'owner', 'History comment', ?, ?)`, id+"-comment", id, at, at)
	}
}

func taskActivitySignalCounts(t *testing.T, ctx context.Context, database *sql.DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"tasks", "events", "comments", "task_agent_work_history", "schema_migrations"} {
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func TestTaskActivitySignalMigrationIsAdditiveOnPopulatedDatabase(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < taskActivitySignalMigration {
		t.Fatalf("migration %03d is not present", taskActivitySignalMigration)
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, taskActivitySignalMigration-1)
	populateProductionFixture(t, ctx, database, taskActivitySignalMigration-1)
	populateTaskActivitySignalHistory(t, ctx, database, 60, 25)
	before := taskActivitySignalCounts(t, ctx, database)
	beforeStable := readStableFixture(t, ctx, database, taskActivitySignalMigration-1)

	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-%03d database: %v", taskActivitySignalMigration, err)
	}
	after := taskActivitySignalCounts(t, ctx, database)
	for _, table := range []string{"tasks", "events", "comments", "task_agent_work_history"} {
		if before[table] != after[table] {
			t.Fatalf("%s count changed by migration: %d -> %d", table, before[table], after[table])
		}
	}
	if after["tasks"] != 62 || after["events"] != 1+60*25 || after["comments"] != 61 {
		t.Fatalf("populated counts = %#v", after)
	}
	assertStableFixtureMigration(t, beforeStable, readStableFixture(t, ctx, database, taskActivitySignalMigration))

	// The migration only creates empty tables; the projector (TC-379) fills them.
	for _, table := range []string{"task_activity_signal", "task_activity_signal_projection"} {
		var rows int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
			t.Fatalf("migration did not create %s: %v", table, err)
		}
		if rows != 0 {
			t.Fatalf("%s has %d rows after migration, want 0", table, rows)
		}
	}

	exec := func(query string, args ...any) error {
		_, err := database.ExecContext(ctx, query, args...)
		return err
	}
	// D2 values are accepted; anything else is rejected by the schema.
	for _, status := range []string{"known", "bounded", "unknown"} {
		if err := exec(`INSERT OR REPLACE INTO task_activity_signal(task_id, status, last_meaningful_at, source, not_after, reason, classification_version, computed_at) VALUES ('history-001', ?, NULL, '', NULL, '', 2, '2026-03-01T00:00:00Z')`, status); err != nil {
			t.Fatalf("status %q rejected: %v", status, err)
		}
	}
	if err := exec(`INSERT OR REPLACE INTO task_activity_signal(task_id, status, classification_version, computed_at) VALUES ('history-001', 'pending', 2, '2026-03-01T00:00:00Z')`); err == nil {
		t.Fatal("transport-only status pending was stored")
	}
	if err := exec(`INSERT INTO task_activity_signal(task_id, status, classification_version, computed_at) VALUES ('no-such-task', 'known', 2, '2026-03-01T00:00:00Z')`); err == nil {
		t.Fatal("signal row for a missing task was accepted")
	}
	if err := exec(`INSERT INTO task_activity_signal_projection(id, updated_at) VALUES (1, '2026-03-01T00:00:00Z')`); err != nil {
		t.Fatalf("projection row rejected: %v", err)
	}
	if err := exec(`INSERT INTO task_activity_signal_projection(id, updated_at) VALUES (2, '2026-03-01T00:00:00Z')`); err == nil {
		t.Fatal("projection accepted a second row")
	}
	var cursor, seeded int
	if err := database.QueryRowContext(ctx, `SELECT event_cursor, seeded_version FROM task_activity_signal_projection WHERE id=1`).Scan(&cursor, &seeded); err != nil || cursor != 0 || seeded != 0 {
		t.Fatalf("projection defaults = %d/%d, %v; want 0/0", cursor, seeded, err)
	}

	// A retained pre-043 binary keeps writing history and may hard-delete; the
	// derived row follows the task through ON DELETE CASCADE.
	if err := exec(`INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES ('retained-event', 'task.updated', 'owner', 'project', 'history-002', '{"version":3}', '2026-03-02T00:00:00Z')`); err != nil {
		t.Fatalf("retained event write: %v", err)
	}
	if err := exec(`INSERT INTO comments(id, task_id, actor_id, body, created_at, updated_at) VALUES ('retained-comment', 'history-002', 'owner', 'Retained', '2026-03-02T00:00:00Z', '2026-03-02T00:00:00Z')`); err != nil {
		t.Fatalf("retained comment write: %v", err)
	}
	if err := exec(`UPDATE tasks SET title='Retained write', version=version+1 WHERE id='task-1'`); err != nil {
		t.Fatalf("retained task write: %v", err)
	}
	if err := exec(`DELETE FROM events WHERE task_id='history-001'`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM comments WHERE task_id='history-001'`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM tasks WHERE id='history-001'`); err != nil {
		t.Fatalf("hard delete task: %v", err)
	}
	var orphans int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_activity_signal WHERE task_id='history-001'`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("signal rows after hard delete = %d, %v; want 0", orphans, err)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("migration %03d integrity: %v", taskActivitySignalMigration, err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	if got := taskActivitySignalCounts(t, ctx, database)["schema_migrations"]; got != after["schema_migrations"] {
		t.Fatalf("rerun recorded %d migrations, want %d", got, after["schema_migrations"])
	}
}
