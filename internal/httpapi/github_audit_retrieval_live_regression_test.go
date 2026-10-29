package httpapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory from TC-311's actual GitHub/model run: a compatibility
// guard mentions the route but returns false, causing retrieval to stop before
// dispatch. A real path dispatcher calls its handler on the next line; following
// only the route's matching line then misses the behavior in a separate owner.
// The model must receive the pinned router AND handler, without another model
// call or an unrelated compatibility guard satisfying the criterion by itself.
func TestGithubAuditRetrievalWorkerFollowsPathDispatchPastCompatibilityGuard(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("LIVEGAP"), Name: stringPtr("Synthetic live retrieval regression")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic health"), Description: stringPtr("Goal: Provide GET /healthz returning HTTP 200 JSON status ok.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{files: map[string]string{
		retrievalSHA + ":internal/legacy_origin.go": "package app\nfunc legacy(r *Request) bool {\n path := r.URL.Path\n if path == \"/healthz\" || path == \"/readyz\" {\n  return false\n }\n return true\n}\n",
		retrievalSHA + ":internal/router.go":        "package app\nfunc route(w Writer, r *Request) {\n if r.URL.Path == \"/healthz\" {\n  s.health(w, r, false)\n  return\n }\n}\n",
		retrievalSHA + ":internal/server_assets.go": "package app\nfunc (s *Server) health(w Writer, r *Request, ready bool) {\n s.writeJSON(w, 200, map[string]any{\"status\":\"ok\"})\n}\n",
	}, hints: map[string][]string{"/healthz": {"internal/legacy_origin.go", "internal/router.go"}, "health": {"internal/server_assets.go"}}}
	// Real code search interleaves many route mentions in docs before code.
	distractors := []string{}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("docs/operations_%02d.md", i)
		f.files[retrievalSHA+":"+name] = "GET /healthz is reachable; this is not implementation.\n"
		distractors = append(distractors, name)
	}
	f.hints["/healthz"] = append(distractors, f.hints["/healthz"]...)
	ai := &retrievalAI{}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "dispatch-regression", Fingerprint: "dispatch-regression"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	evidence, err := data.GetGithubAuditEvidence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundBody := false
	for _, item := range evidence.Items {
		foundBody = foundBody || item.Path == "internal/server_assets.go" && strings.Contains(item.Text, "writeJSON")
	}
	writeRetrievalReceipt(t, "live-path-dispatch", evidence, f)
	if !foundBody {
		t.Fatal("route mention stopped capture before the actual pinned health handler")
	}
	if ai.calls != 1 {
		t.Fatalf("literal dispatch must not require extra inference: %d", ai.calls)
	}
}

// Actual search next reached a complete leaf handler, but following net/http
// Header().Set and the built-in byte conversion exhausted the source budget.
// These known response primitives are not unresolved repository-owned helpers.
func TestGithubAuditRetrievalWorkerDoesNotChaseResponsePrimitives(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("LEAFROUTE"), Name: stringPtr("Synthetic response leaf")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic health"), Description: stringPtr("Goal: Provide GET /healthz returning HTTP 200 JSON status ok.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{files: map[string]string{
		retrievalSHA + ":internal/public_endpoints.go": "package app\nimport \"net/http\"\nfunc hooks(w http.ResponseWriter, r *http.Request) {\n if r.URL.Path == \"/healthz\" {\n  w.Header().Set(\"Content-Type\", \"application/json\")\n  w.WriteHeader(200)\n  w.Write([]byte(`{\"status\":\"ok\"}`))\n  return\n }\n}\n",
	}, hints: map[string][]string{"/healthz": {"internal/public_endpoints.go"}}}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), &retrievalAI{}, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "response-leaf", Fingerprint: "response-leaf"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	evidence, err := data.GetGithubAuditEvidence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	writeRetrievalReceipt(t, "live-response-leaf", evidence, f)
	if len(evidence.CriterionGaps) != 0 {
		t.Fatalf("known response primitives became missing source: %+v", evidence.CriterionGaps)
	}
	if len(f.queries) != 1 {
		t.Fatalf("chased response primitives: %+v", f.queries)
	}
}
