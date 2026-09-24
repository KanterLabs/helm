package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func TestLunaRunDetailReturnsPrivateContentOnlyToOwner(t *testing.T) {
	server, data := testServer(t, "disabled")
	owner, err := data.CreateActor(context.Background(), store.Actor{Kind: "human", Name: "Owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := data.CreateActor(context.Background(), store.Actor{Kind: "human", Name: "Other"}, "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := data.StartLunaRun(context.Background(), store.LunaRunStart{ActorID: owner.ID, Feature: "task_draft", Model: "gpt-5.6-luna", Effort: "medium"})
	if err != nil {
		t.Fatal(err)
	}
	const input, output = "private exact prompt", "raw invalid output"
	if err := data.SaveLunaRunInput(context.Background(), run.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := data.SaveLunaRunOutput(context.Background(), run.ID, output, false); err != nil {
		t.Fatal(err)
	}

	detail := func(actor store.Actor) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/codex/runs/"+run.ID, nil)
		response := httptest.NewRecorder()
		server.codexAccount(response, req, auth.Identity{Actor: actor}, []string{"runs", run.ID})
		return response
	}
	response := detail(owner)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner detail status=%d cache=%q body=%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["input_text"] != input || body["output_text"] != output || body["content_available"] != true || body["input_truncated"] != false || body["output_truncated"] != false {
		t.Fatalf("owner detail=%#v", body)
	}
	if response := detail(other); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), input) || strings.Contains(response.Body.String(), output) {
		t.Fatalf("cross-actor detail status=%d body=%s", response.Code, response.Body.String())
	}

	list := httptest.NewRecorder()
	server.codexAccount(list, httptest.NewRequest(http.MethodGet, "/api/v1/codex/runs", nil), auth.Identity{Actor: owner}, []string{"runs"})
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), input) || strings.Contains(list.Body.String(), output) {
		t.Fatalf("metadata list status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestTaskDraftCapturesExactPromptAndInvalidRawOutput(t *testing.T) {
	server, data := testServer(t, "disabled")
	actor, err := data.EnsureDisabledActor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("CAPTURE"), Name: stringPtr("Capture")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	const rawOutput = `{"invalid":"raw model output"}`
	fake := &fakeCodexAccounts{draftOutput: rawOutput}
	server.Codex = fake
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/CAPTURE/task-draft", strings.NewReader(`{"query":"capture this exact request"}`))
	response := httptest.NewRecorder()
	server.taskDraft(response, req, auth.Identity{Actor: actor}, project.ID)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("draft status=%d body=%s", response.Code, response.Body.String())
	}
	runs, err := data.ListLunaRuns(context.Background(), actor.ID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	fake.mu.Lock()
	prompt := fake.draftRequests[0].Prompt
	fake.mu.Unlock()
	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/codex/runs/"+runs[0].ID, nil)
	detailResponse := httptest.NewRecorder()
	server.codexAccount(detailResponse, detailRequest, auth.Identity{Actor: actor}, []string{"runs", runs[0].ID})
	var detail map[string]any
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detailResponse.Code != http.StatusOK || detail["input_text"] != prompt || detail["output_text"] != rawOutput || detail["content_available"] != true {
		t.Fatalf("captured detail status=%d body=%s", detailResponse.Code, detailResponse.Body.String())
	}
}
