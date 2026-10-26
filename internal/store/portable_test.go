package store

import (
	"context"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"strings"
	"testing"
)

func portableTestString(value string) *string { return &value }

func TestPortableArchiveRoundTripPreservesPopulatedData(t *testing.T) {
	ctx := context.Background()
	sourceDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	source := New(sourceDB)
	owner, err := source.CreateActor(ctx, Actor{Kind: "human", Name: "Source owner"}, "source-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := source.CreateActor(ctx, Actor{Kind: "agent", Name: "Source worker"}, "worker-secret")
	if err != nil {
		t.Fatal(err)
	}
	project, err := source.CreateProject(ctx, ProjectInput{Key: portableTestString("PORT"), Slug: portableTestString("portable-source"), Name: portableTestString("Portable source"), Description: portableTestString("A populated source project")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := source.ListColumns(ctx, project.ID)
	if err != nil || len(columns) < 2 {
		t.Fatalf("source columns = %d, err=%v", len(columns), err)
	}
	description := "Preserve this task"
	due := "2026-12-31T00:00:00Z"
	first, err := source.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("First task"), Description: &description, Priority: portableTestString("high"), DueAt: &due, DueAtSet: true, ColumnID: &columns[1].ID, Position: float64Ptr(2)}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	actual, expected := "It failed", "It succeeds"
	bugKind := "bug"
	bug, err := source.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Typed bug"), Kind: &bugKind, Bug: &BugInput{ActualBehavior: &actual, ExpectedBehavior: &expected, ReproductionSteps: portableTestString("Open it"), Environment: portableTestString("CI"), AffectedVersion: portableTestString("1.2.3")}, ColumnID: &columns[0].ID}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	label, err := source.CreateLabel(ctx, project.ID, LabelInput{Name: "important", Color: "#ef4444"}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO task_labels(task_id,label_id) VALUES (?,?)`, first.ID, label.ID); err != nil {
		t.Fatal(err)
	}
	comment, err := source.CreateComment(ctx, first.ID, worker.ID, "A durable comment")
	if err != nil {
		t.Fatal(err)
	}
	note, err := source.CreateAgentNote(ctx, first.ID, worker.ID, "known_issue", "Use the retained migration path", []string{"internal/db/migrations"})
	if err != nil {
		t.Fatal(err)
	}
	bug, err = source.AddTaskDependency(ctx, bug.ID, first.ID, bug.Version, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO task_links(source_task_id,target_task_id,link_type,created_at) VALUES (?,?,?,?)`, first.ID, bug.ID, "relates", updatedTimestampForPortableTest()); err != nil {
		t.Fatal(err)
	}
	refs, _ := json.Marshal([]string{"checkpoint-a", "checkpoint-b"})
	started := "2026-09-01T10:00:00Z"
	updated := "2026-09-01T10:05:00Z"
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO task_agent_work(task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, bug.ID, "portable-operation", worker.ID, "working", "build", "Preserve progress", "Review import", string(refs), 1, 2, started, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) VALUES (?,?,?,?,?,?,?)`, "portable-event", "task.portable", worker.ID, project.ID, bug.ID, `{"safe":true}`, updated); err != nil {
		t.Fatal(err)
	}
	var sourceCursor int64
	if err := sourceDB.QueryRowContext(ctx, `SELECT cursor FROM events WHERE id=?`, "portable-event").Scan(&sourceCursor); err != nil {
		t.Fatal(err)
	}
	historyID := "portable-history"
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO task_agent_work_history(id,task_id,operation_id,actor_id,state,phase,summary,next_action,checkpoint_refs,checkpoint_completed,checkpoint_total,started_at,created_at,generated_comment_id,progress_event_cursor) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, historyID, bug.ID, "portable-operation", worker.ID, "working", "build", "Preserve progress", "Review import", string(refs), 1, 2, started, updated, comment.ID, sourceCursor); err != nil {
		t.Fatal(err)
	}

	archive, err := source.ExportPortable(ctx, []string{project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Projects) != 1 || len(archive.Columns) != len(columns) || len(archive.Tasks) != 2 || len(archive.Labels) != 1 || len(archive.Relationships.TaskLabels) != 1 || len(archive.Comments) != 1 || len(archive.AgentNotes) != 1 || len(archive.Relationships.Dependencies) != 1 || len(archive.Relationships.TaskLinks) != 1 || len(archive.Activity.AgentWork) != 1 || len(archive.Activity.AgentWorkHistory) != 1 {
		t.Fatalf("archive counts projects=%d columns=%d tasks=%d labels=%d task_labels=%d comments=%d deps=%d links=%d work=%d history=%d", len(archive.Projects), len(archive.Columns), len(archive.Tasks), len(archive.Labels), len(archive.Relationships.TaskLabels), len(archive.Comments), len(archive.Relationships.Dependencies), len(archive.Relationships.TaskLinks), len(archive.Activity.AgentWork), len(archive.Activity.AgentWorkHistory))
	}
	var exportedBug *PortableBug
	for _, task := range archive.Tasks {
		if task.ID == bug.ID {
			exportedBug = task.Bug
		}
	}
	if exportedBug == nil || exportedBug.ActualBehavior != actual || exportedBug.ReporterID != owner.ID {
		t.Fatalf("exported bug = %+v", exportedBug)
	}
	encoded, _ := json.Marshal(archive)
	if strings.Contains(string(encoded), "source-password-hash") || strings.Contains(string(encoded), "worker-secret") {
		t.Fatalf("archive contains authentication material: %s", encoded)
	}

	destinationDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	destination := New(destinationDB)
	importer, err := destination.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "importer-hash")
	if err != nil {
		t.Fatal(err)
	}
	report, err := destination.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID})
	if err != nil {
		t.Fatalf("import archive: %v report=%+v", err, report)
	}
	if report.Counts.ProjectsCreated != 1 || report.Counts.TasksCreated != 2 || report.Counts.CommentsCreated != 1 || report.Counts.AgentNotesCreated != 1 || report.Counts.DependenciesCreated != 1 || report.Counts.LinksCreated != 1 || report.Counts.EventsCreated == 0 || len(report.Remaps) == 0 {
		t.Fatalf("import report = %+v", report)
	}
	importedBug, err := destination.GetTask(ctx, bug.ID)
	if err != nil {
		t.Fatal(err)
	}
	if importedBug.Title != bug.Title || importedBug.Description != bug.Description || importedBug.Version != bug.Version || importedBug.Bug == nil || importedBug.Bug.ActualBehavior != actual || importedBug.Bug.ReporterID != importer.ID {
		t.Fatalf("imported bug = %+v", importedBug)
	}
	var importedCommentActor, importedDependencyActor string
	if err := destinationDB.QueryRowContext(ctx, `SELECT actor_id FROM comments WHERE id=?`, comment.ID).Scan(&importedCommentActor); err != nil {
		t.Fatal(err)
	}
	if err := destinationDB.QueryRowContext(ctx, `SELECT COALESCE(created_by,'') FROM task_dependencies WHERE task_id=?`, bug.ID).Scan(&importedDependencyActor); err != nil {
		t.Fatal(err)
	}
	if importedCommentActor != importer.ID || importedDependencyActor != importer.ID {
		t.Fatalf("attribution comment=%q dependency=%q importer=%q", importedCommentActor, importedDependencyActor, importer.ID)
	}
	importedNote, err := destination.GetAgentNote(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if importedNote.ActorID != importer.ID || importedNote.Body != note.Body || len(importedNote.Evidence) != 1 || importedNote.Evidence[0] != note.Evidence[0] {
		t.Fatalf("imported note = %+v", importedNote)
	}
	var importedLinks int
	if err := destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_links WHERE source_task_id=? AND target_task_id=? AND link_type=?`, first.ID, bug.ID, "relates").Scan(&importedLinks); err != nil {
		t.Fatal(err)
	}
	if importedLinks != 1 {
		t.Fatalf("imported task links = %d", importedLinks)
	}
	var historyCursor int64
	if err := destinationDB.QueryRowContext(ctx, `SELECT progress_event_cursor FROM task_agent_work_history WHERE id=?`, historyID).Scan(&historyCursor); err != nil {
		t.Fatal(err)
	}
	if historyCursor <= 0 {
		t.Fatalf("imported history cursor = %d", historyCursor)
	}

	secondReport, err := destination.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID})
	if err != nil {
		t.Fatalf("retry archive: %v report=%+v", err, secondReport)
	}
	if secondReport.Counts.TasksCreated != 0 || secondReport.Counts.TasksSkipped != 2 || secondReport.Counts.AgentNotesCreated != 0 || secondReport.Counts.AgentNotesSkipped != 1 || secondReport.Counts.HistoryCreated != 0 || secondReport.Counts.HistorySkipped != 1 {
		t.Fatalf("retry report = %+v", secondReport)
	}
	var taskCount, commentCount, noteCount, historyCount int
	_ = destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=?`, project.ID).Scan(&taskCount)
	_ = destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM comments WHERE task_id=?`, first.ID).Scan(&commentCount)
	_ = destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_notes WHERE task_id=?`, first.ID).Scan(&noteCount)
	_ = destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_agent_work_history WHERE task_id=?`, bug.ID).Scan(&historyCount)
	if taskCount != 2 || commentCount != 1 || noteCount != 1 || historyCount != 1 {
		t.Fatalf("retry duplicated data tasks=%d comments=%d notes=%d history=%d", taskCount, commentCount, noteCount, historyCount)
	}
}
