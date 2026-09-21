package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestProjectIntelligenceGETIsDeterministicAndNeverInvokesCodex(t *testing.T) {
	server, data := testServer(t, "disabled")
	actor, _ := data.EnsureDisabledActor(context.Background())
	quiet, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("QUIET"), Name: stringPtr("Quiet")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	urgent, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("URGENT"), Name: stringPtr("Urgent")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	priority := "urgent"
	if _, err := data.CreateTask(context.Background(), urgent.ID, store.TaskInput{Title: stringPtr("Urgent work"), Priority: &priority}, actor.ID); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCodexAccounts{}
	server.Codex = fake

	response := request(t, server, http.MethodGet, "/api/v1/project-intelligence", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result ProjectIntelligenceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Source != "deterministic" || len(result.Projects) != 2 || result.Projects[0].ProjectID != urgent.ID || result.Projects[1].ProjectID != quiet.ID {
		t.Fatalf("result=%+v", result)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.draftRequests) != 0 || len(fake.draftActors) != 0 {
		t.Fatalf("GET invoked Codex: requests=%d actors=%v", len(fake.draftRequests), fake.draftActors)
	}
}

func TestProjectIntelligenceOutputSchemaAvoidsUnsupportedUniqueItems(t *testing.T) {
	var schema any
	if err := json.Unmarshal(projectIntelligenceOutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if _, unsupported := typed["uniqueItems"]; unsupported {
				t.Fatal("project intelligence schema contains unsupported uniqueItems")
			}
			for _, child := range typed {
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(schema)
}

func TestProjectIntelligenceAnalyzeRequiresExplicitPOSTAndCachesResult(t *testing.T) {
	server, data := testServer(t, "disabled")
	actor, _ := data.EnsureDisabledActor(context.Background())
	project, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("SMART"), Name: stringPtr("Smart")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeCodexAccounts{draftOutput: fmt.Sprintf(`{"projects":[{"project_id":%q,"attention":"watch","reason_codes":["quiet"],"summary":"Review the current plan.","confidence":"medium"}],"workspace_insights":[{"kind":"planning_hygiene","project_ids":[%q],"summary":"The workspace would benefit from a planning review."}]}`, project.ID, project.ID)}
	server.Codex = fake

	before := request(t, server, http.MethodGet, "/api/v1/project-intelligence", nil, nil)
	if before.Code != http.StatusOK || !strings.Contains(before.Body.String(), `"source":"deterministic"`) {
		t.Fatalf("before=%d %s", before.Code, before.Body.String())
	}
	analyzed := request(t, server, http.MethodPost, "/api/v1/project-intelligence/analyze", nil, nil)
	if analyzed.Code != http.StatusOK || !strings.Contains(analyzed.Body.String(), `"source":"luna"`) {
		t.Fatalf("analyzed=%d %s", analyzed.Code, analyzed.Body.String())
	}
	after := request(t, server, http.MethodGet, "/api/v1/project-intelligence", nil, nil)
	if after.Code != http.StatusOK || !strings.Contains(after.Body.String(), `"source":"luna"`) {
		t.Fatalf("after=%d %s", after.Code, after.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.draftRequests) != 1 || fake.draftRequests[0].Effort != "low" || fake.draftActors[0] != actor.ID {
		t.Fatalf("drafts=%+v actors=%v", fake.draftRequests, fake.draftActors)
	}
}

func TestProjectIntelligenceAnalyzeRejectsBodyWithoutInvokingCodex(t *testing.T) {
	server, _ := testServer(t, "disabled")
	fake := &fakeCodexAccounts{}
	server.Codex = fake

	response := request(t, server, http.MethodPost, "/api/v1/project-intelligence/analyze", map[string]any{"retry": true}, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.draftRequests) != 0 || len(fake.draftActors) != 0 {
		t.Fatalf("body-bearing request invoked Codex: requests=%d actors=%v", len(fake.draftRequests), fake.draftActors)
	}
}

func TestProjectIntelligenceAnalyzeInvalidOutputIsNotRetried(t *testing.T) {
	server, data := testServer(t, "disabled")
	actor, _ := data.EnsureDisabledActor(context.Background())
	if _, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("SAFE"), Name: stringPtr("Safe")}, actor.ID); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCodexAccounts{draftOutput: `{"projects":[],"workspace_insights":[]}`}
	server.Codex = fake

	response := request(t, server, http.MethodPost, "/api/v1/project-intelligence/analyze", nil, nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"luna_invalid_output"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	read := request(t, server, http.MethodGet, "/api/v1/project-intelligence", nil, nil)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"source":"deterministic"`) {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.draftRequests) != 1 {
		t.Fatalf("invalid output retried: requests=%d", len(fake.draftRequests))
	}
}
