package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/db"
	"path/filepath"
	"testing"
)

func TestPortableReleaseImportRemapsProjectAndReleaseIDs(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-remap.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Importer"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	target, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("RELTARGET"), Slug: portableTestString("release-target"), Name: portableTestString("Release target")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO releases(id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, "release-shared-id", target.ID, "Existing", "Retained destination release", nil, nil, nil, 1, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}
	sourceProjectID, sourceColumnID, sourceReleaseID := "release-source-project", "release-source-column", "release-shared-id"
	archive := PortableArchive{
		Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
		Projects: []PortableProject{{ID: sourceProjectID, Key: "RELSOURCE", Slug: "release-source", Name: "Release source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
		Columns:  []PortableColumn{{ID: sourceColumnID, ProjectID: sourceProjectID, Name: "Imported backlog", SemanticState: "backlog", Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Releases: []PortableRelease{{ID: sourceReleaseID, ProjectID: sourceProjectID, Name: "1.4", Description: "Imported release", Version: 2, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Tasks:    []PortableTask{{ID: "release-remap-task", Number: 1, ProjectID: sourceProjectID, Kind: "task", ColumnID: sourceColumnID, Title: "Bound task", Priority: "normal", ReleaseID: portableTestString(sourceReleaseID), Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp}},
		Labels:   []PortableLabel{}, Comments: []PortableComment{},
		Relationships: PortableRelationships{TaskLabels: []PortableTaskLabel{}, Dependencies: []PortableDependency{}, TaskLinks: []PortableTaskLink{}},
		Activity:      PortableActivity{Events: []PortableEvent{}, AgentWork: []PortableAgentWork{}, AgentWorkHistory: []PortableAgentWorkHistory{}},
	}
	report, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: actor.ID, TargetProjectID: target.ID})
	if err != nil {
		t.Fatalf("release remap import: %v report=%+v", err, report)
	}
	if report.Counts.ReleasesCreated != 1 || !portableReportHasRemap(report, "release", "id") || !portableReportHasRemap(report, "project", "id") {
		t.Fatalf("release remap report = %+v", report)
	}
	var importedReleaseID, importedProjectID string
	if err := database.QueryRowContext(ctx, `SELECT release_id,project_id FROM tasks WHERE id=?`, "release-remap-task").Scan(&importedReleaseID, &importedProjectID); err != nil {
		t.Fatal(err)
	}
	if importedProjectID != target.ID || importedReleaseID == sourceReleaseID || importedReleaseID == "" {
		t.Fatalf("remapped task project=%q release=%q target project=%q", importedProjectID, importedReleaseID, target.ID)
	}
	var importedReleaseProject string
	if err := database.QueryRowContext(ctx, `SELECT project_id FROM releases WHERE id=?`, importedReleaseID).Scan(&importedReleaseProject); err != nil {
		t.Fatal(err)
	}
	if importedReleaseProject != target.ID {
		t.Fatalf("remapped release project=%q target project=%q", importedReleaseProject, target.ID)
	}
}

func TestPortableReleaseNameCollisionPreservesBothLifecyclesAndEventPayloadIDs(t *testing.T) {
	ctx := context.Background()
	timestamp := "2026-09-04T00:00:00Z"
	cases := []struct {
		name                string
		sourceReleased      bool
		destinationReleased bool
	}{
		{name: "planned source versus released destination", sourceReleased: false, destinationReleased: true},
		{name: "released source versus planned destination", sourceReleased: true, destinationReleased: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.Open(ctx, filepath.Join(t.TempDir(), "release-collision.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			data := New(database)
			importer, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Release importer"}, "hash")
			if err != nil {
				t.Fatal(err)
			}
			target, err := data.CreateProject(ctx, ProjectInput{Key: portableTestString("COLLIDE"), Slug: portableTestString("release-collision"), Name: portableTestString("Release collision")}, importer.ID)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := data.ListColumns(ctx, target.ID)
			if err != nil || len(columns) == 0 {
				t.Fatalf("target columns = %d, err=%v", len(columns), err)
			}

			const releaseID = "collision-release"
			const taskID = "collision-task"
			const releaseName = "2.0"
			const projectID = "collision-source-project"
			const columnID = "collision-source-column"
			const actorID = "collision-source-actor"
			destinationReleasedAtValue := any(nil)
			destinationReleasedByValue := any(nil)
			if tc.destinationReleased {
				destinationReleasedAtValue = "2026-09-03T00:00:00Z"
				destinationReleasedByValue = importer.ID
			}
			if _, err := database.ExecContext(ctx, `INSERT INTO releases(id,project_id,name,description,target_date,released_at,released_by,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, releaseID, target.ID, releaseName, "destination metadata", nil, destinationReleasedAtValue, destinationReleasedByValue, 1, timestamp, timestamp); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ExecContext(ctx, `INSERT INTO tasks(id,project_id,number,column_id,kind,title,description,priority,position,version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, taskID, target.ID, 1, columns[0].ID, "task", "destination task", "destination metadata", "normal", 0, 1, timestamp, timestamp); err != nil {
				t.Fatal(err)
			}

			sourceReleasedAt := (*string)(nil)
			sourceReleasedBy := (*string)(nil)
			taskCompletedAt := (*string)(nil)
			semanticState := "backlog"
			if tc.sourceReleased {
				releasedAt := "2026-09-05T00:00:00Z"
				sourceReleasedAt = &releasedAt
				sourceReleasedBy = portableTestString(actorID)
				taskCompletedAt = &releasedAt
				semanticState = "completed"
			}
			sourceReleaseID := releaseID
			sourceTaskID := taskID
			sourceProjectID := projectID
			sourceActorID := actorID
			eventOneProjectID, eventOneActorID := sourceProjectID, sourceActorID
			eventTwoProjectID, eventTwoTaskID, eventTwoActorID := sourceProjectID, sourceTaskID, sourceActorID
			eventOnePayload, _ := json.Marshal(map[string]any{
				"project_id":  sourceProjectID,
				"release_id":  sourceReleaseID,
				"actor_id":    sourceActorID,
				"released_by": sourceActorID,
			})
			eventTwoPayload, _ := json.Marshal(map[string]any{
				"project_id":          sourceProjectID,
				"task_id":             sourceTaskID,
				"release_id":          sourceReleaseID,
				"previous_release_id": sourceReleaseID,
				"new_release_id":      sourceReleaseID,
				"old_release_id":      sourceReleaseID,
				"actor_id":            sourceActorID,
			})
			archive := PortableArchive{
				Format: PortableFormat, Version: PortableVersion, ExportedAt: timestamp,
				Projects: []PortableProject{{ID: sourceProjectID, Key: "COLLSRC", Slug: "collision-source", Name: "Collision source", Color: "#64748b", CreatedAt: timestamp, UpdatedAt: timestamp}},
				Columns:  []PortableColumn{{ID: columnID, ProjectID: sourceProjectID, Name: "Imported column", SemanticState: semanticState, Position: 0, CreatedAt: timestamp, UpdatedAt: timestamp}},
				Releases: []PortableRelease{{ID: sourceReleaseID, ProjectID: sourceProjectID, Name: releaseName, Description: "source metadata", Version: 2, ReleasedAt: sourceReleasedAt, ReleasedBy: sourceReleasedBy, CreatedAt: timestamp, UpdatedAt: timestamp}},
				Tasks:    []PortableTask{{ID: sourceTaskID, Number: 1, ProjectID: sourceProjectID, Kind: "task", ColumnID: columnID, Title: "source task", Description: "source metadata", Priority: "normal", Position: 0, Version: 1, CompletedAt: taskCompletedAt, ReleaseID: &sourceReleaseID, CreatedAt: timestamp, UpdatedAt: timestamp}},
				Activity: PortableActivity{Events: []PortableEvent{
					{Cursor: 1, ID: "collision-release-event", Type: "release.completed", ActorID: &eventOneActorID, ProjectID: &eventOneProjectID, Payload: eventOnePayload, CreatedAt: timestamp},
					{Cursor: 2, ID: "collision-task-release-event", Type: "task.release_changed", ActorID: &eventTwoActorID, ProjectID: &eventTwoProjectID, TaskID: &eventTwoTaskID, Payload: eventTwoPayload, CreatedAt: timestamp},
				}},
			}

			report, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID, TargetProjectID: target.ID})
			if err != nil {
				t.Fatalf("release collision import: %v report=%+v", err, report)
			}
			if report.Counts.ReleasesCreated != 1 || report.Counts.TasksCreated != 1 || report.Counts.EventsCreated != 2 {
				t.Fatalf("release collision report = %+v", report)
			}
			if !portableReportHasRemap(report, "release", "name") || !portableReportHasRemap(report, "release", "id") || !portableReportHasRemap(report, "task", "id") || !portableReportHasRemap(report, "project", "id") {
				t.Fatalf("release collision remaps = %+v", report.Remaps)
			}

			var importedReleaseID string
			for _, remap := range report.Remaps {
				if remap.Entity == "release" && remap.Field == "id" && remap.Source == sourceReleaseID {
					importedReleaseID = remap.Target
					break
				}
			}
			if importedReleaseID == "" || importedReleaseID == sourceReleaseID {
				t.Fatalf("imported release id remap = %q, report=%+v", importedReleaseID, report.Remaps)
			}
			var importedReleaseName, importedDescription string
			var importedReleasedAt, importedReleasedBy sql.NullString
			if err := database.QueryRowContext(ctx, `SELECT name,description,released_at,released_by FROM releases WHERE id=?`, importedReleaseID).Scan(&importedReleaseName, &importedDescription, &importedReleasedAt, &importedReleasedBy); err != nil {
				t.Fatal(err)
			}
			if importedReleaseName == releaseName || importedDescription != "source metadata" {
				t.Fatalf("imported release metadata name=%q description=%q", importedReleaseName, importedDescription)
			}
			if tc.sourceReleased != importedReleasedAt.Valid || (tc.sourceReleased && importedReleasedBy.String != importer.ID) {
				t.Fatalf("imported release status released_at=%q released_by=%q sourceReleased=%t", importedReleasedAt.String, importedReleasedBy.String, tc.sourceReleased)
			}
			var destinationDescription string
			var destinationReleasedAt sql.NullString
			if err := database.QueryRowContext(ctx, `SELECT description,released_at FROM releases WHERE id=?`, releaseID).Scan(&destinationDescription, &destinationReleasedAt); err != nil {
				t.Fatal(err)
			}
			if destinationDescription != "destination metadata" || destinationReleasedAt.Valid != tc.destinationReleased {
				t.Fatalf("destination release changed description=%q released=%q", destinationDescription, destinationReleasedAt.String)
			}

			var importedTaskID, importedTaskReleaseID string
			if err := database.QueryRowContext(ctx, `SELECT id,release_id FROM tasks WHERE title=?`, "source task").Scan(&importedTaskID, &importedTaskReleaseID); err != nil {
				t.Fatal(err)
			}
			if importedTaskID == sourceTaskID || importedTaskReleaseID != importedReleaseID {
				t.Fatalf("imported task id=%q release=%q want release=%q", importedTaskID, importedTaskReleaseID, importedReleaseID)
			}

			var releasePayload, taskPayload string
			if err := database.QueryRowContext(ctx, `SELECT payload FROM events WHERE id=?`, "collision-release-event").Scan(&releasePayload); err != nil {
				t.Fatal(err)
			}
			if err := database.QueryRowContext(ctx, `SELECT payload FROM events WHERE id=?`, "collision-task-release-event").Scan(&taskPayload); err != nil {
				t.Fatal(err)
			}
			var releasePayloadValues, taskPayloadValues map[string]any
			if err := json.Unmarshal([]byte(releasePayload), &releasePayloadValues); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(taskPayload), &taskPayloadValues); err != nil {
				t.Fatal(err)
			}
			if releasePayloadValues["project_id"] != target.ID || releasePayloadValues["release_id"] != importedReleaseID || releasePayloadValues["actor_id"] != importer.ID || releasePayloadValues["released_by"] != importer.ID {
				t.Fatalf("release event payload = %#v", releasePayloadValues)
			}
			if taskPayloadValues["project_id"] != target.ID || taskPayloadValues["task_id"] != importedTaskID || taskPayloadValues["release_id"] != importedReleaseID || taskPayloadValues["previous_release_id"] != importedReleaseID || taskPayloadValues["new_release_id"] != importedReleaseID || taskPayloadValues["old_release_id"] != importedReleaseID || taskPayloadValues["actor_id"] != importer.ID {
				t.Fatalf("task release event payload = %#v", taskPayloadValues)
			}

			retry, err := data.ImportPortable(ctx, archive, PortableImportOptions{ActorID: importer.ID, TargetProjectID: target.ID})
			if err != nil {
				t.Fatalf("release collision retry: %v report=%+v", err, retry)
			}
			if retry.Counts.ReleasesCreated != 0 || retry.Counts.TasksCreated != 0 || retry.Counts.EventsCreated != 0 || retry.Counts.ReleasesSkipped != 1 || retry.Counts.TasksSkipped != 1 || retry.Counts.EventsSkipped != 2 {
				t.Fatalf("release collision retry report = %+v", retry)
			}
		})
	}
}
