package httpapi

import (
	"context"
	"github.com/KanterLabs/helm/internal/store"
	"testing"
)

// Stable unknown linked checks must not poison code-only baseline conditions.
// Actual permission revocation is separately tested through provider HTTP.
func TestGithubAssessmentStablePartialLinkedChecks(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, _ := data.EnsureDisabledActor(ctx)
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PARTIAL"), Name: stringPtr("Synthetic partial checks")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic implementation"), Description: stringPtr("Goal: Implement internal/opaque.go.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, task, err = data.LinkGithubArtifact(ctx, task.ID, "example/synthetic", 7, "https://github.com/example/synthetic/pull/7", task.Version, actor.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{partialChecks: true, files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n", retrievalHead + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n"}}
	ai := &assessmentAI{}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: "partial", Fingerprint: "partial"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := data.GetGithubAuditJob(ctx, run.ID)
	assessmentReceipt(t, "stable-partial-checks", map[string]any{"job": job, "model_boundary": ai.receipts})
	if job.Outcome == "failed" || ai.infer != 1 {
		t.Fatalf("irrelevant unknown check service invalidated code-only placement: %+v", job)
	}
}
