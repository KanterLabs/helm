package store

import (
	"context"
	"database/sql"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"strings"
	"testing"
)

func updatedTimestampForPortableTest() string { return "2026-09-01T10:05:00Z" }

func float64Ptr(value float64) *float64 { return &value }

type portableInterleavingSQL struct {
	portableSQL
	beforeTaskQuery func()
}

func (q *portableInterleavingSQL) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if q.beforeTaskQuery != nil && strings.Contains(query, "SELECT t.id,t.number") {
		callback := q.beforeTaskQuery
		q.beforeTaskQuery = nil
		callback()
	}
	return q.portableSQL.QueryContext(ctx, query, args...)
}

func TestPortableExportUsesSingleReadSnapshot(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-export-snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Snapshot owner"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("SNAP"), Slug: portableTestString("snapshot"), Name: portableTestString("Snapshot")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := data.ListColumns(ctx, project.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("snapshot columns = %d, err=%v", len(columns), err)
	}
	baseTask, err := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Before snapshot"), ColumnID: &columns[0].ID}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}

	writerConn, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writerConn.Close()
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	newColumnID, newTaskID := "snapshot-new-column", "snapshot-new-task"
	var writerErr error
	query := &portableInterleavingSQL{portableSQL: tx}
	query.beforeTaskQuery = func() {
		writerTx, beginErr := writerConn.BeginTx(ctx, nil)
		if beginErr != nil {
			writerErr = beginErr
			return
		}
		defer writerTx.Rollback()
		if _, writerErr = writerTx.ExecContext(ctx, `INSERT INTO columns(id,project_id,name,semantic_state,position,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, newColumnID, project.ID, "Added concurrently", "backlog", 100, updatedTimestampForPortableTest(), updatedTimestampForPortableTest()); writerErr != nil {
			return
		}
		if _, writerErr = writerTx.ExecContext(ctx, `INSERT INTO tasks(id,project_id,number,column_id,kind,title,description,priority,position,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, newTaskID, project.ID, 100, newColumnID, "task", "Added concurrently", "", "normal", 0, 1, updatedTimestampForPortableTest(), updatedTimestampForPortableTest()); writerErr != nil {
			return
		}
		writerErr = writerTx.Commit()
	}
	archive, err := exportPortable(ctx, query, []string{project.ID})
	if err != nil {
		t.Fatalf("snapshot export: %v", err)
	}
	if writerErr != nil {
		t.Fatalf("interleaved writer: %v", writerErr)
	}
	if query.beforeTaskQuery != nil {
		t.Fatal("interleaved writer hook did not run before task query")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var currentColumns, currentTasks int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM columns WHERE project_id=?`, project.ID).Scan(&currentColumns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, project.ID).Scan(&currentTasks); err != nil {
		t.Fatal(err)
	}
	if currentColumns != len(columns)+1 || currentTasks != 2 {
		t.Fatalf("current rows columns=%d tasks=%d, want columns=%d tasks=2", currentColumns, currentTasks, len(columns)+1)
	}
	archiveColumnIDs := make(map[string]struct{}, len(archive.Columns))
	for _, column := range archive.Columns {
		archiveColumnIDs[column.ID] = struct{}{}
	}
	archiveTaskID := ""
	if len(archive.Tasks) > 0 {
		archiveTaskID = archive.Tasks[0].ID
	}
	if len(archive.Columns) != len(columns) || len(archive.Tasks) != 1 || archiveTaskID != baseTask.ID {
		t.Fatalf("snapshot archive columns=%d tasks=%d task=%q, want columns=%d tasks=1 task=%q", len(archive.Columns), len(archive.Tasks), archiveTaskID, len(columns), baseTask.ID)
	}
	for _, task := range archive.Tasks {
		if _, ok := archiveColumnIDs[task.ColumnID]; !ok {
			t.Fatalf("snapshot archive task %s references non-archived column %s", task.ID, task.ColumnID)
		}
	}
	for _, column := range archive.Columns {
		if column.ID == newColumnID {
			t.Fatalf("snapshot archive included concurrently-created column %s", newColumnID)
		}
	}
}
