package store

import (
	"context"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"testing"
)

func TestPortableConflictRemapsAreStableAcrossRetries(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-remap-retry.db"))
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
	eventCursor := int64(1)
	commentID := "portable-remap-comment"
	checkpointCompleted, checkpointTotal := 0, 1
	// Occupy the first deterministic project remap candidate with unrelated
	// data. The retry below must still find the suffix selected after this
	// collision instead of creating a second copy on every attempt.
	occupiedProjectID := portableStableID("project", "portable-remap-project")
	if _, err := database.ExecContext(ctx, `INSERT INTO projects(id,key,slug,name,description,color,favorite,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, occupiedProjectID, "OCCUPIED", "occupied", "Unrelated destination", "", "#64748b", 0, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	archive := func(name, title, body, payload, summary string) PortableArchive {
		return PortableArchive{
			Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
			Source:        PortableSource{Product: "helm", API: "/api/v1"},
			Projects:      []PortableProject{{ID: "portable-remap-project", Key: "REMAP", Slug: "portable-remap", Name: name, Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
			Columns:       []PortableColumn{{ID: "portable-remap-column", ProjectID: "portable-remap-project", Name: "Backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
			Tasks:         []PortableTask{{ID: "portable-remap-task", Number: 1, ProjectID: "portable-remap-project", Kind: "task", ColumnID: "portable-remap-column", Title: title, Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
			Labels:        []PortableLabel{},
			Comments:      []PortableComment{{ID: commentID, TaskID: "portable-remap-task", ActorID: actor.ID, Body: body, CreatedAt: timestamp, UpdatedAt: timestamp}},
			Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
			Activity: PortableActivity{
				Events:           []PortableEvent{{Cursor: eventCursor, ID: "portable-remap-event", Type: "portable.test", ActorID: portableTestString(actor.ID), ProjectID: portableTestString("portable-remap-project"), TaskID: portableTestString("portable-remap-task"), Payload: json.RawMessage(payload), CreatedAt: timestamp}},
				AgentWork:        []PortableAgentWork{},
				AgentWorkHistory: []PortableAgentWorkHistory{{ID: "portable-remap-history", TaskID: "portable-remap-task", OperationID: "portable-remap-operation", ActorID: actor.ID, State: "working", Phase: "test", Summary: summary, CheckpointRefs: []string{"one"}, CheckpointCompleted: &checkpointCompleted, CheckpointTotal: &checkpointTotal, StartedAt: timestamp, CreatedAt: timestamp, GeneratedCommentID: portableTestString(commentID), ProgressEventCursor: &eventCursor}},
			},
		}
	}
	first := archive("First", "First task", "first comment", `{"v":1}`, "first summary")
	if report, err := data.ImportPortable(ctx, first, PortableImportOptions{ActorID: actor.ID}); err != nil {
		t.Fatalf("first import: %v report=%+v", err, report)
	}
	second := archive("Second", "Second task", "second comment", `{"v":2}`, "second summary")
	firstRemap, err := data.ImportPortable(ctx, second, PortableImportOptions{ActorID: actor.ID})
	if err != nil {
		t.Fatalf("first remapped import: %v report=%+v", err, firstRemap)
	}
	if firstRemap.Counts.ProjectsCreated != 1 || firstRemap.Counts.TasksCreated != 1 || firstRemap.Counts.CommentsCreated != 1 || firstRemap.Counts.EventsCreated != 1 || firstRemap.Counts.HistoryCreated != 1 {
		t.Fatalf("first remapped report = %+v", firstRemap)
	}
	secondRemap, err := data.ImportPortable(ctx, second, PortableImportOptions{ActorID: actor.ID})
	if err != nil {
		t.Fatalf("retry remapped import: %v report=%+v", err, secondRemap)
	}
	if secondRemap.Counts.ProjectsCreated != 0 || secondRemap.Counts.TasksCreated != 0 || secondRemap.Counts.CommentsCreated != 0 || secondRemap.Counts.EventsCreated != 0 || secondRemap.Counts.HistoryCreated != 0 {
		t.Fatalf("retry created duplicate records: %+v", secondRemap)
	}
	if secondRemap.Counts.ProjectsSkipped != 1 || secondRemap.Counts.TasksSkipped != 1 || secondRemap.Counts.CommentsSkipped != 1 || secondRemap.Counts.EventsSkipped != 1 || secondRemap.Counts.HistorySkipped != 1 {
		t.Fatalf("retry skip report = %+v", secondRemap)
	}
	var projectCount, taskCount, commentCount, eventCount, historyCount int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM projects`:                &projectCount,
		`SELECT COUNT(*) FROM tasks`:                   &taskCount,
		`SELECT COUNT(*) FROM comments`:                &commentCount,
		`SELECT COUNT(*) FROM events`:                  &eventCount,
		`SELECT COUNT(*) FROM task_agent_work_history`: &historyCount,
	} {
		if err := database.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if projectCount != 3 || taskCount != 2 || commentCount != 2 || eventCount != 3 || historyCount != 2 {
		t.Fatalf("retry duplicated records projects=%d tasks=%d comments=%d events=%d history=%d", projectCount, taskCount, commentCount, eventCount, historyCount)
	}
}

func TestPortableFieldOnlyRemapsReuseTheImportedSubtree(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"

	t.Run("project key and slug", func(t *testing.T) {
		database, err := db.Open(ctx, filepath.Join(t.TempDir(), "project-fields.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		data := New(database)
		actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("FIELD"), Slug: portableTestString("field-slug"), Name: portableTestString("Existing")}, actor.ID); err != nil {
			t.Fatal(err)
		}
		archive := PortableArchive{
			Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
			Projects: []PortableProject{{ID: "field-project", Key: "FIELD", Slug: "field-slug", Name: "Imported", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
			Columns:  []PortableColumn{}, Tasks: []PortableTask{}, Labels: []PortableLabel{}, Comments: []PortableComment{},
			Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
			Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
		}
		first, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
		if err != nil {
			t.Fatalf("first import: %v report=%+v", err, first)
		}
		if first.Counts.ProjectsCreated != 1 || !portableReportHasRemap(first, "project", "key") || !portableReportHasRemap(first, "project", "slug") {
			t.Fatalf("first field remap report = %+v", first)
		}
		second, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
		if err != nil {
			t.Fatalf("retry import: %v report=%+v", err, second)
		}
		if second.Counts.ProjectsCreated != 0 || second.Counts.ProjectsSkipped != 1 || !portableReportHasRemap(second, "project", "key") || !portableReportHasRemap(second, "project", "slug") {
			t.Fatalf("retry field remap report = %+v", second)
		}
		var projectCount int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projectCount); err != nil {
			t.Fatal(err)
		}
		if projectCount != 2 {
			t.Fatalf("field-only project retry created a duplicate: count=%d", projectCount)
		}
	})

	t.Run("column position and task number", func(t *testing.T) {
		database, err := db.Open(ctx, filepath.Join(t.TempDir(), "child-fields.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		data := New(database)
		actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		target, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("TARGET"), Name: portableTestString("Target")}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := data.ListColumns(ctx, target.ID)
		if err != nil || len(columns) == 0 {
			t.Fatalf("target columns = %d, err=%v", len(columns), err)
		}
		if _, err := data.CreateTask(ctx, target.ID, TaskInput{Title: portableTestString("Existing number one"), ColumnID: &columns[0].ID}, actor.ID); err != nil {
			t.Fatal(err)
		}
		archive := PortableArchive{
			Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
			Projects: []PortableProject{{ID: "child-source-project", Key: "CHILD", Slug: "child-source", Name: "Child source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
			Columns:  []PortableColumn{{ID: "child-source-column", ProjectID: "child-source-project", Name: "Imported column", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
			Tasks:    []PortableTask{{ID: "child-source-task", Number: 1, ProjectID: "child-source-project", Kind: "task", ColumnID: "child-source-column", Title: "Imported task", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
			Labels:   []PortableLabel{}, Comments: []PortableComment{},
			Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
			Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
		}
		first, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
		if err != nil {
			t.Fatalf("first child import: %v report=%+v", err, first)
		}
		if first.Counts.ColumnsCreated != 1 || first.Counts.TasksCreated != 1 || !portableReportHasRemap(first, "column", "position") || !portableReportHasRemap(first, "task", "number") {
			t.Fatalf("first child field remap report = %+v", first)
		}
		second, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
		if err != nil {
			t.Fatalf("retry child import: %v report=%+v", err, second)
		}
		if second.Counts.ColumnsCreated != 0 || second.Counts.ColumnsSkipped != 1 || second.Counts.TasksCreated != 0 || second.Counts.TasksSkipped != 1 || !portableReportHasRemap(second, "column", "position") || !portableReportHasRemap(second, "task", "number") {
			t.Fatalf("retry child field remap report = %+v", second)
		}
		var columnCount, taskCount int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM columns WHERE project_id=?`, target.ID).Scan(&columnCount); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, target.ID).Scan(&taskCount); err != nil {
			t.Fatal(err)
		}
		if columnCount != len(columns)+1 || taskCount != 2 {
			t.Fatalf("field-only child retry created duplicates: columns=%d tasks=%d", columnCount, taskCount)
		}
	})
}

func portableReportHasRemap(report PortableImportReport, entity, field string) bool {
	for _, remap := range report.Remaps {
		if remap.Entity == entity && remap.Field == field {
			return true
		}
	}
	return false
}

func portableReportHasIssue(report PortableImportReport, entity, field, message string) bool {
	for _, issue := range report.Errors {
		if issue.Entity == entity && issue.Field == field && issue.Message == message {
			return true
		}
	}
	return false
}
