package store

import (
	"testing"
)

// Failure inventory: adjacent labeled sections can be swallowed as multiline
// goal prose, silently omitting acceptance criteria. This captures real stored
// cards, checking exact criteria and source line identities before inference.
func TestGithubAuditCaptureAdjacentGoalAndAcceptanceHeadings(t *testing.T) {
	f := newDependencyFixture(t, "HEADINGS")
	s, ctx, actor, p := f.store, f.ctx, f.actor, f.project
	description := "Goal: Provide GET /healthz.\nAcceptance criteria\n- [ ] Return HTTP 200 JSON.\n- [ ] Return the exact status healthy."
	title := "Synthetic adjacent criteria"
	task, err := s.CreateTask(ctx, p.ID, TaskInput{Title: &title, Description: &description}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.UpdateGithubSelection(ctx, p.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := s.StartGithubAudit(ctx, p.ID, actor.ID, GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "adjacent-headings", Fingerprint: "adjacent-headings"})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := s.GetGithubAuditCapture(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Tasks) != 1 {
		t.Fatalf("scope %d", len(capture.Tasks))
	}
	conditions := capture.Tasks[0].Conditions
	if len(conditions) != 3 {
		t.Fatalf("acceptance criteria silently omitted: %+v", conditions)
	}
	for i, want := range []string{"Provide GET /healthz.", "Return HTTP 200 JSON.", "Return the exact status healthy."} {
		if conditions[i].Text != want {
			t.Fatalf("condition %d: %+v", i, conditions[i])
		}
	}
	if conditions[0].EndLine != 1 || conditions[1].Origin != "acceptance_criterion" || conditions[1].Line != 3 || conditions[2].Line != 4 {
		t.Fatalf("lost section/line identities: %+v", conditions)
	}
}
