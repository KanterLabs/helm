package store

import (
	"context"
	"errors"
	"fmt"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableTaskLinkUniquenessIsPreflighted(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-link-conflict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("LINKS"), Name: portableTestString("Links")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := data.ListColumns(ctx, project.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("columns = %d, err=%v", len(columns), err)
	}
	createTask := func(title string) Task {
		t.Helper()
		task, createErr := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString(title), ColumnID: &columns[0].ID}, actor.ID)
		if createErr != nil {
			t.Fatal(createErr)
		}
		return task
	}
	source, existingTarget, importedTarget := createTask("Source"), createTask("Existing target"), createTask("Imported target")
	createdAt := updatedTimestampForPortableTest()
	if _, err := database.ExecContext(ctx, `INSERT INTO task_links(source_task_id,target_task_id,link_type,created_at) VALUES (?,?,?,?)`, source.ID, existingTarget.ID, "relates", createdAt); err != nil {
		t.Fatal(err)
	}
	archive, err := data.ExportPortable(ctx, []string{project.ID})
	if err != nil {
		t.Fatal(err)
	}
	archive.Relationships.TaskLinks = []PortableTaskLink{{SourceTaskID: source.ID, TargetTaskID: importedTarget.ID, LinkType: "relates", CreatedAt: createdAt}}
	preview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: project.ID, DryRun: true})
	if err != nil {
		t.Fatalf("link remap dry-run: %v report=%+v", err, preview)
	}
	if preview.Counts.LinksCreated != 0 || preview.Counts.LinksSkipped != 1 || !portableReportHasRemap(preview, "task_link", "target_task_id") {
		t.Fatalf("link remap preview = %+v", preview)
	}
	if _, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: project.ID}); err != nil {
		t.Fatalf("link remap import: %v", err)
	}
	var links int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_links WHERE source_task_id=?`, source.ID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("remap import changed destination link count to %d", links)
	}
	failPreview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: project.ID, Conflict: portableConflictFail, DryRun: true})
	if !errors.Is(err, ErrConflict) || len(failPreview.Errors) == 0 || !strings.Contains(failPreview.Errors[0].Message, "already links") {
		t.Fatalf("link fail preview error=%v report=%+v", err, failPreview)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_links WHERE source_task_id=?`, source.ID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 1 {
		t.Fatalf("link fail preview mutated destination link count to %d", links)
	}
}

