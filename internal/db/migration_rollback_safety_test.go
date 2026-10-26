package db

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPre009QueriesAndPostUpgradeWritesSurviveBinaryOnlyRollback(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 9 {
		t.Skip("migration 009 is not present")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "roadmap.db")
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 8)
	populateProductionFixture(t, ctx, database, 8)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("upgrade through migration 009: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO task_agent_work(task_id, operation_id, actor_id, state, phase, summary, next_action, checkpoint_refs, checkpoint_completed, checkpoint_total, started_at, updated_at) VALUES ('task-1', 'run-1', 'agent', 'working', 'Implement', 'Upgrade safely', 'Run checks', '["migration"]', 1, 2, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	// This is the pre-009 task read/write shape used by retained binaries. It
	// intentionally does not mention task_agent_work or any newer columns.
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET title='Updated by retained binary', description='retained write', priority='normal', column_id='todo', position=1.75, assignee_id=NULL, due_at=NULL, version=version+1, updated_at='2026-01-02T00:00:00Z' WHERE id='task-1' AND version=3`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen without db.Open/Migrate, standing in for the retained binary's
	// binary-only rollback. New-schema rows must remain untouched while old
	// task query/write patterns continue to work.
	retained, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var title, operationID, state string
	if err := retained.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Updated by retained binary" {
		t.Fatalf("retained task title = %q", title)
	}
	if err := retained.QueryRowContext(ctx, `SELECT operation_id, state FROM task_agent_work WHERE task_id='task-1'`).Scan(&operationID, &state); err != nil {
		t.Fatal(err)
	}
	if operationID != "run-1" || state != "working" {
		t.Fatalf("post-upgrade work row = %s/%s", operationID, state)
	}
	if _, err := retained.ExecContext(ctx, `UPDATE tasks SET title='Rollback write', version=version+1, updated_at='2026-01-03T00:00:00Z' WHERE id='task-1' AND version=4`); err != nil {
		t.Fatal(err)
	}
	if err := retained.QueryRowContext(ctx, `SELECT operation_id FROM task_agent_work WHERE task_id='task-1'`).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	if operationID != "run-1" {
		t.Fatalf("task_agent_work changed during retained write: %q", operationID)
	}
}

func TestRetainedBinaryProcessAgainstSchema10(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 10 {
		t.Skip("migration 010 is not present")
	}
	latest, _, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "roadmap.db")
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 8)
	populateProductionFixture(t, ctx, database, 8)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("upgrade to schema %d: %v", latest, err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO task_agent_work(task_id, operation_id, actor_id, state, phase, summary, next_action, checkpoint_refs, started_at, updated_at) VALUES ('task-1', 'retained-run', 'agent', 'working', 'old binary', 'Existing pulse', 'Keep going', '[]', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	populateAuditFixture(t, ctx, database)
	beforeAudits := readAuditFixture(t, ctx, database)
	inspection, err := InspectSchema(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaVersion != latest || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
		t.Fatalf("upgraded schema inspection = %#v", inspection)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Build a standalone executable that represents the retained pre-009
	// binary's database boundary. It deliberately has no dependency on this
	// package's migration runner and performs only the old task query/write.
	workerPath := buildRetainedBinary(t)
	worker := exec.Command(workerPath, path)
	if output, err := worker.CombinedOutput(); err != nil {
		t.Fatalf("retained binary process: %v\n%s", err, output)
	}
	retained, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var title, operationID string
	if err := retained.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id='task-1'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Retained process write" {
		t.Fatalf("retained process task write = %q", title)
	}
	if err := retained.QueryRowContext(ctx, `SELECT operation_id FROM task_agent_work WHERE task_id='task-1'`).Scan(&operationID); err != nil {
		t.Fatal(err)
	}
	if operationID != "retained-run" {
		t.Fatalf("post-upgrade agent work row changed by retained process: %q", operationID)
	}
	afterAudits := readAuditFixture(t, ctx, retained)
	if !reflect.DeepEqual(beforeAudits, afterAudits) {
		t.Fatalf("retained process changed audit rows:\nbefore=%#v\nafter=%#v", beforeAudits, afterAudits)
	}
	inspection, err = InspectSchema(ctx, retained)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.SchemaVersion != latest || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
		t.Fatalf("retained process changed schema inspection = %#v", inspection)
	}
	if err := CheckIntegrity(ctx, retained); err != nil {
		t.Fatalf("retained process integrity: %v", err)
	}
}

const retainedBinarySource = `package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: retained-roadmap DATABASE")
	}
	database, err := sql.Open("sqlite", "file:"+os.Args[1]+"?_pragma=foreign_keys(1)")
	if err != nil {
		fail("open database: %v", err)
	}
	defer database.Close()
	var title string
	if err := database.QueryRow("SELECT id, number, project_id, column_id, title, description, priority, position, version FROM tasks WHERE id='task-1'").Scan(new(string), new(int), new(string), new(string), &title, new(string), new(string), new(float64), new(int)); err != nil {
		fail("read pre-009 task shape: %v", err)
	}
	if title == "" {
		fail("retained read returned an empty task title")
	}
	result, err := database.Exec("UPDATE tasks SET title='Retained process write', version=version+1, updated_at='2026-01-04T00:00:00Z' WHERE id='task-1' AND version=3")
	if err != nil {
		fail("write pre-009 task shape: %v", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		fail("retained write affected %d rows: %v", changed, err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\\n", args...)
	os.Exit(1)
}
`

func buildRetainedBinary(t *testing.T) string {
	t.Helper()
	moduleRoot := findModuleRoot(t)
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "main.go")
	binaryPath := filepath.Join(sourceDir, "roadmap-retained")
	if err := os.WriteFile(sourcePath, []byte(retainedBinarySource), 0o600); err != nil {
		t.Fatalf("write retained binary source: %v", err)
	}
	build := exec.Command("go", "build", "-mod=readonly", "-o", binaryPath, sourcePath)
	build.Dir = moduleRoot
	// The normal package test has already resolved this module's dependencies;
	// building from the same module avoids any git checkout or network lookup
	// for historical application sources.
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build retained binary: %v\n%s", err, output)
	}
	if info, err := os.Stat(binaryPath); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("retained binary is not executable: %v", err)
	}
	return binaryPath
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get test working directory: %v", err)
	}
	for {
		if info, statErr := os.Stat(filepath.Join(directory, "go.mod")); statErr == nil && !info.IsDir() {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not find module root")
		}
		directory = parent
	}
}
