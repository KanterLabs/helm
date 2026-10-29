package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Additional failure discovered during destination-plan review: an otherwise
// valid archive can exceed a reused parent's fanout only at the destination.
// Preview must detect this before SQL triggers reject an eventual write.
func TestPortableV4DestinationHierarchyLimit(t *testing.T) {
	ctx := context.Background()
	source, data := portableV4Server(t)
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("LIMIT"), Name: stringPtr("Synthetic fanout")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Synthetic parent")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	seed := request(t, source, http.MethodGet, "/api/v1/projects/"+project.ID+"/export", nil, nil)
	var initial map[string]any
	_ = json.Unmarshal(seed.Body.Bytes(), &initial)
	child, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Synthetic new child"), ParentTaskID: &parent.ID}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = child
	exported := request(t, source, http.MethodGet, "/api/v1/projects/"+project.ID+"/export", nil, nil)
	var archive map[string]any
	_ = json.Unmarshal(exported.Body.Bytes(), &archive)
	destination, dest := portableV4Server(t)
	if _, err := dest.EnsureDisabledActor(ctx); err != nil {
		t.Fatal(err)
	}
	imported := request(t, destination, http.MethodPost, "/api/v1/import", initial, nil)
	if imported.Code != 200 {
		t.Fatal(imported.Body.String())
	}
	for i := 0; i < 200; i++ {
		title := fmt.Sprintf("Synthetic existing child %d", i)
		existing, err := dest.CreateTask(ctx, project.ID, store.TaskInput{Title: &title}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dest.DB.ExecContext(ctx, `UPDATE tasks SET parent_task_id=? WHERE id=?`, parent.ID, existing.ID); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := dest.ExportPortable(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline.ExportedAt = ""
	proof, err := os.MkdirTemp("", "helm-portable-v4-limit-proof-")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"?dry_run=true", ""} {
		response := request(t, destination, http.MethodPost, "/api/v1/import"+suffix, archive, nil)
		if err := os.WriteFile(filepath.Join(proof, "rejection"+strings.ReplaceAll(suffix, "?", "-")+".json"), response.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if response.Code != 400 || !strings.Contains(response.Body.String(), "200") {
			t.Fatalf("destination fanout not rejected during planning: %d %s", response.Code, response.Body.String())
		}
	}
	after, err := dest.ExportPortable(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	after.ExportedAt = ""
	t.Logf("Synthetic destination hierarchy rejection receipts: %s", proof)
	if !reflect.DeepEqual(baseline, after) {
		t.Fatal("failed hierarchy import changed destination")
	}
}
