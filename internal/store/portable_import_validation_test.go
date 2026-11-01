package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"testing"
)

func TestPortableDryRunValidationAndConflictAreNonMutating(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-validation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	timestamp := "2026-09-04T00:00:00Z"
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{{ID: "portable-project", Key: "PORT", Slug: "portable", Name: "Portable", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Columns:  []PortableColumn{{ID: "portable-column", ProjectID: "portable-project", Name: "Backlog", SemanticState: "backlog", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Tasks:    []PortableTask{{ID: "portable-task", Number: 1, ProjectID: "portable-project", Kind: "task", ColumnID: "portable-column", Title: "Preview", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	preview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v report=%+v", err, preview)
	}
	if !preview.DryRun || preview.Counts.ProjectsCreated != 1 || preview.Counts.TasksCreated != 1 {
		t.Fatalf("dry-run report = %+v", preview)
	}
	var projects int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, "portable-project").Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Fatalf("dry-run wrote %d projects", projects)
	}
	invalidArchive := archive
	invalidArchive.Format = "unknown.format"
	if _, err := data.ImportPortable(ctx, invalidArchive, PortableImportOptions{ActorID: actor.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid archive error = %v, want ErrInvalid", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Fatalf("invalid archive wrote %d projects", projects)
	}
	if _, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID}); err != nil {
		t.Fatalf("valid import: %v", err)
	}
	conflictArchive := archive
	conflictArchive.Tasks = []PortableTask{}
	conflictArchive.Columns = []PortableColumn{}
	conflictArchive.Projects[0].Name = "Different project"
	if _, err := data.ImportPortable(ctx, conflictArchive, PortableImportOptions{ActorID: actor.ID, Conflict: "fail"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want ErrConflict", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatal(err)
	}
	if projects != 1 {
		t.Fatalf("conflict changed project count to %d", projects)
	}
}

func TestPortableImportInvalidatesMappedProjectTaskCursor(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-cursor-revision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	target, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("CURSOR"), Slug: portableTestString("cursor-target"), Name: portableTestString("Cursor target")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := data.ListColumns(ctx, target.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("target columns = %d, err=%v", len(columns), err)
	}
	for index := 0; index < 2; index++ {
		if _, err := data.CreateTask(ctx, target.ID, TaskInput{Title: portableTestString(fmt.Sprintf("Existing task %d", index+1)), ColumnID: &columns[0].ID}, actor.ID); err != nil {
			t.Fatalf("create existing task %d: %v", index, err)
		}
	}
	_, more, cursor, err := data.ListTasksCursor(ctx, target.ID, TaskFilter{Sort: TaskSortNumber, Limit: 1})
	if err != nil || !more || cursor == "" {
		t.Fatalf("first task page more=%t cursor=%q err=%v", more, cursor, err)
	}
	readStats := func() (int64, int, int) {
		t.Helper()
		var revision int64
		if err := database.QueryRowContext(ctx, `SELECT task_collection_revision FROM projects WHERE id=?`, target.ID).Scan(&revision); err != nil {
			t.Fatalf("read task collection revision: %v", err)
		}
		var tasks, events int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, target.ID).Scan(&tasks); err != nil {
			t.Fatalf("count target tasks: %v", err)
		}
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE project_id=?`, target.ID).Scan(&events); err != nil {
			t.Fatalf("count target events: %v", err)
		}
		return revision, tasks, events
	}
	revisionBefore, tasksBefore, eventsBefore := readStats()
	timestamp := updatedTimestampForPortableTest()
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{{ID: "cursor-source-project", Key: "CURSORSRC", Slug: "cursor-source", Name: "Cursor source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Columns:  []PortableColumn{{ID: "cursor-source-column", ProjectID: "cursor-source-project", Name: "Imported backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Tasks:    []PortableTask{{ID: "cursor-source-task", Number: 1, ProjectID: "cursor-source-project", Kind: "task", ColumnID: "cursor-source-column", Title: "Imported task", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	preview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID, DryRun: true})
	if err != nil {
		t.Fatalf("portable cursor dry-run: %v report=%+v", err, preview)
	}
	if !preview.DryRun || preview.Counts.ProjectsSkipped != 1 || preview.Counts.TasksCreated != 1 {
		t.Fatalf("portable cursor dry-run report = %+v", preview)
	}
	if revision, tasks, events := readStats(); revision != revisionBefore || tasks != tasksBefore || events != eventsBefore {
		t.Fatalf("dry-run changed target stats revision/tasks/events=%d/%d/%d, want %d/%d/%d", revision, tasks, events, revisionBefore, tasksBefore, eventsBefore)
	}

	invalidArchive := archive
	invalidArchive.Format = "invalid.format"
	invalidReport, err := data.ImportPortable(ctx, invalidArchive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid portable cursor import error=%v report=%+v, want ErrInvalid", err, invalidReport)
	}
	if revision, tasks, events := readStats(); revision != revisionBefore || tasks != tasksBefore || events != eventsBefore {
		t.Fatalf("validation failure changed target stats revision/tasks/events=%d/%d/%d, want %d/%d/%d", revision, tasks, events, revisionBefore, tasksBefore, eventsBefore)
	}

	report, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
	if err != nil {
		t.Fatalf("portable cursor import: %v report=%+v", err, report)
	}
	if report.Counts.ProjectsSkipped != 1 || report.Counts.ColumnsCreated != 1 || report.Counts.TasksCreated != 1 || !portableReportHasRemap(report, "project", "id") {
		t.Fatalf("portable cursor import report = %+v", report)
	}
	if revision, tasks, events := readStats(); revision <= revisionBefore || tasks != tasksBefore+1 || events != eventsBefore {
		t.Fatalf("import changed target stats revision/tasks/events=%d/%d/%d, want revision > %d, tasks=%d, events=%d", revision, tasks, events, revisionBefore, tasksBefore+1, eventsBefore)
	}
	_, _, _, err = data.ListTasksCursor(ctx, target.ID, TaskFilter{Sort: TaskSortNumber, CursorToken: cursor, Limit: 1})
	if !errors.Is(err, ErrTaskCollectionChanged) {
		t.Fatalf("portable import continuation error = %v, want ErrTaskCollectionChanged", err)
	}
	_, more, freshCursor, err := data.ListTasksCursor(ctx, target.ID, TaskFilter{Sort: TaskSortNumber, Limit: 1})
	if err != nil || !more || freshCursor == "" {
		t.Fatalf("fresh task page after portable import more=%t cursor=%q err=%v", more, freshCursor, err)
	}
	revisionAfterImport, tasksAfterImport, eventsAfterImport := readStats()
	retryReport, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
	if err != nil {
		t.Fatalf("identical portable retry: %v report=%+v", err, retryReport)
	}
	if retryReport.Counts.ProjectsCreated != 0 || retryReport.Counts.ColumnsCreated != 0 || retryReport.Counts.TasksCreated != 0 || retryReport.Counts.ProjectsSkipped != 1 || retryReport.Counts.ColumnsSkipped != 1 || retryReport.Counts.TasksSkipped != 1 {
		t.Fatalf("identical portable retry report = %+v", retryReport)
	}
	if revision, tasks, events := readStats(); revision != revisionAfterImport || tasks != tasksAfterImport || events != eventsAfterImport {
		t.Fatalf("identical portable retry changed target stats revision/tasks/events=%d/%d/%d, want %d/%d/%d", revision, tasks, events, revisionAfterImport, tasksAfterImport, eventsAfterImport)
	}
	if _, _, _, err := data.ListTasksCursor(ctx, target.ID, TaskFilter{Sort: TaskSortNumber, CursorToken: freshCursor, Limit: 1}); err != nil {
		t.Fatalf("identical portable retry invalidated fresh continuation: %v", err)
	}
}
