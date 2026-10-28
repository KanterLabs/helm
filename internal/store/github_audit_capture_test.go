package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Failure inventory (before implementation): default scope can drop completed
// and blocked cards or page at 50; oversized scopes can silently truncate;
// description conditions can disappear or be rewritten; checklist identity can
// be lost; criterion IDs can drift on retries or survive changed task versions;
// explicit filters and delegated authorization bounds must remain authoritative.
func TestGithubAuditCaptureCompleteScopeAndQuotedCriteria(t *testing.T) {
	f := newDependencyFixture(t, "CAPTURE")
	description := "Goal: Provide `GET /healthz`.\n\nAcceptance criteria\n- [ ] Respond with status 200.\n- [x] Return JSON.\n\nNotes\nAudit demonstration only."
	task, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: stringPtrForTest("Health route"), Description: &description}, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = f.store.AddTaskChecklistItem(f.ctx, task.ID, ChecklistItemInput{Text: stringPtrForTest("Keep liveness independent of storage")}, task.Version, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		f.task(t, fmt.Sprintf("Other %d", i))
	}
	done := f.task(t, "Done beyond board page")
	done, err = f.store.CompleteTask(f.ctx, done.ID, f.actor.ID, done.Version)
	if err != nil {
		t.Fatal(err)
	}
	blocked := f.task(t, "Blocked remains in scope")
	// Fixture placement bypasses lifecycle solely to create a bounded capture input.
	if _, err = f.store.DB.ExecContext(f.ctx, `UPDATE tasks SET column_id=(SELECT id FROM columns WHERE project_id=? AND semantic_state='blocked' LIMIT 1) WHERE id=?`, f.project.ID, blocked.ID); err != nil {
		t.Fatal(err)
	}
	sel, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, RequestKey: "scope", Fingerprint: "scope"}
	run, job, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := f.store.GetGithubAuditCapture(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Tasks) != 58 || job.TaskCount != 58 {
		t.Errorf("silently dropped selected cards: tasks=%d count=%d", len(capture.Tasks), job.TaskCount)
	}
	var captured GithubAuditTask
	states := map[string]bool{}
	for _, row := range capture.Tasks {
		states[row.SemanticState] = true
		if row.ID == task.ID {
			captured = row
		}
	}
	if !states["completed"] || !states["blocked"] {
		t.Errorf("default scope missing lifecycle states: %v", states)
	}
	raw, _ := json.Marshal(captured)
	var typed struct {
		Kind       string `json:"kind"`
		Conditions []struct {
			ID, Text, Origin, SourceID string
			TaskVersion                int64 `json:"task_version"`
			Line                       int   `json:"line"`
		} `json:"conditions"`
	}
	if err = json.Unmarshal(raw, &typed); err != nil {
		t.Fatal(err)
	}
	if typed.Kind != "task" || len(typed.Conditions) != 4 {
		t.Fatalf("missing quoted criteria: %s", raw)
	}
	want := []string{"Provide `GET /healthz`.", "Respond with status 200.", "Return JSON.", "Keep liveness independent of storage"}
	ids := map[string]bool{}
	for i, condition := range typed.Conditions {
		if condition.ID == "" || ids[condition.ID] || condition.Text != want[i] || condition.TaskVersion != task.Version {
			t.Errorf("unstable or rewritten condition: %+v", condition)
		}
		ids[condition.ID] = true
		if i < 3 && condition.Line == 0 {
			t.Error("description quote lost line origin")
		}
	}
	if _, err = f.store.CancelGithubAudit(f.ctx, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	input.RequestKey = "retry"
	second, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := f.store.GetGithubAuditCapture(f.ctx, second.ID)
	for _, row := range repeated.Tasks {
		if row.ID == task.ID {
			b, _ := json.Marshal(row)
			if string(b) != string(raw) {
				t.Error("immutable criteria drifted on retry")
			}
		}
	}
	if _, err = f.store.CancelGithubAudit(f.ctx, second.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	input.RequestKey = "explicit"
	input.TaskIDs = []string{done.ID}
	explicit, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	only, _ := f.store.GetGithubAuditCapture(f.ctx, explicit.ID)
	if len(only.Tasks) != 1 || only.Tasks[0].ID != done.ID {
		t.Fatal("explicit scope changed")
	}
}

func TestGithubAuditCaptureRejectsUnreportedBatchOverflow(t *testing.T) {
	f := newDependencyFixture(t, "CAPLIMIT")
	first := f.task(t, "Synthetic limit seed")
	// Populate directly to isolate the server-side scope boundary from task creation.
	for i := 0; i < 1000; i++ {
		_, err := f.store.DB.ExecContext(context.Background(), `INSERT INTO tasks(id,project_id,number,column_id,title,description,priority,position,version,created_at,updated_at) SELECT ?,project_id,?,column_id,'Synthetic scope card','','normal',0,1,created_at,updated_at FROM tasks WHERE id=?`, fmt.Sprintf("capture-limit-%04d", i), i+2, first.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	sel, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, RequestKey: "limit", Fingerprint: "limit"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(fmt.Sprint(err), "1000") {
		t.Fatalf("batch overflow must reject with visible hard limit, got %v", err)
	}
}

// Multiline source conditions must not lose the line containing the actual
// required behavior. This exercises the persisted capture, not a parser mock.
func TestGithubAuditCapturePreservesMultilineConditions(t *testing.T) {
	f := newDependencyFixture(t, "CAPLINES")
	description := "## Goal\nProvide a health endpoint\nfor the service.\n\n## Acceptance criteria\n- [ ] GET /healthz returns\n  status 200 without storage.\n\n## Notes\nUnrelated prose."
	task, err := f.store.CreateTask(f.ctx, f.project.ID, TaskInput{Title: stringPtrForTest("Synthetic multiline"), Description: &description}, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := f.store.UpdateGithubSelection(f.ctx, f.project.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: "lines", Fingerprint: "lines"}
	run, _, err := f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.store.GetGithubAuditCapture(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	conditions := c.Tasks[0].Conditions
	if len(conditions) != 2 || conditions[0].Text != "Provide a health endpoint\nfor the service." || conditions[0].Line != 2 || conditions[0].EndLine != 3 || conditions[1].Text != "GET /healthz returns\n  status 200 without storage." || conditions[1].Line != 6 || conditions[1].EndLine != 7 {
		t.Fatalf("multiline quote lost requirements: %+v", conditions)
	}
	if _, err = f.store.CancelGithubAudit(f.ctx, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	changed := "Goal: Provide GET /readyz."
	updated, err := f.store.UpdateTask(f.ctx, task.ID, TaskInput{Description: &changed}, task.Version, f.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	input.RequestKey = "new-version"
	run, _, err = f.store.StartGithubAudit(f.ctx, f.project.ID, f.actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	c, err = f.store.GetGithubAuditCapture(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tasks[0].Conditions[0].ID == conditions[0].ID || c.Tasks[0].Conditions[0].TaskVersion != updated.Version {
		t.Fatal("criterion identity survived a changed task version")
	}
}
