package httpapi

import (
	"context"
	"fmt"
	"github.com/KanterLabs/helm/internal/store"
	"testing"
)

// Failure inventory: the first bounded alphabetical file page can exclude the
// entire actual UI owner. A bounded directory index must let semantic planning
// route to a late-sorting owner without word-ranking or arbitrary fetches.
func TestGithubAssessmentSemanticLargeTreeRoutesToLateDirectory(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, _ := data.EnsureDisabledActor(ctx)
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("TREEPLAN"), Name: stringPtr("Synthetic large tree")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Dependencies"), Description: stringPtr("Goal: Show task dependencies on cards.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{files: map[string]string{retrievalSHA + ":web/src/cards/Opaque.svelte": "<article>{card.Prerequisites}</article>\n"}}
	for i := 0; i < 700; i++ {
		f.files[fmt.Sprintf("%s:internal/unrelated/owner%03d.go", retrievalSHA, i)] = "package app\nvar unused = 1\n"
	}
	ai := &assessmentAI{planOutputs: []string{`{"paths":[{"path":"web/src/cards","start_line":1,"end_line":1,"reason":"Choose card UI ownership directory"}],"gap":"Locate the task-card implementation"}`, `{"paths":[{"path":"web/src/cards/Opaque.svelte","start_line":1,"end_line":1,"reason":"Inspect the card renderer"}],"gap":"Read the dependency display"}`}}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: "large-tree", Fingerprint: "large-tree"})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := data.GetGithubAuditJob(ctx, run.ID)
	e, _ := data.GetGithubAuditEvidence(ctx, run.ID)
	assessmentReceipt(t, "semantic-large-tree", map[string]any{"job": job, "evidence": e, "model_boundary": ai.receipts, "reads": f.reads})
	if job.Outcome == "failed" || ai.plans != 2 || f.reads[retrievalSHA+":web/src/cards/Opaque.svelte"] != 1 {
		t.Fatalf("late owner excluded %+v calls=%d reads=%v", job, ai.plans, f.reads)
	}
	if len(f.reads) != 1 {
		t.Fatal("directory planning spent source budget on unrelated files")
	}
}
