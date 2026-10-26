package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

type contextExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func TestTaskDependencyMigrationPreservesPopulatedDataAndConstraints(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 12 {
		t.Skip("migration 012 is not present")
	}

	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, 11)
	populateProductionFixture(t, ctx, database, 11)
	before := readStableFixture(t, ctx, database, 11)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-012 database: %v", err)
	}
	latest, _, err := EmbeddedSchema()
	if err != nil {
		t.Fatalf("read latest schema: %v", err)
	}
	after := readStableFixture(t, ctx, database, latest)
	assertStableFixtureMigration(t, before, after)
	for _, table := range []string{"actors", "projects", "columns", "tasks", "task_links", "comments", "events", "idempotency_keys"} {
		expected := before.counts[table]
		if table == "columns" {
			expected++ // Migration 036 adds the project's Bugs column.
		}
		if got := after.counts[table]; got != expected {
			t.Fatalf("%s count changed during migration: before=%d after=%d", table, expected, got)
		}
	}
	var dependencyCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencyCount); err != nil {
		t.Fatal(err)
	}
	if dependencyCount != 0 {
		t.Fatalf("new dependency rows after additive migration = %d, want 0", dependencyCount)
	}
	var reverseIndexCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='task_dependencies_prerequisite_idx'`).Scan(&reverseIndexCount); err != nil {
		t.Fatal(err)
	}
	if reverseIndexCount != 1 {
		t.Fatalf("reverse dependency index count = %d, want 1", reverseIndexCount)
	}
	for _, trigger := range []string{
		"task_dependencies_validate_insert",
		"task_dependencies_guard_task_update",
		"task_dependencies_guard_column_update",
	} {
		var triggerCount int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?`, trigger).Scan(&triggerCount); err != nil {
			t.Fatal(err)
		}
		if triggerCount != 1 {
			t.Fatalf("dependency trigger %q count = %d, want 1", trigger, triggerCount)
		}
	}

	type foreignKey struct {
		table    string
		from     string
		to       string
		onUpdate string
		onDelete string
	}
	foreignKeys := make([]foreignKey, 0, 3)
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_list('task_dependencies')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, sequence int
		var key foreignKey
		var match string
		if err := rows.Scan(&id, &sequence, &key.table, &key.from, &key.to, &key.onUpdate, &key.onDelete, &match); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		foreignKeys = append(foreignKeys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(foreignKeys) != 3 {
		t.Fatalf("task dependency foreign keys = %#v, want 3 entries", foreignKeys)
	}
	wantForeignKeys := []foreignKey{
		{table: "tasks", from: "task_id", to: "id", onUpdate: "NO ACTION", onDelete: "CASCADE"},
		{table: "tasks", from: "prerequisite_task_id", to: "id", onUpdate: "NO ACTION", onDelete: "CASCADE"},
		{table: "actors", from: "created_by", to: "id", onUpdate: "NO ACTION", onDelete: "SET NULL"},
	}
	for _, want := range wantForeignKeys {
		found := false
		for _, got := range foreignKeys {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("foreign key %#v missing from %#v", want, foreignKeys)
		}
	}

	// task-1 starts with a fixture claim; release it before creating a valid
	// dependency so insertion exercises the graph guard rather than claim
	// policy.  No pre-existing data is changed by migration itself.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET claimed_by=NULL, claim_expires_at=NULL WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('task-1', 'bug-1', 'owner', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert valid dependency: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO projects(id, key, slug, name, created_at, updated_at) VALUES ('cross-project', 'CROSS', 'cross-project', 'Cross project', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('cross-todo', 'cross-project', 'Todo', 'backlog', 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, title, created_at, updated_at) VALUES ('cross-task', 'cross-project', 1, 'cross-todo', 'Cross project task', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, title, created_at, updated_at) VALUES ('deleted-task', 'project', 50, 'todo', 'Deleted task', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z'), ('fk-task-a', 'project', 51, 'todo', 'Foreign key source', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z'), ('fk-task-b', 'project', 52, 'todo', 'Foreign key prerequisite', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET deleted_at='2026-01-02T00:00:00Z' WHERE id='deleted-task'`); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencyCount); err != nil {
		t.Fatal(err)
	}
	if dependencyCount != 1 {
		t.Fatalf("dependency row count = %d, want 1", dependencyCount)
	}

	// Each failure is a single statement; the row count assertion after every
	// rejection proves that the trigger cannot leave a partial edge behind.
	invalidInserts := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "self reference",
			query: `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('fk-task-a', 'fk-task-a', 'owner', '2026-01-02T00:00:00Z')`,
		},
		{
			name:  "cross project",
			query: `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('task-1', 'cross-task', 'owner', '2026-01-02T00:00:00Z')`,
		},
		{
			name:  "deleted task",
			query: `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('task-1', 'deleted-task', 'owner', '2026-01-02T00:00:00Z')`,
		},
		{
			name:  "actor foreign key",
			query: `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('fk-task-a', 'fk-task-b', 'missing-actor', '2026-01-02T00:00:00Z')`,
		},
	}
	for _, test := range invalidInserts {
		t.Run(test.name, func(t *testing.T) {
			if _, err := database.ExecContext(ctx, test.query, test.args...); err == nil {
				t.Fatal("invalid dependency insert unexpectedly succeeded")
			}
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencyCount); err != nil {
				t.Fatal(err)
			}
			if dependencyCount != 1 {
				t.Fatalf("dependency rows after rejected insert = %d, want 1", dependencyCount)
			}
		})
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("populated dependency schema integrity: %v", err)
	}
}

