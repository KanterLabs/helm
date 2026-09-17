package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestAgentNotesAreBoundedVersionedAndResolvable(t *testing.T) {
	server, data, _, task := progressFixture(t)
	created := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/agent-notes", map[string]any{
		"category": "known_issue", "body": "Node 20 is required.", "evidence": []string{"web/package.json"},
	}, map[string]string{"Idempotency-Key": "note-create-1"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"v1"` {
		t.Fatalf("create note = %d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	var note store.AgentNote
	if err := json.Unmarshal(created.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	if note.Category != "known_issue" || len(note.Evidence) != 1 {
		t.Fatalf("created note = %+v", note)
	}

	edited := request(t, server, http.MethodPatch, "/api/v1/tasks/"+task.ID+"/agent-notes/"+note.ID, map[string]any{
		"category": "constraint", "body": "Use Node 20 for the web build.", "evidence": []string{"web/package.json"},
	}, map[string]string{"If-Match": `"v1"`, "Idempotency-Key": "note-edit-1"})
	if edited.Code != http.StatusOK || edited.Header().Get("ETag") != `"v2"` {
		t.Fatalf("edit note = %d etag=%q body=%s", edited.Code, edited.Header().Get("ETag"), edited.Body.String())
	}
	stale := request(t, server, http.MethodPatch, "/api/v1/tasks/"+task.ID+"/agent-notes/"+note.ID, map[string]any{"category": "constraint", "body": "stale"}, map[string]string{"If-Match": `"v1"`, "Idempotency-Key": "note-stale"})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale edit = %d %s", stale.Code, stale.Body.String())
	}

	for index := 1; index < store.MaxActiveAgentNotes; index++ {
		response := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/agent-notes", map[string]any{"category": "workaround", "body": fmt.Sprintf("Workaround %d", index)}, map[string]string{"Idempotency-Key": fmt.Sprintf("note-create-%d", index+1)})
		if response.Code != http.StatusCreated {
			t.Fatalf("create bounded note %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	overflow := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/agent-notes", map[string]any{"category": "known_issue", "body": "Too many"}, map[string]string{"Idempotency-Key": "note-overflow"})
	if overflow.Code != http.StatusConflict || !strings.Contains(overflow.Body.String(), "limit") {
		t.Fatalf("overflow note = %d %s", overflow.Code, overflow.Body.String())
	}

	resolved := request(t, server, http.MethodDelete, "/api/v1/tasks/"+task.ID+"/agent-notes/"+note.ID, nil, map[string]string{"If-Match": `"v2"`, "Idempotency-Key": "note-resolve"})
	if resolved.Code != http.StatusNoContent || resolved.Header().Get("ETag") != `"v3"` {
		t.Fatalf("resolve note = %d etag=%q body=%s", resolved.Code, resolved.Header().Get("ETag"), resolved.Body.String())
	}
	active := request(t, server, http.MethodGet, "/api/v1/tasks/"+task.ID+"/agent-notes", nil, nil)
	if active.Code != http.StatusOK || strings.Contains(active.Body.String(), note.ID) {
		t.Fatalf("resolved note leaked into active list: %s", active.Body.String())
	}
	all := request(t, server, http.MethodGet, "/api/v1/tasks/"+task.ID+"/agent-notes?include_resolved=true", nil, nil)
	if all.Code != http.StatusOK || !strings.Contains(all.Body.String(), note.ID) || !strings.Contains(all.Body.String(), "resolved_at") {
		t.Fatalf("resolved note missing from history: %s", all.Body.String())
	}
	var retained int
	if err := data.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agent_notes WHERE id=? AND resolved_at IS NOT NULL`, note.ID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("retained resolved note count=%d err=%v", retained, err)
	}
}

func TestAgentNoteInputIsStrictAndTaskScoped(t *testing.T) {
	server, _, project, task := progressFixture(t)
	invalid := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/agent-notes", map[string]any{"category": "guess", "body": "not verified"}, map[string]string{"Idempotency-Key": "note-invalid"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid category = %d %s", invalid.Code, invalid.Body.String())
	}
	other, err := server.Store.CreateTask(t.Context(), project.ID, store.TaskInput{Title: stringPtr("Other task")}, "actor-disabled-mode")
	if err != nil {
		t.Fatal(err)
	}
	created := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/agent-notes", map[string]any{"category": "known_issue", "body": "Scoped note"}, map[string]string{"Idempotency-Key": "note-scoped"})
	var note store.AgentNote
	if err := json.Unmarshal(created.Body.Bytes(), &note); err != nil {
		t.Fatal(err)
	}
	wrong := request(t, server, http.MethodGet, "/api/v1/tasks/"+other.ID+"/agent-notes/"+note.ID, nil, nil)
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("cross-task note read = %d %s", wrong.Code, wrong.Body.String())
	}
}
