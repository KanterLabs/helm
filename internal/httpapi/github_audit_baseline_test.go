package httpapi

import (
	"context"
	"github.com/KanterLabs/helm/internal/store"
	"testing"
)

// Explicit PR-head selection must never be an implicit retrieval fallback.
func TestGithubAssessmentExplicitPRHeadBaseline(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, _ := data.EnsureDisabledActor(ctx)
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PRBASE"), Name: stringPtr("Synthetic explicit PR baseline")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("PR implementation"), Description: stringPtr("Goal: Implement internal/opaque.go.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\n// absent on main\n", retrievalHead + ":internal/opaque.go": "package app\nfunc Render() string { return \"complete\" }\n"}}
	ai := &assessmentAI{}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, Baseline: &store.GithubAuditBaseline{Repository: "example/synthetic", PullRequest: 7}, RequestKey: "pr-head", Fingerprint: "pr-head"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	e, err := data.GetGithubAuditEvidence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessmentReceipt(t, "explicit-pr-baseline", map[string]any{"evidence": e, "reads": f.reads, "model_boundary": ai.receipts})
	if len(e.Repositories) != 1 || e.Repositories[0].SHA != retrievalHead || e.Repositories[0].Ref != "refs/pull/7/head" {
		t.Fatalf("explicit PR head not pinned %+v", e.Repositories)
	}
	if f.reads[retrievalSHA+":internal/opaque.go"] != 0 || f.reads[retrievalHead+":internal/opaque.go"] != 1 {
		t.Fatal("explicit baseline mixed main/PR work")
	}
}
