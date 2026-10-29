package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/store"
)

type assessmentAI struct {
	mutate      func(map[string]any)
	planOutputs []string
	planOutput  string
	plans       int
	infer       int
	afterPlan   func()
	receipts    []json.RawMessage
}

func (a *assessmentAI) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	return codexruntime.AccountStatus{Connected: true}, nil
}
func (a *assessmentAI) DraftEvidence(_ context.Context, _ string, r codexruntime.RunRequest) (codexruntime.RunResult, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return codexruntime.RunResult{}, err
	}
	a.receipts = append(a.receipts, json.RawMessage(r.Prompt))
	if string(input["phase"]) == `"discovery"` {
		a.plans++
		if len(a.planOutputs) >= a.plans {
			return codexruntime.RunResult{Status: "completed", Output: a.planOutputs[a.plans-1]}, nil
		}
		if a.afterPlan != nil {
			a.afterPlan()
		}
		if a.planOutput != "" {
			return codexruntime.RunResult{Status: "completed", Output: a.planOutput}, nil
		}
		return codexruntime.RunResult{Status: "completed", Output: `{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":6,"reason":"Named criterion owner"}],"gap":"Find implementation of the captured condition"}`}, nil
	}
	a.infer++
	var e store.GithubAuditEvidence
	if err := json.Unmarshal(input["evidence"], &e); err != nil {
		return codexruntime.RunResult{}, err
	}
	rows := []map[string]any{}
	var cols []map[string]any
	json.Unmarshal(input["columns"], &cols)
	for _, task := range e.Tasks {
		target := ""
		for _, c := range cols {
			if c["semantic_state"] == "completed" {
				target, _ = c["id"].(string)
			}
		}
		conditions := []map[string]any{}
		for _, c := range task.Conditions {
			refs := []string{}
			for _, item := range e.Items {
				if item.SourceRole == "baseline" && item.EvidenceClass == "implementation" {
					for _, id := range item.CriterionIDs {
						if id == c.ID {
							refs = append(refs, item.ID)
						}
					}
				}
			}
			conditions = append(conditions, map[string]any{"id": c.ID, "text": c.Text, "origin": c.Origin, "result": "supported", "explanation": "Pinned implementation meets the quoted condition.", "citation_ids": refs, "gaps": []string{}})
		}
		placement, action := "correction_recommended", "complete"
		if task.SemanticState == "completed" {
			placement, action = "column_correct", "none"
		}
		row := map[string]any{"task_id": task.ID, "captured_version": task.Version, "source_column_id": task.ColumnID, "conditions": conditions, "placement": placement, "target_column_id": target, "target_semantic_state": "completed", "lifecycle_action": action, "reason": "Committed implementation supports Done.", "basis": []any{}}
		if a.mutate != nil {
			a.mutate(row)
		}
		rows = append(rows, row)
	}
	raw, _ := json.Marshal(map[string]any{"assessment_version": 2, "assessments": rows})
	return codexruntime.RunResult{Status: "completed", Output: string(raw)}, nil
}

func assessmentReceipt(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SYNTHETIC_RETRIEVAL_TOKEN") {
		t.Fatal("credential in evidence")
	}
	dir := "/tmp/helm-tc308-handoff"
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name+".json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Sanitized receipt: %s", path)
}

