package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Failure inventory (written before retrieval): incidental audit prose/README
// promises can displace a route's non-obvious owner; huge unrelated blobs can
// consume the shared budget; early cards can starve later cards; repeated cards
// can reread shared blobs; missing permission and exhausted discovery must name
// only affected criteria; default-branch search/PR code must not become baseline
// proof; changed blobs, revoked grants and cancellation must fence every read.
func TestGithubAuditRetrievalHTTPWorkerFindsHiddenRoute(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("RETRIEVE"), Name: stringPtr("Synthetic retrieval")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []store.Task{}
	for _, goal := range []string{"Goal: Provide GET /healthz for the audit demonstration.", "Goal: Provide GET /healthz.", "Goal: Provide GET /readyz."} {
		task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic criterion"), Description: &goal}, actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	if _, err = data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{files: map[string]string{
		retrievalSHA + ":internal/audit_fixture.go": "package app\nvar cardDescription = \"Goal: Provide GET /healthz for the audit demonstration.\"\n",
		retrievalSHA + ":README.md":                 "Promises GET /healthz and GET /readyz for audit demonstration.\n",
		retrievalSHA + ":docs/audit.md":             strings.Repeat("audit demonstration ", 9000),
		retrievalSHA + ":internal/zinc.go":          "package app\nfunc wire(mux *Mux) {\n mux.HandleFunc(\"GET /healthz\", pulse)\n}\n",
		retrievalSHA + ":internal/opaque.go":        "package app\nfunc pulse(w Writer, r *Request) {\n w.WriteHeader(200)\n w.Write([]byte(`{\"ok\":true}`))\n}\n",
		retrievalSHA + ":internal/quartz.go":        "package app\nfunc wireReady(mux *Mux) {\n mux.HandleFunc(\"GET /readyz\", ready)\n}\nfunc ready(w Writer,r *Request) { w.WriteHeader(200) }\n",
	}, hints: map[string][]string{"/healthz": {"internal/audit_fixture.go", "internal/zinc.go", "README.md"}, "pulse": {"internal/opaque.go"}, "/readyz": {"internal/quartz.go"}}}
	// Many plausible but unrelated owners exceed the old file budget and occur
	// before the true owner. Only server-filtered content hints identify it.
	for i := 0; i < 60; i++ {
		f.files[fmt.Sprintf("%s:internal/audit_%02d.go", retrievalSHA, i)] = "package app\n// audit demonstration\n"
	}
	provider := newRetrievalProvider(t, f)
	ai := &retrievalAI{}
	service := NewGithubAuditService(data, provider, ai, nil)
	s.GithubAudits = service
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.githubAuditStart(w, r, auth.Identity{Actor: actor}, p.ID)
	}))
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "criterion-capture")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var started GithubAuditStartResponse
	if err = json.NewDecoder(res.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 202 {
		t.Fatalf("start %d", res.StatusCode)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := data.GetGithubAuditJob(ctx, started.Audit.ID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := data.GetGithubAuditEvidence(ctx, job.AuditID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Outcome == "failed" || ai.calls != 1 {
		t.Errorf("worker failed or added inference rounds: %+v calls=%d", job, ai.calls)
	}
	for _, want := range []string{"internal/zinc.go", "internal/opaque.go", "internal/quartz.go"} {
		found := false
		for _, item := range e.Items {
			if item.Path == want {
				found = true
				if len(item.TaskIDs) == 0 {
					t.Errorf("%s unbound to card", want)
				}
				if item.SHA != retrievalSHA || item.BlobSHA != retrievalBlob || item.StartLine < 1 {
					t.Error("lost pinned excerpt identity")
				}
			}
		}
		if !found {
			t.Errorf("missing criterion owner %s", want)
		}
	}
	for _, item := range e.Items {
		if strings.HasPrefix(item.Path, "docs/") || strings.Contains(item.Path, "audit_") {
			t.Errorf("incidental prose captured: %s", item.Path)
		}
	}
	if f.reads[retrievalSHA+":docs/audit.md"] != 0 {
		t.Error("huge unrelated document consumed reads")
	}
	if f.reads[retrievalSHA+":internal/zinc.go"] != 1 {
		t.Errorf("shared route reread %d times", f.reads[retrievalSHA+":internal/zinc.go"])
	}
	raw, _ := json.Marshal(e)
	if !strings.Contains(string(raw), `"criterion_ids"`) {
		t.Error("evidence lacks criterion identities")
	}
	if len(f.queries) == 0 {
		t.Error("never performed content discovery")
	}
	if !e.Partial || e.OmittedFiles == 0 {
		t.Error("repository coverage must disclose unread unrelated files")
	}
	if len(e.CriterionGaps) != 0 {
		t.Errorf("unread unrelated files contaminated supported criterion capture: %+v", e.CriterionGaps)
	}
	writeRetrievalReceipt(t, "hidden-route", e, f)
}

func TestGithubAuditRetrievalRelevantGapsAndPRSeparation(t *testing.T) {
	for _, mode := range []string{"unavailable", "exhausted", "pr_only", "cancellation", "ambiguous", "readme_only", "explicit_missing", "excerpt_limit", "multiple_routes", "absent_path"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GAPS"), Name: stringPtr("Synthetic gaps")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			goal := "Goal: Implement GET /healthz."
			if mode == "ambiguous" {
				goal = "Improve things eventually."
			}
			if mode == "multiple_routes" {
				goal = "Goal: Implement GET /healthz and GET /missingz."
			}
			if mode == "absent_path" {
				goal = "Goal: Implement internal/absent.go."
			}
			if mode == "explicit_missing" {
				goal = "Goal: Update internal/a.go and internal/b.go."
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Health route"), Description: &goal}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			f := &retrievalFixture{files: map[string]string{}, hints: map[string][]string{"/healthz": {"internal/opaque.go"}}, denied: map[string]bool{}}
			if mode == "readme_only" {
				f.files[retrievalSHA+":README.md"] = "GET /healthz is promised.\n"
				f.hints["/healthz"] = []string{"README.md"}
			} else if mode == "explicit_missing" {
				f.files[retrievalSHA+":internal/a.go"] = "package app\nfunc first() {}\n"
				f.files[retrievalSHA+":internal/b.go"] = "package app\nfunc second() {}\n"
				f.denied[retrievalSHA+":internal/b.go"] = true
			} else if mode == "excerpt_limit" || mode == "multiple_routes" {
				f.files[retrievalSHA+":internal/opaque.go"] = "package app\nfunc route(m *Mux) { m.HandleFunc(\"GET /healthz\", pulse) }\nfunc pulse() {}\n"
			} else if mode == "pr_only" {
				_, task, err = data.LinkGithubArtifact(ctx, task.ID, "example/synthetic", 7, "https://github.com/example/synthetic/pull/7", task.Version, actor.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				f.files[retrievalHead+":internal/opaque.go"] = "package app\nfunc routes(m *Mux) { m.HandleFunc(\"GET /healthz\", pulse) }\n"
			} else if mode == "unavailable" {
				f.files[retrievalSHA+":internal/opaque.go"] = "package app\n// route\n"
				f.denied[retrievalSHA+":internal/opaque.go"] = true
			} else {
				for i := 0; i < 120; i++ {
					f.files[fmt.Sprintf("%s:internal/owner%03d.go", retrievalSHA, i)] = "package app\nfunc unrelated() {}\n"
				}
			}
			provider := newRetrievalProvider(t, f)
			service := NewGithubAuditService(data, provider, &retrievalAI{}, nil)
			run, job, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, RequestKey: mode, Fingerprint: mode})
			if err != nil {
				t.Fatal(err)
			}
			c, err := data.GetGithubAuditCapture(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			checks := 0
			check := func() error {
				checks++
				if mode == "cancellation" && checks >= 3 {
					return fmt.Errorf("synthetic canceled fence")
				}
				return nil
			}
			var e store.GithubAuditEvidence
			if mode == "excerpt_limit" {
				b := defaultRetrievalBudget()
				b.ExcerptBytes = 16
				e, err = service.captureWithBudget(ctx, job, c, check, b)
			} else {
				e, err = service.capture(ctx, job, c, check)
			}
			if mode == "cancellation" {
				if err == nil {
					t.Fatal("capture ignored cancellation fence")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(e)
			var receipt struct {
				Gaps []struct {
					TaskID      string `json:"task_id"`
					CriterionID string `json:"criterion_id"`
					Code        string `json:"code"`
					SourceRole  string `json:"source_role"`
				} `json:"criterion_gaps"`
			}
			json.Unmarshal(raw, &receipt)
			baselineGap := false
			for _, gap := range receipt.Gaps {
				if gap.TaskID == task.ID && (gap.CriterionID != "" || mode == "ambiguous") && gap.SourceRole == "baseline" {
					baselineGap = true
				}
			}
			if !e.Partial {
				t.Error("criterion gap must remain partial")
			}
			if mode == "absent_path" {
				named := false
				for _, gap := range e.CriterionGaps {
					if gap.Code == "explicit_path_unavailable" && gap.Path == "internal/absent.go" {
						named = true
					}
				}
				if !named {
					t.Error("missing explicit absent-path gap")
				}
			}
			if mode == "multiple_routes" {
				named := false
				for _, gap := range e.CriterionGaps {
					if gap.Code == "criterion_anchor_unresolved" {
						named = true
					}
				}
				if !named {
					t.Error("one matched route hid another required route")
				}
			}
			if !baselineGap {
				t.Errorf("missing relevant baseline criterion gap: %s", raw)
			}
			if mode == "pr_only" {
				found := false
				for _, item := range e.Items {
					if item.Path == "internal/opaque.go" {
						found = true
						if item.SHA != retrievalHead {
							t.Error("PR-only implementation presented at baseline")
						}
					}
				}
				if !found {
					t.Error("missing separate PR-head implementation")
				}
				if !strings.Contains(string(raw), `"source_role":"linked_pr"`) {
					t.Error("PR evidence lost source role")
				}
			}
			if len(f.reads) > 96 {
				t.Errorf("unbounded file reads: %d", len(f.reads))
			}
			writeRetrievalReceipt(t, mode, e, f)
		})
	}
}
