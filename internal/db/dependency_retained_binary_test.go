package db

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRetainedPre012BinaryCannotBypassDependencyGuards(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 12 {
		t.Skip("migration 012 is not present")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "roadmap.db")
	database, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	applyMigrationPrefix(t, ctx, database, migrations, 11)
	populateProductionFixture(t, ctx, database, 11)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("upgrade pre-012 database: %v", err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET claimed_by=NULL, claim_expires_at=NULL WHERE id='task-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO columns(id, project_id, name, semantic_state, position, created_at, updated_at) VALUES ('done', 'project', 'Done', 'completed', (SELECT MAX(position)+1 FROM columns WHERE project_id='project'), '2026-01-02T00:00:02Z', '2026-01-02T00:00:02Z')`); err != nil {
		t.Fatal(err)
	}
	insertTask := func(id string, number int) {
		t.Helper()
		if _, err := database.ExecContext(ctx, `INSERT INTO tasks(id, project_id, number, column_id, kind, title, description, priority, position, version, created_at, updated_at) VALUES (?, 'project', ?, 'todo', 'task', ?, '', 'normal', ?, 1, '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`, id, number, id, number); err != nil {
			t.Fatalf("insert retained fixture task %s: %v", id, err)
		}
	}
	insertTask("bulk-dependent", 100)
	insertTask("bulk-prerequisite", 101)
	if _, err := database.ExecContext(ctx, `INSERT INTO task_dependencies(task_id, prerequisite_task_id, created_by, created_at) VALUES ('task-1', 'bug-1', 'owner', '2026-01-02T00:00:00Z'), ('bulk-dependent', 'bulk-prerequisite', 'owner', '2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("insert retained dependency fixtures: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	workerPath := buildRetainedDependencyBinary(t)
	worker := exec.Command(workerPath, path)
	if output, err := worker.CombinedOutput(); err != nil {
		t.Fatalf("retained dependency binary: %v\n%s", err, output)
	}

	retained, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	var title, columnID string
	var version int
	if err := retained.QueryRowContext(ctx, `SELECT title, column_id, version FROM tasks WHERE id='task-1'`).Scan(&title, &columnID, &version); err != nil {
		t.Fatal(err)
	}
	if title != "Retained dependency edit" || columnID != "doing" || version != 5 {
		t.Fatalf("retained task state = %q/%q/%d, want edited active version 5", title, columnID, version)
	}
	var bugColumn, bugCompleted sql.NullString
	if err := retained.QueryRowContext(ctx, `SELECT column_id, completed_at FROM tasks WHERE id='bug-1'`).Scan(&bugColumn, &bugCompleted); err != nil {
		t.Fatal(err)
	}
	if bugColumn.String != "done" || !bugCompleted.Valid {
		t.Fatalf("retained prerequisite state = %q/%q, want completed in done", bugColumn.String, bugCompleted.String)
	}
	var bulkState string
	if err := retained.QueryRowContext(ctx, `SELECT semantic_state FROM columns WHERE id='todo'`).Scan(&bulkState); err != nil {
		t.Fatal(err)
	}
	if bulkState != "backlog" {
		t.Fatalf("blocked bulk completion changed todo semantic state to %q", bulkState)
	}
	var bulkCompleted int
	if err := retained.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE id IN ('bulk-dependent', 'bulk-prerequisite') AND completed_at IS NOT NULL`).Scan(&bulkCompleted); err != nil {
		t.Fatal(err)
	}
	if bulkCompleted != 0 {
		t.Fatalf("blocked bulk completion wrote %d completion timestamps", bulkCompleted)
	}
	var dependencyCount int
	if err := retained.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencyCount); err != nil {
		t.Fatal(err)
	}
	if dependencyCount != 2 {
		t.Fatalf("retained binary changed dependency row count to %d, want 2", dependencyCount)
	}
	if err := CheckIntegrity(ctx, retained); err != nil {
		t.Fatalf("retained dependency schema integrity: %v", err)
	}
}