// Failure inventory is docs/AUDIT_ASSESSMENT_FAILURES.md, written first.
// This exercises real HTTP start/findings, provider HTTP and worker publication.
func TestGithubAssessmentHTTPWorker(t *testing.T) {
	activeAssessment := func(result, placement, action string) func(map[string]any) {
		return func(r map[string]any) {
			c := r["conditions"].([]map[string]any)[0]
			c["result"] = result
			if result == "unknown" {
				c["gaps"] = []string{"Runtime behavior was not inspected"}
			}
			refs := c["citation_ids"]
			r["basis"] = []map[string]any{
				{"kind": "implementation", "explanation": "Some baseline implementation was read", "citation_ids": refs},
				{"kind": "work_remaining", "explanation": "Required behavior remains unverified", "citation_ids": refs},
			}
			r["placement"], r["lifecycle_action"] = placement, action
			// The server binds the captured destination; the model supplies its meaning.
			delete(r, "target_column_id")
			r["target_semantic_state"] = "active"
			if placement == "unable_to_verify" {
				r["target_semantic_state"] = nil
				r["basis"] = []any{}
			}
		}
	}
	cases := []struct {
		name, state string
		mutate      func(map[string]any)
		fail        bool
	}{
		{"complete-backlog", "backlog", nil, false},
		{"correct-despite-unread", "completed", nil, false},
		{"unknown-is-not-correct-active", "active", activeAssessment("unknown", "column_correct", "none"), true},
		{"unknown-cannot-reopen", "completed", activeAssessment("unknown", "correction_recommended", "reopen"), true},
		{"contradicted-correct-active", "active", activeAssessment("contradicted", "column_correct", "none"), false},
		{"unknown-active-unable", "active", activeAssessment("unknown", "unable_to_verify", "none"), false},
		{"unknown-is-not-done", "backlog", func(r map[string]any) { r["conditions"].([]map[string]any)[0]["result"] = "unknown" }, true},
		{"invented-citation", "backlog", func(r map[string]any) {
			r["conditions"].([]map[string]any)[0]["citation_ids"] = []string{"invented:path"}
		}, true},
		{"rewritten-quote", "backlog", func(r map[string]any) { r["conditions"].([]map[string]any)[0]["text"] = "Invented requirement" }, true},
		{"wrong-task-version", "backlog", func(r map[string]any) { r["captured_version"] = int64(999) }, true},
		{"wrong-destination", "backlog", func(r map[string]any) { r["target_column_id"] = "invented-column" }, true},
		{"missing-condition", "backlog", func(r map[string]any) { r["conditions"] = []any{} }, true},
		{"unknown-unable", "backlog", func(r map[string]any) {
			c := r["conditions"].([]map[string]any)[0]
			c["result"] = "unknown"
			c["citation_ids"] = []string{}
			c["gaps"] = []string{"Relevant behavior remains unknown"}
			r["placement"] = "unable_to_verify"
			r["target_column_id"] = nil
			r["target_semantic_state"] = nil
			r["lifecycle_action"] = "none"
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("ASSESS"), Name: stringPtr("Synthetic assessment")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic source"), Description: stringPtr("Goal: Implement internal/opaque.go.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.state == "completed" {
				task, err = data.CompleteTask(ctx, task.ID, actor.ID, task.Version)
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "active" {
				// Fixture placement only; the audit itself must leave this version intact.
				_, err = data.DB.ExecContext(ctx, `UPDATE tasks SET column_id=(SELECT id FROM columns WHERE project_id=? AND semantic_state='active') WHERE id=?`, p.ID, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				task, err = data.GetTask(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n", retrievalSHA + ":unrelated.png": "irrelevant unread image"}}
			ai := &assessmentAI{mutate: tc.mutate}
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
			s.GithubAudits = service
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					s.githubAuditStart(w, r, auth.Identity{Actor: actor}, p.ID)
				} else {
					s.auditFindings(w, r, auth.Identity{Actor: actor}, strings.TrimPrefix(r.URL.Path, "/"))
				}
			}))
			defer server.Close()
			req, _ := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
			req.Header.Set("Idempotency-Key", tc.name)
			req.Header.Set("Content-Type", "application/json")
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
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, _ := data.GetGithubAuditJob(ctx, started.Audit.ID)
			run, _ := data.GetAuditRun(ctx, started.Audit.ID)
			response, err := http.Get(server.URL + "/" + started.Audit.ID)
			if err != nil {
				t.Fatal(err)
			}
			var public map[string]any
			json.NewDecoder(response.Body).Decode(&public)
			response.Body.Close()
			assessmentReceipt(t, tc.name, map[string]any{"job": job, "response": public, "model_boundary": ai.receipts, "reads": f.reads})
			if tc.fail {
				if job.ErrorCode != "invalid_model_output" || run.FindingCount != 0 {
					t.Fatalf("invalid output published: %+v count=%d", job, run.FindingCount)
				}
				return
			}
			if job.Outcome == "failed" || run.FindingCount != 1 {
				t.Fatalf("valid assessment failed %+v count=%d", job, run.FindingCount)
			}
			raw, _ := json.Marshal(public)
			if !strings.Contains(string(raw), `"assessment_version":2`) || !strings.Contains(string(raw), `"application_eligibility"`) {
				t.Fatalf("missing public v2 contract: %s", raw)
			}
			if ai.plans != 0 || ai.infer != 1 {
				t.Fatalf("literal capture made unnecessary planning calls: %d/%d", ai.plans, ai.infer)
			}
			after, _ := data.GetTask(ctx, task.ID)
			if after.Version != task.Version {
				t.Fatal("assessment changed task")
			}
		})
	}
}

func TestGithubAssessmentRejectsMalformedVersionWithoutFallback(t *testing.T) {
	for _, raw := range []string{`{"assessment_version":2,"findings":[]}`, `{"assessment_version":3,"assessments":[]}`, `{"assessment_version":2,"assessments":[]} {}`} {
		if _, err := decodeGithubAssessmentOutput(raw); err == nil {
			t.Fatalf("accepted malformed v2: %s", raw)
		}
	}
	_ = fmt.Sprint // keep receipts easy to extend without changing fixture behavior
}

func TestGithubAssessmentOpenAPIContract(t *testing.T) {
	var document struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &document); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GithubAssessment", "GithubConditionAssessment", "GithubAuditCondition", "GithubAuditCriterionGap", "GithubAuditContext", "GithubAuditBaseline"} {
		if _, ok := document.Components.Schemas[name]; !ok {
			t.Errorf("missing public schema %s", name)
		}
	}
}