func TestTaskDependencyInsertionLimitsAndCycles(t *testing.T) {
	ctx := context.Background()
	database := newRawDatabase(t)
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 12 {
		t.Skip("migration 012 is not present")
	}
	applyMigrationPrefix(t, ctx, database, migrations, len(migrations))
	populateProductionFixture(t, ctx, database, len(migrations))
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET claimed_by=NULL, claim_expires_at=NULL WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	insertTask := func(exec contextExecer, id string, number int) error {
		_, err := exec.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, version, created_at, updated_at) VALUES (?, 'project', ?, 'todo', 'task', ?, '', 'normal', ?, 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`, id, number, id, number)
		return err
	}
	insertDependency := func(exec contextExecer, taskID, prerequisiteID string) error {
		_, err := exec.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES (?, ?, 'owner', '2026-01-02T00:00:00Z')`, taskID, prerequisiteID)
		return err
	}

	firstLimitTx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertTask(firstLimitTx, "limit-source", 1000); err != nil {
		firstLimitTx.Rollback()
		t.Fatalf("insert limit source: %v", err)
	}
	for index := 0; index < 201; index++ {
		prerequisiteID := fmt.Sprintf("limit-prerequisite-%03d", index)
		if err := insertTask(firstLimitTx, prerequisiteID, 1100+index); err != nil {
			firstLimitTx.Rollback()
			t.Fatalf("insert %s: %v", prerequisiteID, err)
		}
		if index < 200 {
			if err := insertDependency(firstLimitTx, "limit-source", prerequisiteID); err != nil {
				firstLimitTx.Rollback()
				t.Fatalf("insert prerequisite %d: %v", index, err)
			}
		} else if err := insertDependency(firstLimitTx, "limit-source", prerequisiteID); err == nil {
			firstLimitTx.Rollback()
			t.Fatal("201st prerequisite unexpectedly succeeded")
		}
	}
	if err := firstLimitTx.Commit(); err != nil {
		t.Fatalf("commit prerequisite limit fixture: %v", err)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE task_id='limit-source'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Fatalf("prerequisites after limit rejection = %d, want 200", count)
	}

	secondLimitTx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertTask(secondLimitTx, "limit-prerequisite", 1400); err != nil {
		secondLimitTx.Rollback()
		t.Fatalf("insert limit prerequisite: %v", err)
	}
	for index := 0; index < 201; index++ {
		dependentID := fmt.Sprintf("limit-dependent-%03d", index)
		if err := insertTask(secondLimitTx, dependentID, 1500+index); err != nil {
			secondLimitTx.Rollback()
			t.Fatalf("insert %s: %v", dependentID, err)
		}
		if index < 200 {
			if err := insertDependency(secondLimitTx, dependentID, "limit-prerequisite"); err != nil {
				secondLimitTx.Rollback()
				t.Fatalf("insert dependent %d: %v", index, err)
			}
		} else if err := insertDependency(secondLimitTx, dependentID, "limit-prerequisite"); err == nil {
			secondLimitTx.Rollback()
			t.Fatal("201st dependent unexpectedly succeeded")
		}
	}
	if err := secondLimitTx.Commit(); err != nil {
		t.Fatalf("commit dependent limit fixture: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE prerequisite_task_id='limit-prerequisite'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 200 {
		t.Fatalf("dependents after limit rejection = %d, want 200", count)
	}

	cycleTx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"cycle-a", "cycle-b", "cycle-c"} {
		if err := insertTask(cycleTx, id, 1800+index); err != nil {
			cycleTx.Rollback()
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	if err := insertDependency(cycleTx, "cycle-a", "cycle-b"); err != nil {
		cycleTx.Rollback()
		t.Fatal(err)
	}
	if err := insertDependency(cycleTx, "cycle-b", "cycle-c"); err != nil {
		cycleTx.Rollback()
		t.Fatal(err)
	}
	if err := insertDependency(cycleTx, "cycle-c", "cycle-a"); err == nil {
		cycleTx.Rollback()
		t.Fatal("transitive dependency cycle unexpectedly succeeded")
	}
	if err := cycleTx.Commit(); err != nil {
		t.Fatalf("commit cycle fixture: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE task_id LIKE 'cycle-%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("cycle rows after rejected edge = %d, want 2", count)
	}
	if err := CheckIntegrity(ctx, database); err != nil {
		t.Fatalf("dependency limit/cycle integrity: %v", err)
	}
}
