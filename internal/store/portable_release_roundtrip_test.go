package store

import (
	"context"
	"errors"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"testing"
)

func TestPortableReleaseV2RoundTripAndLegacyV1Assignment(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	sourceDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	source := New(sourceDB)
	owner, err := source.CreateActor(ctx, Actor{Kind: "human", Name: "Release owner"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := source.CreateProject(ctx, ProjectInput{Key: portableTestString("REL"), Slug: portableTestString("release-source"), Name: portableTestString("Release source")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := source.ListColumns(ctx, project.ID)
	if err != nil || len(columns) == 0 {
		t.Fatalf("source columns = %d, err=%v", len(columns), err)
	}
	task, err := source.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Release task"), ColumnID: &columns[0].ID}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID := "portable-release"
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO releases(id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, releaseID, project.ID, "1.4", "Search and release planning", "2026-11-15", nil, nil, 3, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(ctx, `UPDATE tasks SET release_id=? WHERE id=?`, releaseID, task.ID); err != nil {
		t.Fatal(err)
	}

	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Version != 2 || len(archive.Releases) != 1 || archive.Releases[0].Status != "planned" {
		t.Fatalf("v2 release export = version %d releases=%+v", archive.Version, archive.Releases)
	}
	if len(archive.Tasks) != 1 || archive.Tasks[0].ReleaseID == nil || *archive.Tasks[0].ReleaseID != releaseID {
		t.Fatalf("v2 task release export = %+v", archive.Tasks)
	}

	destinationDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	destination := New(destinationDB)
	importer, err := destination.CreateActor(ctx, Actor{Kind: "human", Name: "Release importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	report, err := destination.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID})
	if err != nil {
		t.Fatalf("v2 release import: %v report=%+v", err, report)
	}
	if report.Counts.ReleasesCreated != 1 || report.Counts.TasksCreated != 1 {
		t.Fatalf("v2 release import report = %+v", report)
	}
	var importedReleaseID, importedTaskReleaseID string
	if err := destinationDB.QueryRowContext(ctx, `SELECT id FROM releases WHERE project_id=?`, project.ID).Scan(&importedReleaseID); err != nil {
		t.Fatal(err)
	}
	if err := destinationDB.QueryRowContext(ctx, `SELECT release_id FROM tasks WHERE id=?`, task.ID).Scan(&importedTaskReleaseID); err != nil {
		t.Fatal(err)
	}
	if importedReleaseID != releaseID || importedTaskReleaseID != releaseID {
		t.Fatalf("imported release binding release=%q task=%q", importedReleaseID, importedTaskReleaseID)
	}

	// v1 archives predate releases. Even if a caller-populated struct carries
	// release-shaped fields, the importer must leave every task unassigned and
	// must not create release rows.
	legacy := archive
	legacy.Version = 1
	legacy.Tasks = append([]PortableTask(nil), archive.Tasks...)
	legacy.Releases = append([]PortableRelease(nil), archive.Releases...)
	legacyReport, err := destination.ImportPortable(ctx, legacy, PortableImportOptions{ActorID: importer.ID, TargetProjectID: project.ID})
	if err != nil {
		t.Fatalf("v1 release import: %v report=%+v", err, legacyReport)
	}
	if legacyReport.Counts.ReleasesCreated != 0 || legacyReport.Counts.TasksCreated != 1 {
		t.Fatalf("v1 release import report = %+v", legacyReport)
	}
	var unassigned int
	if err := destinationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=? AND release_id IS NULL`, project.ID).Scan(&unassigned); err != nil {
		t.Fatal(err)
	}
	if unassigned != 1 {
		t.Fatalf("v1 import unassigned task count = %d", unassigned)
	}
}

func TestPortableReleaseImportRejectsCrossProjectReferenceBeforeWrites(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	firstProject := PortableProject{ID: "release-project-a", Key: "RELA", Slug: "release-a", Name: "Release A", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}
	secondProject := PortableProject{ID: "release-project-b", Key: "RELB", Slug: "release-b", Name: "Release B", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{firstProject, secondProject},
		Columns: []PortableColumn{
			{ID: "release-column-a", ProjectID: firstProject.ID, Name: "Backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp},
			{ID: "release-column-b", ProjectID: secondProject.ID, Name: "Backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp},
		},
		Releases: []PortableRelease{{ID: "release-b", ProjectID: secondProject.ID, Name: "2.0", Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Tasks:    []PortableTask{{ID: "release-task-a", Number: 1, ProjectID: firstProject.ID, Kind: "task", ColumnID: "release-column-a", Title: "Wrong project release", Priority: "normal", ReleaseID: portableTestString("release-b"), Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	report, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-project release import error=%v report=%+v, want ErrInvalid", err, report)
	}
	var projects, releases, tasks int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM projects`: &projects,
		`SELECT COUNT(*) FROM releases`: &releases,
		`SELECT COUNT(*) FROM tasks`:    &tasks,
	} {
		if err := database.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if projects != 0 || releases != 0 || tasks != 0 {
		t.Fatalf("cross-project validation wrote projects=%d releases=%d tasks=%d", projects, releases, tasks)
	}
}

func TestPortableReleaseImportRejectsReservedNamesBeforeWrites(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	for _, name := range []string{"unassigned", "NONE", " Unassigned "} {
		t.Run(name, func(t *testing.T) {
			database, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-reserved.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			data := New(database)
			actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
			if err != nil {
				t.Fatal(err)
			}
			project := PortableProject{ID: "release-project", Key: "REL", Slug: "release-project", Name: "Release project", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}
			archive := PortableArchive{
				Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
				Projects: []PortableProject{project},
				Columns:  []PortableColumn{},
				Releases: []PortableRelease{{ID: "reserved-release", ProjectID: project.ID, Name: name, Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
				Tasks:    []PortableTask{}, Labels: []PortableLabel{}, Comments: []PortableComment{},
				Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
				Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
			}
			report, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("reserved release import error=%v report=%+v, want ErrInvalid", err, report)
			}
			var projects, releases int
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM releases`).Scan(&releases); err != nil {
				t.Fatal(err)
			}
			if projects != 0 || releases != 0 {
				t.Fatalf("reserved-name validation wrote projects=%d releases=%d", projects, releases)
			}
		})
	}
}

func TestPortableReleasedReleaseRoundTripStagesMembership(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	sourceDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "released-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sourceDB.Close()
	source := New(sourceDB)
	owner, err := source.CreateActor(ctx, Actor{Kind: "human", Name: "Release owner"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	project, err := source.CreateProject(ctx, ProjectInput{Key: portableTestString("RELR"), Slug: portableTestString("released-source"), Name: portableTestString("Released source")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := source.StateColumn(ctx, project.ID, "completed")
	if err != nil {
		t.Fatal(err)
	}
	task, err := source.CreateTask(ctx, project.ID, TaskInput{Title: portableTestString("Released task"), ColumnID: &completed.ID}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	releaseID := "portable-released-release"
	if _, err := sourceDB.ExecContext(ctx, `INSERT INTO releases(id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, releaseID, project.ID, "1.5", "Released planning", "2026-11-15", nil, nil, 4, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceDB.ExecContext(ctx, `UPDATE tasks SET release_id=? WHERE id=?`, releaseID, task.ID); err != nil {
		t.Fatal(err)
	}
	releasedAt := "2026-09-05T00:00:00Z"
	if _, err := sourceDB.ExecContext(ctx, `UPDATE releases SET released_at=?,released_by=?,updated_at=? WHERE id=?`, releasedAt, owner.ID, releasedAt, releaseID); err != nil {
		t.Fatal(err)
	}

	archive, err := source.ExportPortableVersion(ctx, []string{project.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.Releases) != 1 || archive.Releases[0].Status != "released" || archive.Releases[0].ReleasedAt == nil || *archive.Releases[0].ReleasedAt != releasedAt {
		t.Fatalf("released export = %+v", archive.Releases)
	}

	destinationDB, err := db.Open(ctx, filepath.Join(t.TempDir(), "released-destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destinationDB.Close()
	destination := New(destinationDB)
	importer, err := destination.CreateActor(ctx, Actor{Kind: "human", Name: "Release importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	report, err := destination.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID})
	if err != nil {
		t.Fatalf("released import: %v report=%+v", err, report)
	}
	var importedReleasedAt, importedReleasedBy string
	if err := destinationDB.QueryRowContext(ctx, `SELECT released_at,released_by FROM releases WHERE id=?`, releaseID).Scan(&importedReleasedAt, &importedReleasedBy); err != nil {
		t.Fatal(err)
	}
	if importedReleasedAt != releasedAt || importedReleasedBy != importer.ID {
		t.Fatalf("imported release completion released_at=%q released_by=%q importer=%q", importedReleasedAt, importedReleasedBy, importer.ID)
	}
	var importedTaskRelease string
	if err := destinationDB.QueryRowContext(ctx, `SELECT release_id FROM tasks WHERE id=?`, task.ID).Scan(&importedTaskRelease); err != nil {
		t.Fatal(err)
	}
	if importedTaskRelease != releaseID {
		t.Fatalf("imported released task release_id=%q", importedTaskRelease)
	}
}
