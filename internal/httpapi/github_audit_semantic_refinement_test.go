package httpapi

import (
	"context"
	"github.com/KanterLabs/helm/internal/store"
	"strings"
	"testing"
)

// Failure inventory: a tree-only planner cannot know the right line of a large
// file; stopping at its package/import prefix hides the implementation. Multiple
// requested owners must not be discarded after the first implementation file.
func TestGithubAssessmentSemanticRefinesLargeOwnerAndReadsSelectedOwners(t *testing.T) {
	for _, mode := range []string{"large-owner", "two-owners"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REFINE"), Name: stringPtr("Synthetic semantic refinement")}, actor.ID)
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
			f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render(card Card) string { return card.Prerequisites.Label() }\n", retrievalSHA + ":internal/support.go": "package app\nfunc Labels(card Card) string { return card.Prerequisites.Label() }\n"}}
			ai := &assessmentAI{}
			if mode == "large-owner" {
				f.files[retrievalSHA+":internal/opaque.go"] = "package app\n" + strings.Repeat("// Unrelated synthetic prefix padding\n", 199) + "func Render(card Card) string {\n return card.Prerequisites.Label()\n}\n"
				ai.planOutputs = []string{`{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":1,"reason":"Find the owner"}],"gap":"Locate dependency display"}`, `{"paths":[{"path":"internal/opaque.go","start_line":201,"end_line":203,"reason":"Inspect the server-indexed renderer"}],"gap":"Read the renderer body beyond its prefix"}`}
			} else {
				f.files[retrievalSHA+":internal/opaque.go"] = "package app\nfunc Render(card Card) string { return card.Prerequisites }\n"
				f.files[retrievalSHA+":internal/support.go"] = "package app\nfunc Labels(card Card) string { return card.Prerequisites }\n"
				ai.planOutput = `{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":2,"reason":"Card renderer"},{"path":"internal/support.go","start_line":1,"end_line":2,"reason":"Dependency labels"}],"gap":"Inspect both owners"}`
			}
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
			run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: mode, Fingerprint: mode})
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
			assessmentReceipt(t, "semantic-"+mode, map[string]any{"evidence": e, "model_boundary": ai.receipts, "reads": f.reads})
			if mode == "large-owner" {
				found := false
				for _, item := range e.Items {
					found = found || strings.Contains(item.Text, "card.Prerequisites")
				}
				if !found || ai.plans != 2 {
					t.Fatalf("large owner never refined: found=%v calls=%d", found, ai.plans)
				}
				if f.reads[retrievalSHA+":internal/opaque.go"] != 1 {
					t.Fatal("refinement must reuse pinned read")
				}
			}
			if mode == "two-owners" && f.reads[retrievalSHA+":internal/support.go"] != 1 {
				t.Fatal("second semantic owner was discarded")
			}
		})
	}
}
