package httpapi

import (
	"context"
	"fmt"
	"github.com/KanterLabs/helm/internal/store"
	"testing"
)

func TestGithubAssessmentOrdinaryLanguageDiscovery(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprintf("linked-%v", linked), func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("SEMANTIC"), Name: stringPtr("Synthetic semantic discovery")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Dependencies"), Description: stringPtr("Goal: Show task dependencies on cards.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if linked {
				_, task, err = data.LinkGithubArtifact(ctx, task.ID, "example/synthetic", 7, "https://github.com/example/synthetic/pull/7", task.Version, actor.ID, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render(card Card) string {\n return card.Prerequisites.Label()\n}\nfunc (p *Prerequisites) Label() string { return p.Name }\n", retrievalHead + ":internal/opaque.go": "package app\nfunc Render(card Card) string { return card.Prerequisites.Label() }\nfunc (p *Prerequisites) Label() string { return p.Name }\n"}}
			for i := 0; i < 20; i++ {
				f.files[fmt.Sprintf("%s:internal/a%02d.go", retrievalSHA, i)] = "package app\nvar unrelated = 1\n"
			}
			ai := &assessmentAI{}
			if !linked {
				f.hints = map[string][]string{"Render": {"internal/opaque.go"}}
				ai.planOutputs = []string{`{"paths":[],"searches":[{"term":"Render","question":"Where are dependencies displayed on cards?"}],"gap":"Find card rendering"}`, `{"paths":[{"path":"internal/opaque.go","declaration":"Render","reason":"Card renderer"}],"searches":[],"source_complete":true,"gap":"Inspect dependencies"}`}
			}
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
			run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "semantic", Fingerprint: "semantic"})
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, _ := data.GetGithubAuditJob(ctx, run.ID)
			e, err := data.GetGithubAuditEvidence(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			assessmentReceipt(t, fmt.Sprintf("ordinary-language-linked-%v", linked), map[string]any{"job": job, "evidence": e, "model_boundary": ai.receipts, "reads": f.reads})
			if job.Outcome == "failed" || ai.plans < 1 || ai.plans > 4 {
				t.Fatalf("ordinary English discovery failed %+v plans=%d", job, ai.plans)
			}
			found := false
			for _, item := range e.Items {
				if item.Path == "internal/opaque.go" && item.SourceRole == "baseline" && len(item.CriterionIDs) > 0 {
					found = true
					if item.SHA != retrievalSHA {
						t.Fatal("baseline silently switched")
					}
				}
			}
			if !found {
				t.Fatal("ordinary English has no pinned criterion excerpt")
			}
			if f.reads[retrievalSHA+":internal/opaque.go"] != 1 {
				t.Fatal("semantic read was not pinned/deduplicated")
			}
		})
	}
}

func TestGithubAssessmentPlanningFencesAndPathBounds(t *testing.T) {
	for _, mode := range []string{"cancel", "revoke", "invented-path", "empty-plan"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PLANFENCE"), Name: stringPtr("Synthetic planning fence")}, actor.ID)
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
			f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render(card Card) string { return card.Prerequisites.Label() }\nfunc (p *Prerequisites) Label() string { return p.Name }\n"}}
			ai := &assessmentAI{}
			revoked := false
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, func(context.Context, string, string) error {
				if revoked {
					return fmt.Errorf("synthetic revocation")
				}
				return nil
			})
			run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: mode, Fingerprint: mode})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cancel":
				ai.afterPlan = func() {
					if _, e := data.CancelGithubAudit(ctx, run.ID, "cancelled"); e != nil {
						t.Error(e)
					}
				}
			case "revoke":
				ai.afterPlan = func() { revoked = true }
			case "invented-path":
				ai.planOutput = `{"paths":[{"path":"../../secrets","start_line":1,"end_line":1,"reason":"Invented"}],"gap":"Named missing criterion"}`
			case "empty-plan":
				ai.planOutput = `{"paths":[],"gap":"No suitable candidate"}`
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, _ := data.GetGithubAuditJob(ctx, run.ID)
			got, _ := data.GetAuditRun(ctx, run.ID)
			assessmentReceipt(t, "planning-"+mode, map[string]any{"job": job, "reads": f.reads, "model_boundary": ai.receipts})
			if ai.plans < 1 || ai.plans > 2 {
				t.Fatalf("planning was absent or unbounded %d", ai.plans)
			}
			if mode == "cancel" || mode == "revoke" {
				if ai.infer != 0 || len(f.reads) != 0 || got.FindingCount != 0 {
					t.Fatal("extra inference/read/publication after planning fence")
				}
				if mode == "cancel" && job.Outcome != "cancelled" {
					t.Fatal("cancel lost")
				}
			}
			if mode == "invented-path" {
				for key := range f.reads {
					if key != retrievalSHA+":internal/opaque.go" {
						t.Fatalf("model escaped pinned tree: %s", key)
					}
				}
			}
		})
	}
}
