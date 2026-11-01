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
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

type auditFlowAI struct {
	task         string
	calls        int
	entered      chan struct{}
	release      chan struct{}
	invented     bool
	disconnected bool
}

func (a *auditFlowAI) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	return codexruntime.AccountStatus{Connected: !a.disconnected, AccountType: "chatgpt"}, nil
}
func (a *auditFlowAI) DraftEvidence(_ context.Context, _ string, r codexruntime.RunRequest) (codexruntime.RunResult, error) {
	a.calls++
	if !strings.Contains(r.Prompt, "synthetic source") || strings.Contains(r.Prompt, "SENTINEL_PROVIDER_TOKEN") {
		panic("invalid evidence boundary")
	}
	if a.entered != nil {
		close(a.entered)
		<-a.release
	}
	var input struct {
		Evidence store.GithubAuditEvidence `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		panic(err)
	}
	findings := []map[string]any{}
	for _, task := range input.Evidence.Tasks {
		finding := map[string]any{"task_id": task.ID, "verdict": "correct", "confidence": 0.8, "reason": "Synthetic branch evidence reviewed.", "evidence_refs": []string{"github:file:1"}}
		if task.ID == a.task {
			finding["verdict"] = "move_proposed"
			finding["proposed_semantic_destination"] = "ready"
		}
		if a.invented {
			finding["evidence_refs"] = []string{"github:file:invented"}
		}
		findings = append(findings, finding)
	}
	output, _ := json.Marshal(map[string]any{"findings": findings})
	return codexruntime.RunResult{Status: "completed", Output: string(output)}, nil
}
func TestGithubAuditRealHTTPBranchFlow(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GHFLOW"), Name: stringPtr("Synthetic audit")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic branch task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err = data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr(fmt.Sprintf("Synthetic task %d", i))}, actor.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() {
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"synthetic permission denied"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			w.Write([]byte(`{"tree":[{"path":"main.go","type":"blob","mode":"100644","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":16}],"truncated":false}`))
		case strings.Contains(r.URL.Path, "/contents/"):
			w.Write([]byte(`{"type":"file","encoding":"base64","content":"c3ludGhldGljIHNvdXJjZQ==","size":16,"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
		case strings.Contains(r.URL.Path, "/commits/"):
			w.Write([]byte(`{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		default:
			w.Write([]byte(`{"id":1,"full_name":"example/synthetic","default_branch":"main","permissions":{"pull":true}}`))
		}
	}))
	defer fake.Close()
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("SENTINEL_PROVIDER_TOKEN"), 0600)
	provider := githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: token, Repositories: []string{"example/synthetic"}, HTTPClient: fake.Client(), Endpoint: fake.URL})
	ai := &auditFlowAI{task: task.ID}
	service := NewGithubAuditService(data, provider, ai, nil)
	s.GithubAudits = service
	identity := auth.Identity{Actor: actor}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 1 && parts[0] == "start" {
			s.githubAuditStart(w, r, identity, p.ID)
			return
		}
		if len(parts) == 3 && parts[0] == "audit" {
			if parts[2] == "findings" {
				s.auditFindings(w, r, identity, parts[1])
				return
			}
			s.githubAuditJob(w, r, identity, parts[1], parts[2])
			return
		}
		if len(parts) == 2 && parts[0] == "finding" {
			s.auditFinding(w, r, identity, parts[1])
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "synthetic-branch")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 202 {
		t.Fatalf("start: %d", response.StatusCode)
	}
	var started GithubAuditStartResponse
	if err = json.NewDecoder(response.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	replay, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{}`))
	replay.Header.Set("Content-Type", "application/json")
	replay.Header.Set("Idempotency-Key", "synthetic-branch")
	replayResponse, err := http.DefaultClient.Do(replay)
	if err != nil {
		t.Fatal(err)
	}
	var replayed GithubAuditStartResponse
	json.NewDecoder(replayResponse.Body).Decode(&replayed)
	replayResponse.Body.Close()
	if replayResponse.StatusCode != 202 || replayed.Audit.ID != started.Audit.ID {
		t.Fatal("duplicate HTTP start created another run")
	}
	conflicting, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{}`))
	conflicting.Header.Set("Content-Type", "application/json")
	conflicting.Header.Set("Idempotency-Key", "active-conflict")
	conflictResponse, err := http.DefaultClient.Do(conflicting)
	if err != nil {
		t.Fatal(err)
	}
	conflictBody, _ := io.ReadAll(conflictResponse.Body)
	conflictResponse.Body.Close()
	if conflictResponse.StatusCode != 409 || !strings.Contains(string(conflictBody), started.Audit.ID) {
		t.Fatalf("active conflict %d %s", conflictResponse.StatusCode, conflictBody)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := data.GetGithubAuditJob(ctx, started.Audit.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Outcome != "complete" || ai.calls != 1 || job.TaskCount != 51 {
		t.Fatalf("job=%+v calls=%d", job, ai.calls)
	}
	evidence, err := data.GetGithubAuditEvidence(ctx, job.AuditID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Tasks) != 51 || evidence.Tasks[0].SemanticState == "" || evidence.Tasks[0].ColumnName == "" {
		t.Fatal("missing task placement capture")
	}
	if len(evidence.Items) != 1 || evidence.Repositories[0].SHA != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("evidence=%+v", evidence)
	}
	unchanged, _ := data.GetTask(ctx, task.ID)
	if unchanged.Version != task.Version {
		t.Fatal("audit mutated task")
	}
	call := func(method, path, body, etag string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, raw
	}
	status, raw := call("GET", "/audit/"+job.AuditID+"/findings", "", "")
	if status != 200 {
		t.Fatalf("findings %d %s", status, raw)
	}
	var collection struct {
		Data []store.AuditFinding `json:"data"`
	}
	if err = json.Unmarshal(raw, &collection); err != nil {
		t.Fatal(err)
	}
	var finding store.AuditFinding
	for _, candidate := range collection.Data {
		if candidate.TaskID == task.ID {
			finding = candidate
		}
	}
	// The target may be on the second findings page; capture IDs are independent
	// of the board and findings collection's first page.
	if finding.ID == "" {
		all, _, _ := data.ListAuditFindings(ctx, job.AuditID, 200, 0)
		for _, candidate := range all {
			if candidate.TaskID == task.ID {
				finding = candidate
			}
		}
	}
	status, raw = call("PATCH", "/finding/"+finding.ID, `{"review_state":"approved"}`, `"v1"`)
	if status != 200 {
		t.Fatalf("approval %d %s", status, raw)
	}
	approved, _ := data.GetTask(ctx, task.ID)
	if approved.Version != task.Version {
		t.Fatal("approval moved task")
	}
	payload := fmt.Sprintf(`{"finding_ids":[%q],"preview":true}`, finding.ID)
	status, raw = call("POST", "/audit/"+job.AuditID+"/apply", payload, "")
	if status != 200 || !strings.Contains(string(raw), `"eligible"`) {
		t.Fatalf("preview %d %s", status, raw)
	}
	status, raw = call("POST", "/audit/"+job.AuditID+"/apply", strings.Replace(payload, "true", "false", 1), "")
	if status != 200 || !strings.Contains(string(raw), `"applied"`) {
		t.Fatalf("apply %d %s", status, raw)
	}
	var leakedEventReasons int
	if err = data.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE type='task.moved' AND payload LIKE '%Synthetic branch evidence reviewed%'`).Scan(&leakedEventReasons); err != nil {
		t.Fatal(err)
	}
	if leakedEventReasons != 0 {
		t.Fatal("private finding reason leaked into general activity")
	}
	moved, _ := data.GetTask(ctx, task.ID)
	if moved.Version != task.Version+1 {
		t.Fatal("explicit apply did not move once")
	}
	revoked.Store(true)
	status, raw = call("GET", "/audit/"+job.AuditID+"/evidence", "", "")
	if status != 409 || strings.Contains(string(raw), "synthetic source") {
		t.Fatalf("revoked evidence %d %s", status, raw)
	}
	status, raw = call("GET", "/audit/"+job.AuditID+"/findings", "", "")
	if status != 409 || strings.Contains(string(raw), "Synthetic branch evidence reviewed") {
		t.Fatalf("revoked findings %d %s", status, raw)
	}
	status, raw = call("PATCH", "/finding/"+finding.ID, `{"review_state":"dismissed"}`, `"v2"`)
	if status != 409 {
		t.Fatalf("revoked review %d %s", status, raw)
	}
	revoked.Store(false)
	// Rotating the synthetic file invalidates the provider's observed denial.
	if err = os.WriteFile(token, []byte("SENTINEL_PROVIDER_TOKEN_ROTATED"), 0600); err != nil {
		t.Fatal(err)
	}
	ai.entered = make(chan struct{})
	ai.release = make(chan struct{})
	retryReq, _ := http.NewRequest("POST", server.URL+"/audit/"+job.AuditID+"/retry", strings.NewReader(`{}`))
	retryReq.Header.Set("Idempotency-Key", "explicit-retry")
	retryReq.Header.Set("Content-Type", "application/json")
	retryRes, err := http.DefaultClient.Do(retryReq)
	if err != nil {
		t.Fatal(err)
	}
	var retried GithubAuditStartResponse
	if retryRes.StatusCode != 202 {
		raw, _ := io.ReadAll(retryRes.Body)
		t.Fatalf("retry %d %s", retryRes.StatusCode, raw)
	}
	json.NewDecoder(retryRes.Body).Decode(&retried)
	retryRes.Body.Close()
	finished := make(chan error, 1)
	go func() { finished <- service.RunOnce(ctx) }()
	<-ai.entered
	status, raw = call("POST", "/audit/"+retried.Audit.ID+"/cancel", `{}`, "")
	if status != 200 {
		t.Fatalf("cancel %d %s", status, raw)
	}
	close(ai.release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	cancelled, _ := data.GetGithubAuditJob(ctx, retried.Audit.ID)
	if cancelled.Outcome != "cancelled" || cancelled.RetryOf != job.AuditID {
		t.Fatalf("cancel/retry %+v", cancelled)
	}
	cancelledRun, _ := data.GetAuditRun(ctx, retried.Audit.ID)
	if cancelledRun.FindingCount != 0 || cancelledRun.Status != "failed" {
		t.Fatal("late inference published after cancel")
	}
	ai.entered = nil
	ai.release = nil
	ai.invented = true
	invalidReq, _ := http.NewRequest("POST", server.URL+"/start", strings.NewReader(`{}`))
	invalidReq.Header.Set("Idempotency-Key", "invented-citation")
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRes, err := http.DefaultClient.Do(invalidReq)
	if err != nil {
		t.Fatal(err)
	}
	var invalid GithubAuditStartResponse
	json.NewDecoder(invalidRes.Body).Decode(&invalid)
	invalidRes.Body.Close()
	if invalidRes.StatusCode != 202 {
		t.Fatalf("invalid-output start %d", invalidRes.StatusCode)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	rejected, _ := data.GetGithubAuditJob(ctx, invalid.Audit.ID)
	rejectedRun, _ := data.GetAuditRun(ctx, invalid.Audit.ID)
	if rejected.Outcome != "failed" || rejected.ErrorCode != "invalid_model_output" || rejectedRun.FindingCount != 0 {
		t.Fatalf("invented citation accepted %+v %+v", rejected, rejectedRun)
	}
	ai.disconnected = true
	missingAI, _ := http.NewRequest("POST", server.URL+"/start", strings.NewReader(`{}`))
	missingAI.Header.Set("Idempotency-Key", "missing-ai")
	missingAI.Header.Set("Content-Type", "application/json")
	missingRes, err := http.DefaultClient.Do(missingAI)
	if err != nil {
		t.Fatal(err)
	}
	missingRaw, _ := io.ReadAll(missingRes.Body)
	missingRes.Body.Close()
	if missingRes.StatusCode != 409 || !strings.Contains(string(missingRaw), "ai_not_connected") {
		t.Fatalf("missing AI %d %s", missingRes.StatusCode, missingRaw)
	}
	artifact, _ := json.MarshalIndent(map[string]any{"job": job, "evidence": evidence, "cancelled_retry": cancelled, "applied_task": moved, "rejected_citation": rejected}, "", "  ")
	if strings.Contains(string(artifact), "SENTINEL_PROVIDER_TOKEN") {
		t.Fatal("credential leak")
	}
	dir, err := os.MkdirTemp("", "helm-github-audit-proof-")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "github-audit-response.json")
	if err = os.WriteFile(path, artifact, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Synthetic response artifact: %s", path)
}
