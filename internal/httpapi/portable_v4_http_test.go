package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

// Separate file-backed databases model an actual installation-to-installation
// transfer; the ordinary testServer helper uses one named shared memory DB.
func portableV4Server(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "synthetic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := store.New(database)
	cfg := config.Config{AuthMode: "disabled", PublicOrigin: "http://helm.test", SecureCookies: false}
	return New(data, auth.NewManager(data, cfg), cfg), data
}

// Failure inventory is in docs/PORTABLE_TRANSFER_V4.md. This real HTTP flow
// precedes the v4 implementation and captures synthetic response receipts.
func TestPortableV4PreservationRoundtrip(t *testing.T) {
	ctx := context.Background()
	source, data := portableV4Server(t)
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PRESERVE"), Name: stringPtr("Synthetic preservation")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Synthetic parent")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Synthetic child")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := "2026-10-29T00:00:00Z"
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE projects SET checklist_completion_policy='require' WHERE id=?`, []any{project.ID}},
		{`INSERT INTO releases(id,project_id,name,description,version,created_at,updated_at) VALUES ('synthetic-focus',?,'Synthetic Focus','',1,?,?)`, []any{project.ID, timestamp, timestamp}},
		{`UPDATE tasks SET release_id='synthetic-focus' WHERE id=?`, []any{child.ID}},
		{`INSERT INTO github_artifacts(id,project_id,repository,repository_id,number,url,created_at) VALUES ('synthetic-pr',?,'example/synthetic',42,7,'https://github.com/example/synthetic/pull/7',?)`, []any{project.ID, timestamp}},
		{`INSERT INTO task_artifacts(task_id,artifact_id,created_at,created_by) VALUES (?,'synthetic-pr',?,?)`, []any{child.ID, timestamp, actor.ID}},
		{`INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,finalized_at,created_at,updated_at) VALUES ('synthetic-audit',?,?,'board','complete',?,?,?,?)`, []any{project.ID, actor.ID, timestamp, timestamp, timestamp, timestamp}},
		{`UPDATE tasks SET parent_task_id=? WHERE id=?`, []any{parent.ID, child.ID}},
		{`INSERT INTO task_checklist_items(id,task_id,text,position,completed,created_at,updated_at) VALUES (?,?,?,0,0,?,?)`, []any{"synthetic-open", child.ID, "Synthetic open", timestamp, timestamp}},
		{`INSERT INTO task_checklist_items(id,task_id,text,position,completed,completed_at,completed_by,created_at,updated_at) VALUES (?,?,?,1,1,?,?,?,?)`, []any{"synthetic-done", child.ID, "Synthetic done", timestamp, actor.ID, timestamp, timestamp}},
	} {
		if _, err := data.DB.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := data.DB.ExecContext(ctx, `INSERT INTO events(id,type,actor_id,project_id,task_id,payload,created_at) VALUES ('synthetic-checklist-event','task.checklist_item_added',?,?,?, ?,?)`, actor.ID, project.ID, child.ID, `{"item_id":"synthetic-open","item_ids":["synthetic-open","synthetic-done"]}`, timestamp); err != nil {
		t.Fatal(err)
	}
	response := request(t, source, http.MethodGet, "/api/v1/projects/"+project.ID+"/export", nil, nil)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var archive map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &archive); err != nil {
		t.Fatal(err)
	}
	if archive["version"] != float64(4) {
		t.Fatalf("default export version=%v; expected lossless v4", archive["version"])
	}
	projects := archive["projects"].([]any)
	if projects[0].(map[string]any)["checklist_completion_policy"] != "require" {
		t.Fatal("require policy absent")
	}
	var exportedChild map[string]any
	for _, raw := range archive["tasks"].([]any) {
		task := raw.(map[string]any)
		if task["id"] == child.ID {
			exportedChild = task
		}
	}
	if exportedChild["parent_task_id"] != parent.ID || len(exportedChild["checklist"].([]any)) != 2 {
		t.Fatalf("lost hierarchy or checklist: %+v", exportedChild)
	}
	// Prove task insertion and parent restoration do not depend on array order.
	tasks := archive["tasks"].([]any)
	tasks[0], tasks[1] = tasks[1], tasks[0]
	destination, dest := portableV4Server(t)
	if _, err := dest.EnsureDisabledActor(ctx); err != nil {
		t.Fatal(err)
	}
	// Seed the original subtree to force deterministic project/task remapping.
	initial := request(t, destination, http.MethodPost, "/api/v1/import", archive, nil)
	if initial.Code != 200 {
		t.Fatal(initial.Body.String())
	}
	if _, err := dest.DB.ExecContext(ctx, `UPDATE projects SET name='Unrelated destination' WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := dest.DB.ExecContext(ctx, `UPDATE tasks SET title='Unrelated destination task' WHERE id=?`, child.ID); err != nil {
		t.Fatal(err)
	}
	before, err := dest.ExportPortable(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview := request(t, destination, http.MethodPost, "/api/v1/import?dry_run=true", archive, nil)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	after, err := dest.ExportPortable(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	before.ExportedAt = ""
	after.ExportedAt = ""
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("preview wrote destination rows")
	}
	imported := request(t, destination, http.MethodPost, "/api/v1/import", archive, map[string]string{"Idempotency-Key": "synthetic-v4-import"})
	if imported.Code != 200 {
		t.Fatal(imported.Body.String())
	}
	var report store.PortableImportReport
	if err := json.Unmarshal(imported.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	mappedProject, mappedParent, mappedChild := project.ID, parent.ID, child.ID
	for _, remap := range report.Remaps {
		if remap.Field != "id" {
			continue
		}
		switch remap.Source {
		case project.ID:
			mappedProject = remap.Target
		case parent.ID:
			mappedParent = remap.Target
		case child.ID:
			mappedChild = remap.Target
		}
	}
	if mappedProject == project.ID || mappedChild == child.ID || mappedParent == parent.ID {
		t.Fatal("collision did not remap subtree")
	}
	retry := request(t, destination, http.MethodPost, "/api/v1/import", archive, nil)
	if retry.Code != 200 {
		t.Fatal(retry.Body.String())
	}
	var retryReport store.PortableImportReport
	_ = json.Unmarshal(retry.Body.Bytes(), &retryReport)
	if retryReport.Counts.TasksCreated != 0 || retryReport.Counts.ProjectsCreated != 0 {
		t.Fatalf("retry duplicated subtree: %+v", retryReport)
	}
	reexport := request(t, destination, http.MethodGet, "/api/v1/projects/"+mappedProject+"/export", nil, nil)
	var roundtrip map[string]any
	if err := json.Unmarshal(reexport.Body.Bytes(), &roundtrip); err != nil {
		t.Fatal(err)
	}
	github := roundtrip["github"].(map[string]any)
	if len(github["artifacts"].([]any)) != 1 || len(github["audit_runs"].([]any)) != 1 || github["task_artifacts"].([]any)[0].(map[string]any)["task_id"] != mappedChild {
		t.Fatal("v4 lost remapped PR/audit history")
	}
	if len(roundtrip["releases"].([]any)) != 1 {
		t.Fatal("v4 lost Focus")
	}
	var mappedOpen string
	for _, raw := range roundtrip["tasks"].([]any) {
		task := raw.(map[string]any)
		if task["id"] == mappedChild {
			if task["parent_task_id"] != mappedParent || len(task["checklist"].([]any)) != 2 {
				t.Fatalf("lost remapped child: %+v", task)
			}
			if task["release_id"] != roundtrip["releases"].([]any)[0].(map[string]any)["id"] {
				t.Fatal("v4 lost Focus membership")
			}
			items := task["checklist"].([]any)
			for i, raw := range items {
				item := raw.(map[string]any)
				original := exportedChild["checklist"].([]any)[i].(map[string]any)
				for _, field := range []string{"text", "position", "completed", "completed_at", "completed_by", "created_at", "updated_at"} {
					if item[field] != original[field] {
						t.Fatalf("checklist %s changed: %v -> %v", field, original[field], item[field])
					}
				}
				if i == 0 {
					mappedOpen = item["id"].(string)
				}
			}
			completion := request(t, destination, http.MethodPost, "/api/v1/tasks/"+mappedChild+"/complete", map[string]any{}, map[string]string{"If-Match": fmt.Sprintf(`"v%v"`, task["version"])})
			if completion.Code != http.StatusConflict || !strings.Contains(completion.Body.String(), "checklist_incomplete") {
				t.Fatalf("completion safeguard lost: %d %s", completion.Code, completion.Body.String())
			}
		}
	}
	for _, raw := range roundtrip["activity"].(map[string]any)["events"].([]any) {
		event := raw.(map[string]any)
		if event["type"] == "task.checklist_item_added" {
			payload := event["payload"].(map[string]any)
			if payload["item_id"] != mappedOpen || payload["item_ids"].([]any)[0] != mappedOpen {
				t.Fatalf("history checklist references lost: %+v", payload)
			}
		}
	}
	var unchanged string
	if err := dest.DB.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id=?`, child.ID).Scan(&unchanged); err != nil || unchanged != "Unrelated destination task" {
		t.Fatal("existing destination changed")
	}
	proof, err := os.MkdirTemp("", "helm-portable-v4-proof-")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"export.json": response.Body.Bytes(), "preview.json": preview.Body.Bytes(), "import.json": imported.Body.Bytes(), "retry.json": retry.Body.Bytes(), "reexport.json": reexport.Body.Bytes()} {
		if err := os.WriteFile(filepath.Join(proof, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Synthetic portable roundtrip receipts: %s", proof)
	verifyPortableV4Rejections(t, source, destination, dest, archive, response.Body.Bytes(), project, parent, child, actor, proof)
}