func TestPortableDependencyLimitsArePreflightedWithoutMutation(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-dependency-limit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("DEPLIMIT"), Name: portableTestString("Dependency limits")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := data.ListColumns(ctx, project.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("columns = %d, err=%v", len(columns), err)
	}
	dependent, err := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Dependent"), ColumnID: &columns[0].ID}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := make([]Task, 0, maxDirectTaskDependencies)
	for index := 0; index < maxDirectTaskDependencies; index++ {
		prerequisite, createErr := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString(fmt.Sprintf("Prerequisite %d", index)), ColumnID: &columns[0].ID}, actor.ID)
		if createErr != nil {
			t.Fatal(createErr)
		}
		prerequisites = append(prerequisites, prerequisite)
		if _, err := database.ExecContext(ctx, `INSERT INTO task_dependencies(task_id,prerequisite_task_id,created_by,created_at) VALUES (?,?,?,?)`, dependent.ID, prerequisite.ID, actor.ID, updatedTimestampForPortableTest()); err != nil {
			t.Fatal(err)
		}
	}
	// The loop above intentionally exercises the trigger boundary; verify the
	// fixture really reached the limit before asking the importer to add one.
	var dependencyCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE task_id=?`, dependent.ID).Scan(&dependencyCount); err != nil {
		t.Fatal(err)
	}
	if dependencyCount != maxDirectTaskDependencies {
		t.Fatalf("dependency fixture count = %d", dependencyCount)
	}
	existingDependent, ok, err := portableExistingTask(database, dependent.ID)
	if err != nil || !ok {
		t.Fatalf("existing dependent = %+v, ok=%v, err=%v", existingDependent, ok, err)
	}
	timestamp := updatedTimestampForPortableTest()
	sourceProjectID := "dependency-source-project"
	existingDependent.ProjectID = sourceProjectID
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{{ID: sourceProjectID, Key: "DEPSOURCE", Slug: "dependency-source", Name: "Dependency source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Columns:  []PortableColumn{{ID: columns[0].ID, ProjectID: sourceProjectID, Name: columns[0].Name, SemanticState: columns[0].SemanticState, Position: columns[0].Position, CreatedAt: columns[0].CreatedAt, UpdatedAt: columns[0].UpdatedAt}},
		Tasks:    []PortableTask{existingDependent, {ID: "portable-new-prerequisite", Number: 999, ProjectID: sourceProjectID, Kind: "task", ColumnID: columns[0].ID, Title: "New prerequisite", Priority: "normal", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{{TaskID: dependent.ID, PrerequisiteID: "portable-new-prerequisite", CreatedBy: portableTestString(actor.ID), CreatedAt: timestamp}}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	preview, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: project.ID, DryRun: true})
	if !errors.Is(err, ErrInvalid) || len(preview.Errors) == 0 || !strings.Contains(preview.Errors[0].Message, "direct prerequisites") {
		t.Fatalf("dependency limit dry-run error=%v report=%+v", err, preview)
	}
	var taskCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, project.ID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	beforeTaskCount := taskCount
	result, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: project.ID})
	if !errors.Is(err, ErrInvalid) || len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "direct prerequisites") {
		t.Fatalf("dependency limit import error=%v report=%+v", err, result)
	}
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, project.ID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != beforeTaskCount {
		t.Fatalf("dependency limit import mutated task count from %d to %d", beforeTaskCount, taskCount)
	}
}

func TestPortableDependenciesPreflightDestinationGraphAndLifecycle(t *testing.T) {
	t.Run("destination cycle", func(t *testing.T) {
		ctx := context.Background()
		database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-destination-cycle.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		data := New(database)
		actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("CYCLE"), Name: portableTestString("Destination cycle")}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := data.ListColumns(ctx, project.ID)
		if err != nil || len(columns) == 0 {
			t.Fatalf("columns = %d, err=%v", len(columns), err)
		}
		createTask := func(title string) Task {
			t.Helper()
			task, createErr := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString(title), ColumnID: &columns[0].ID}, actor.ID)
			if createErr != nil {
				t.Fatal(createErr)
			}
			return task
		}
		a, b := createTask("A"), createTask("B")
		timestamp := updatedTimestampForPortableTest()
		if _, err := database.ExecContext(ctx, `INSERT INTO task_dependencies(task_id,prerequisite_task_id,created_by,created_at) VALUES (?,?,?,?)`, a.ID, b.ID, actor.ID, timestamp); err != nil {
			t.Fatal(err)
		}
		archive, err := data.ExportPortable(ctx, []string{project.ID})
		if err != nil {
			t.Fatal(err)
		}
		// Keep the destination A -> B edge out of the archive. Importing only
		// B -> A must be checked against the destination graph, not rejected by
		// the archive-local cycle validator first.
		archive.Relationships.Dependencies = []PortableDependency{{TaskID: b.ID, PrerequisiteID: a.ID, CreatedBy: portableTestString(actor.ID), CreatedAt: timestamp}}
		var beforeDependencies int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&beforeDependencies); err != nil {
			t.Fatal(err)
		}
		for _, options := range []PortableImportOptions{{ActorID: actor.ID, TargetProjectID: project.ID, DryRun: true}, {ActorID: actor.ID, TargetProjectID: project.ID}} {
			report, importErr := data.ImportPortable(ctx, archive, options)
			if !errors.Is(importErr, ErrConflict) || !portableReportHasIssue(report, "dependency", "prerequisite_task_id", "dependency would create a cycle in the destination graph") {
				t.Fatalf("destination cycle options=%+v error=%v report=%+v", options, importErr, report)
			}
			var dependencies int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencies); err != nil {
				t.Fatal(err)
			}
			if dependencies != beforeDependencies {
				t.Fatalf("destination cycle options=%+v mutated dependencies from %d to %d", options, beforeDependencies, dependencies)
			}
		}
	})

	t.Run("active task with unmet prerequisite", func(t *testing.T) {
		ctx := context.Background()
		database, err := db.Open(ctx, filepath.Join(t.TempDir(), "portable-destination-unmet.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer database.Close()
		data := New(database)
		actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		project, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("UNMET"), Name: portableTestString("Destination unmet")}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := data.ListColumns(ctx, project.ID)
		if err != nil || len(columns) < 3 {
			t.Fatalf("columns = %d, err=%v", len(columns), err)
		}
		var activeColumn Column
		for _, column := range columns {
			if column.SemanticState == "active" {
				activeColumn = column
				break
			}
		}
		if activeColumn.ID == "" {
			t.Fatal("active column was not created")
		}
		dependent, err := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Active dependent"), ColumnID: &columns[0].ID}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		prerequisite, err := data.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Unfinished prerequisite"), ColumnID: &columns[0].ID}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		timestamp := updatedTimestampForPortableTest()
		if _, err := database.ExecContext(ctx, `UPDATE tasks SET column_id=?,updated_at=? WHERE id=?`, activeColumn.ID, timestamp, dependent.ID); err != nil {
			t.Fatal(err)
		}
		archive, err := data.ExportPortable(ctx, []string{project.ID})
		if err != nil {
			t.Fatal(err)
		}
		archive.Relationships.Dependencies = []PortableDependency{{TaskID: dependent.ID, PrerequisiteID: prerequisite.ID, CreatedBy: portableTestString(actor.ID), CreatedAt: timestamp}}
		var beforeTasks, beforeDependencies int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&beforeTasks); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&beforeDependencies); err != nil {
			t.Fatal(err)
		}
		for _, options := range []PortableImportOptions{{ActorID: actor.ID, TargetProjectID: project.ID, DryRun: true}, {ActorID: actor.ID, TargetProjectID: project.ID}} {
			report, importErr := data.ImportPortable(ctx, archive, options)
			if !errors.Is(importErr, ErrInvalid) || !portableReportHasIssue(report, "dependency", "task_id", "dependency prerequisites are not satisfied for this task state") {
				t.Fatalf("destination unmet options=%+v error=%v report=%+v", options, importErr, report)
			}
			var tasks, dependencies int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies`).Scan(&dependencies); err != nil {
				t.Fatal(err)
			}
			if tasks != beforeTasks || dependencies != beforeDependencies {
				t.Fatalf("destination unmet options=%+v mutated tasks/dependencies from %d/%d to %d/%d", options, beforeTasks, beforeDependencies, tasks, dependencies)
			}
		}
	})
}