const retainedDependencyBinarySource = `package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: retained-dependency DATABASE")
	}
	database, err := sql.Open("sqlite", "file:"+os.Args[1]+"?_pragma=foreign_keys(1)")
	if err != nil {
		fail("open database: %v", err)
	}
	defer database.Close()

	// A pre-012 edit that does not touch lifecycle columns remains valid.
	if _, err := database.Exec("UPDATE tasks SET title='Retained dependency edit', version=version+1, updated_at='2026-01-03T00:00:00Z' WHERE id='task-1' AND version=3"); err != nil {
		fail("unrelated retained edit: %v", err)
	}

	// Each guarded statement changes multiple columns so a successful partial
	// write would be visible in the final assertions in the Go test.
	reject(database, "claim", "UPDATE tasks SET claimed_by='agent', claim_expires_at='2999-01-01T00:00:00Z', version=version+1, updated_at='2026-01-03T00:00:01Z' WHERE id='task-1' AND version=4")
	reject(database, "start", "UPDATE tasks SET column_id='doing', version=version+1, updated_at='2026-01-03T00:00:02Z' WHERE id='task-1' AND version=4")
	reject(database, "complete", "UPDATE tasks SET column_id='done', completed_at='2026-01-03T00:00:03Z', version=version+1, updated_at='2026-01-03T00:00:03Z' WHERE id='task-1' AND version=4")
	reject(database, "delete prerequisite", "UPDATE tasks SET deleted_at='2026-01-03T00:00:04Z', version=version+1, updated_at='2026-01-03T00:00:04Z' WHERE id='bug-1' AND version=2")

	// Completing the prerequisite makes the normal retained task transition
	// valid.  It then gives the reopen and bulk-column guards a live dependent
	// that has already started work.
	if _, err := database.Exec("UPDATE tasks SET column_id='done', completed_at='2026-01-03T00:00:05Z', version=version+1, updated_at='2026-01-03T00:00:05Z' WHERE id='bug-1' AND version=2"); err != nil {
		fail("complete prerequisite: %v", err)
	}
	if _, err := database.Exec("UPDATE tasks SET column_id='doing', claimed_by='agent', claim_expires_at='2999-01-01T00:00:00Z', version=version+1, updated_at='2026-01-03T00:00:06Z' WHERE id='task-1' AND version=4"); err != nil {
		fail("start retained dependent: %v", err)
	}
	reject(database, "reopen prerequisite", "UPDATE tasks SET column_id='todo', completed_at=NULL, version=version+1, updated_at='2026-01-03T00:00:07Z' WHERE id='bug-1' AND version=3")
	reject(database, "bulk reopen", "UPDATE columns SET semantic_state='backlog', updated_at='2026-01-03T00:00:08Z' WHERE id='done' AND semantic_state='completed'")
	reject(database, "bulk start", "UPDATE columns SET semantic_state='active', updated_at='2026-01-03T00:00:09Z' WHERE id='todo' AND semantic_state='backlog'")
	reject(database, "bulk completion", "UPDATE columns SET semantic_state='completed', updated_at='2026-01-03T00:00:10Z' WHERE id='todo' AND semantic_state='backlog'")
}

func reject(database *sql.DB, name, statement string) {
	if _, err := database.Exec(statement); err == nil {
		fail("%s unexpectedly succeeded", name)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\\n", args...)
	os.Exit(1)
}
`

func buildRetainedDependencyBinary(t *testing.T) string {
	t.Helper()
	moduleRoot := findModuleRoot(t)
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "main.go")
	binaryPath := filepath.Join(sourceDir, "roadmap-retained-dependency")
	if err := os.WriteFile(sourcePath, []byte(retainedDependencyBinarySource), 0o600); err != nil {
		t.Fatalf("write retained dependency source: %v", err)
	}
	build := exec.Command("go", "build", "-mod=readonly", "-o", binaryPath, sourcePath)
	build.Dir = moduleRoot
	build.Env = os.Environ()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build retained dependency binary: %v\n%s", err, output)
	}
	if info, err := os.Stat(binaryPath); err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		t.Fatalf("retained dependency binary is not executable: %v", err)
	}
	return binaryPath
}
