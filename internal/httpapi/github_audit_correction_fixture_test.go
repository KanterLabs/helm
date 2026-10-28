package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

type correctionFixture struct {
	s        *Server
	data     *store.Store
	actor    store.Actor
	project  store.Project
	task     store.Task
	finding  store.AuditFinding
	job      store.GithubAuditJob
	remote   *retrievalFixture
	server   *httptest.Server
	identity auth.Identity
}

func newCorrectionFixture(t *testing.T, action string, extras ...int) *correctionFixture {
	t.Helper()
	s, d := testServer(t, "disabled")
	ctx := context.Background()
	a, err := d.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.CreateProject(ctx, store.ProjectInput{Key: stringPtr("CORRECT"), Name: stringPtr("Synthetic corrections")}, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := store.TaskInput{Title: stringPtr("Synthetic correction"), Description: stringPtr("Goal: Implement internal/opaque.go.")}
	if strings.HasPrefix(action, "bug_") {
		input.Kind = stringPtr("bug")
		input.Bug = &store.BugInput{ActualBehavior: stringPtr("Synthetic regression")}
	}
	task, err := d.CreateTask(ctx, p.ID, input, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var active string
	if err = d.DB.QueryRow(`SELECT id FROM columns WHERE project_id=? AND semantic_state='active'`, p.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	task, err = d.UpdateTask(ctx, task.ID, store.TaskInput{ColumnID: &active}, task.Version, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if action == "reopen" {
		task, err = d.CompleteTask(ctx, task.ID, a.ID, task.Version)
	}
	if action == "bug_reopen" {
		task, err = d.ResolveBug(ctx, task.ID, store.ResolveBugInput{Resolution: "fixed"}, task.Version, a.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if action == "complete-dependency" {
		prerequisite, e := d.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic prerequisite"), Description: stringPtr("Goal: Implement internal/opaque.go.")}, a.ID)
		if e != nil {
			t.Fatal(e)
		}
		prerequisite, e = d.CompleteTask(ctx, prerequisite.ID, a.ID, prerequisite.Version)
		if e != nil {
			t.Fatal(e)
		}
		task, e = d.AddTaskDependency(ctx, task.ID, prerequisite.ID, task.Version, a.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	if action == "complete-custom" {
		if _, err = d.DB.Exec(`UPDATE columns SET name='Verified and shipped',version=version+1 WHERE project_id=? AND semantic_state='completed'`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if action == "complete-checklist" {
		if _, err = d.DB.Exec(`UPDATE projects SET checklist_completion_policy='require' WHERE id=?`, p.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = d.DB.Exec(`INSERT INTO task_checklist_items(id,task_id,text,completed,position,created_at,updated_at) VALUES('required-item',?,'Implement internal/opaque.go.',0,0,'now','now')`, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(extras) > 0 {
		for i := 0; i < extras[0]; i++ {
			other, e := d.CreateTask(ctx, p.ID, input, a.ID)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = d.UpdateTask(ctx, other.ID, store.TaskInput{ColumnID: &active}, other.Version, a.ID); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, err = d.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if action == "complete-pr" {
		if _, _, err = d.LinkGithubArtifact(ctx, task.ID, "example/synthetic", 7, "https://github.com/example/synthetic/pull/7", task.Version, a.ID, true); err != nil {
			t.Fatal(err)
		}
		task, err = d.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	remote := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n", retrievalSHA + ":unrelated.png": "unread"}}
	ai := &assessmentAI{}
	if action == "bug_resolve" {
		ai.mutate = func(r map[string]any) { r["lifecycle_action"] = "bug_resolve" }
	}
	if action == "reopen" || action == "bug_reopen" {
		ai.mutate = func(r map[string]any) {
			r["placement"] = "correction_recommended"
			r["target_column_id"] = active
			r["target_semantic_state"] = "active"
			r["lifecycle_action"] = action
			c := r["conditions"].([]map[string]any)[0]
			c["result"] = "contradicted"
			r["basis"] = []map[string]any{{"kind": "implementation", "explanation": "Partial implementation requires reopening", "citation_ids": c["citation_ids"]}}
		}
	}
	svc := NewGithubAuditService(d, newRetrievalProvider(t, remote), ai, nil)
	s.GithubAudits = svc
	f := &correctionFixture{s: s, data: d, actor: a, project: p, task: task, remote: remote, identity: auth.Identity{Actor: a}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			s.githubAuditStart(w, r, f.identity, p.ID)
			return
		}
		s.githubAuditJob(w, r, f.identity, f.job.AuditID, "apply")
	}))
	t.Cleanup(f.server.Close)
	status, raw := f.post(t, "/start", map[string]any{}, "synthetic-start")
	if status != 202 {
		t.Fatalf("start %d %s", status, raw)
	}
	var start GithubAuditStartResponse
	json.Unmarshal(raw, &start)
	if err = svc.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.job, err = d.GetGithubAuditJob(ctx, start.Audit.ID)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := d.ListAuditFindings(ctx, start.Audit.ID, 50, 0)
	if err != nil || len(findings) < 1 {
		t.Fatalf("publication %+v %v findings=%d", f.job, err, len(findings))
	}
	for _, finding := range findings {
		if finding.TaskID == task.ID {
			f.finding = finding
		}
	}
	return f
}
func (f *correctionFixture) post(t *testing.T, path string, body any, key string) (int, []byte) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", f.server.URL+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b json.RawMessage
	if err = json.NewDecoder(res.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}
func (f *correctionFixture) request(preview bool, token string, inputs map[string]any) map[string]any {
	item := map[string]any{"finding_id": f.finding.ID}
	for k, v := range inputs {
		item[k] = v
	}
	if token != "" {
		item["confirmation_token"] = token
	}
	return map[string]any{"assessment_version": 2, "preview": preview, "items": []any{item}}
}
func correctionRow(t *testing.T, status int, raw []byte) map[string]any {
	t.Helper()
	if status != 200 {
		t.Fatalf("HTTP %d %s", status, raw)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Data) != 1 {
		t.Fatalf("receipt %s %v", raw, err)
	}
	return body.Data[0]
}
func correctionArtifact(t *testing.T, name string, v any) {
	t.Helper()
	b, _ := json.MarshalIndent(v, "", "  ")
	if strings.Contains(string(b), "confirmation_token") {
		t.Fatal("proof must be removed from receipt artifact")
	}
	if err := os.MkdirAll("/tmp/helm-tc309-handoff", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/helm-tc309-handoff/"+name+".json", b, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *correctionFixture) snapshot(t *testing.T) string {
	t.Helper()
	tables := []string{"tasks", "audit_findings", "audit_runs", "events", "comments", "github_audit_apply_previews"}
	rows := map[string]any{}
	for _, table := range tables {
		r, err := f.data.DB.Query("SELECT * FROM " + table + " ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := r.Columns()
		all := [][]any{}
		for r.Next() {
			v := make([]any, len(cols))
			p := make([]any, len(cols))
			for i := range v {
				p[i] = &v[i]
			}
			if err = r.Scan(p...); err != nil {
				t.Fatal(err)
			}
			all = append(all, v)
		}
		r.Close()
		rows[table] = all
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func (f *correctionFixture) previewSnapshot(t *testing.T) string {
	t.Helper()
	var requests, receipts int
	if err := f.data.DB.QueryRow(`SELECT count(*) FROM github_audit_correction_requests`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := f.data.DB.QueryRow(`SELECT count(*) FROM github_audit_correction_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]any{f.snapshot(t), requests, receipts})
	return string(raw)
}
