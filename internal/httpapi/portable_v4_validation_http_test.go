package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Rejection and legacy compatibility checks reuse the populated HTTP flow;
// keep them separate from the successful preservation/remapping assertions.
func verifyPortableV4Rejections(t *testing.T, source, destination *Server, dest *store.Store, archive map[string]any, exported []byte, project store.Project, parent, child store.Task, actor store.Actor, proof string) {
	ctx := context.Background()
	// Explicit old archives stay readable, but cannot safely retry onto richer
	// versions of the same task without reconstructing omitted fields.
	for _, version := range []string{"2", "3"} {
		legacy := request(t, source, http.MethodGet, "/api/v1/projects/"+project.ID+"/export?version="+version, nil, nil)
		var body map[string]any
		_ = json.Unmarshal(legacy.Body.Bytes(), &body)
		if len(body["warnings"].([]any)) == 0 {
			t.Fatal("legacy export omission warning absent")
		}
		rejected := request(t, destination, http.MethodPost, "/api/v1/import", body, nil)
		if rejected.Code != 409 || !strings.Contains(rejected.Body.String(), "legacy") {
			t.Fatalf("legacy retry duplicated or hid richer task: %d %s", rejected.Code, rejected.Body.String())
		}
	}
	legacyExport := request(t, source, http.MethodGet, "/api/v1/projects/"+project.ID+"/export?version=3", nil, nil)
	var legacy map[string]any
	_ = json.Unmarshal(legacyExport.Body.Bytes(), &legacy)
	requireProject, err := dest.CreateProject(ctx, store.ProjectInput{Key: stringPtr("LEGACY"), Name: stringPtr("Synthetic legacy target"), ChecklistCompletionPolicy: stringPtr("require")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	accepted := request(t, destination, http.MethodPost, "/api/v1/projects/"+requireProject.ID+"/import", legacy, nil)
	if accepted.Code != 200 || !strings.Contains(accepted.Body.String(), "versions 1-3 omit") {
		t.Fatalf("explicit legacy support lost: %d %s", accepted.Code, accepted.Body.String())
	}
	var retainedPolicy string
	if err := dest.DB.QueryRowContext(ctx, `SELECT checklist_completion_policy FROM projects WHERE id=?`, requireProject.ID).Scan(&retainedPolicy); err != nil || retainedPolicy != "require" {
		t.Fatal("legacy import weakened target policy")
	}
	if err := os.WriteFile(filepath.Join(proof, "legacy-import.json"), accepted.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	warnProject, err := dest.CreateProject(ctx, store.ProjectInput{Key: stringPtr("WARN"), Name: stringPtr("Synthetic warn target")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	policyMismatch := request(t, destination, http.MethodPost, "/api/v1/projects/"+warnProject.ID+"/import", archive, nil)
	if policyMismatch.Code != 400 || !strings.Contains(policyMismatch.Body.String(), "checklist_completion_policy") {
		t.Fatalf("weakened project policy: %d %s", policyMismatch.Code, policyMismatch.Body.String())
	}
	// Invalid relations are rejected by preview and import, preserving all rows.
	for name, mutate := range map[string]func(map[string]any){
		"missing parent": func(a map[string]any) { a["tasks"].([]any)[0].(map[string]any)["parent_task_id"] = "missing" },
		"self parent": func(a map[string]any) {
			task := a["tasks"].([]any)[0].(map[string]any)
			task["parent_task_id"] = task["id"]
		},
		"cycle": func(a map[string]any) {
			for _, raw := range a["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["id"] == parent.ID {
					task["parent_task_id"] = child.ID
				}
			}
		},
		"cross project": func(a map[string]any) {
			p := map[string]any{}
			for k, v := range a["projects"].([]any)[0].(map[string]any) {
				p[k] = v
			}
			p["id"] = "synthetic-other"
			p["key"] = "OTHER"
			p["slug"] = "synthetic-other"
			a["projects"] = append(a["projects"].([]any), p)
			for _, raw := range a["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["id"] == parent.ID {
					task["project_id"] = "synthetic-other"
					c := map[string]any{}
					for _, col := range a["columns"].([]any) {
						if col.(map[string]any)["id"] == task["column_id"] {
							for k, v := range col.(map[string]any) {
								c[k] = v
							}
						}
					}
					c["id"] = "synthetic-other-column"
					c["project_id"] = "synthetic-other"
					a["columns"] = append(a["columns"].([]any), c)
					task["column_id"] = "synthetic-other-column"
				}
			}
		},
		"duplicate checklist": func(a map[string]any) {
			for _, raw := range a["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["id"] == child.ID {
					items := task["checklist"].([]any)
					items[1].(map[string]any)["id"] = items[0].(map[string]any)["id"]
				}
			}
		},
		"invalid completion": func(a map[string]any) {
			for _, raw := range a["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["id"] == child.ID {
					task["checklist"].([]any)[0].(map[string]any)["completed_by"] = actor.ID
				}
			}
		},
		"invalid checklist": func(a map[string]any) {
			for _, raw := range a["tasks"].([]any) {
				task := raw.(map[string]any)
				if task["id"] == child.ID {
					task["checklist"].([]any)[0].(map[string]any)["text"] = ""
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var invalid map[string]any
			_ = json.Unmarshal(exported, &invalid)
			mutate(invalid)
			baseline, err := dest.ExportPortable(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			baseline.ExportedAt = ""
			for _, suffix := range []string{"?dry_run=true", ""} {
				rejected := request(t, destination, http.MethodPost, "/api/v1/import"+suffix, invalid, nil)
				if err := os.WriteFile(filepath.Join(proof, strings.ReplaceAll(name, " ", "_")+strings.ReplaceAll(suffix, "?", "-")+".json"), rejected.Body.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				if rejected.Code != 400 {
					t.Fatalf("malformed archive accepted: %d %s", rejected.Code, rejected.Body.String())
				}
			}
			unchanged, err := dest.ExportPortable(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			unchanged.ExportedAt = ""
			if !reflect.DeepEqual(baseline, unchanged) {
				t.Fatal("rejected archive changed destination state")
			}
		})
	}
}
