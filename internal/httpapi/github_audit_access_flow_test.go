package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// Contents can remain authorized after a check/PR permission is revoked. Real
// HTTP evidence and finding reads must revalidate the source types they expose.
func TestGithubAuditHTTPCheckPermissionRevocation(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GHACCESS"), Name: stringPtr("Synthetic check access")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Review synthetic PR")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err = data.LinkGithubArtifact(ctx, task.ID, "example/synthetic", 7, "https://github.com/example/synthetic/pull/7", task.Version, actor.ID, true); err != nil {
		t.Fatal(err)
	}
	var denied atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			if denied.Load() {
				w.WriteHeader(403)
				w.Write([]byte(`{"message":"synthetic check permission revoked"}`))
				return
			}
			fmt.Fprint(w, `{"total_count":1,"check_runs":[{"name":"synthetic tests","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status":"completed","conclusion":"success","html_url":"https://github.com/example/synthetic/actions/runs/1"}]}`)
		case strings.HasSuffix(r.URL.Path, "/statuses"), strings.HasSuffix(r.URL.Path, "/reviews"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			fmt.Fprint(w, `{"title":"Synthetic private PR","state":"open","head":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"base":{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","repo":{"id":1}}}`)
		case strings.Contains(r.URL.Path, "/git/trees/"):
			fmt.Fprint(w, `{"tree":[{"path":"main.go","type":"blob","mode":"100644","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":16}],"truncated":false}`)
		case strings.Contains(r.URL.Path, "/contents/"):
			fmt.Fprint(w, `{"type":"file","encoding":"base64","content":"c3ludGhldGljIHNvdXJjZQ==","size":16,"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
		case strings.Contains(r.URL.Path, "/commits/"):
			fmt.Fprint(w, `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
		default:
			fmt.Fprint(w, `{"id":1,"full_name":"example/synthetic","default_branch":"main","permissions":{"pull":true}}`)
		}
	}))
	defer fake.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err = os.WriteFile(token, []byte("SENTINEL_PROVIDER_TOKEN"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: token, Repositories: []string{"example/synthetic"}, HTTPClient: fake.Client(), Endpoint: fake.URL})
	ai := &auditFlowAI{task: task.ID}
	service := NewGithubAuditService(data, provider, ai, nil)
	s.GithubAudits = service
	identity := auth.Identity{Actor: actor}
	var auditID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			s.githubAuditStart(w, r, identity, p.ID)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/findings") {
			s.auditFindings(w, r, identity, auditID)
			return
		}
		s.githubAuditJob(w, r, identity, auditID, "evidence")
	}))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "check-permission")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var started GithubAuditStartResponse
	json.NewDecoder(res.Body).Decode(&started)
	res.Body.Close()
	if res.StatusCode != 202 {
		t.Fatalf("start %d", res.StatusCode)
	}
	auditID = started.Audit.ID
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := data.GetGithubAuditJob(ctx, auditID)
	if job.Outcome != "complete" {
		t.Fatalf("job %+v", job)
	}
	get := func(path string) (int, []byte) {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, raw
	}
	captured, err := data.GetGithubAuditEvidence(ctx, auditID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range captured.Items {
		if item.Kind == "pull_request" || item.Kind == "check" {
			if len(item.TaskIDs) != 1 || item.TaskIDs[0] != task.ID {
				t.Fatal("linked source lost task association")
			}
		}
	}
	status, raw := get("/evidence")
	if status != 200 || !strings.Contains(string(raw), "Synthetic private PR") {
		t.Fatalf("initial evidence %d %s", status, raw)
	}
	denied.Store(true)
	// The provider's branch/Contents probe still succeeds, reproducing the split.
	if _, err = provider.Resolve(ctx, "example/synthetic", "main"); err != nil {
		t.Fatal(err)
	}
	status, raw = get("/evidence")
	if status != 409 || strings.Contains(string(raw), "Synthetic private PR") {
		t.Fatalf("revoked check evidence %d %s", status, raw)
	}
	status, raw = get("/findings")
	if status != 409 || strings.Contains(string(raw), "Synthetic branch evidence reviewed") {
		t.Fatalf("known-denied findings %d %s", status, raw)
	}
	proof, _ := json.MarshalIndent(map[string]any{"audit_id": auditID, "job": job, "revoked_evidence_status": status, "sanitized_error": json.RawMessage(raw)}, "", "  ")
	dir, err := os.MkdirTemp("", "helm-github-check-access-proof-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "response.json")
	if err = os.WriteFile(path, proof, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Synthetic split-permission response artifact: %s", path)
}
