package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory is in AUDIT_SOURCE_RETRIEVAL_FAILURES.md. This regression
// exercises the real HTTP provider, captured store evidence and audit worker.
func TestGithubAssessmentCriterionLinkedConceptSearch(t *testing.T) {
	for _, mode := range []string{"concept", "beyond-index", "late-body", "refine-excerpt", "unsafe-query", "empty-question"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("CONCEPT"), Name: stringPtr("Synthetic criterion search")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Deployment"), Description: stringPtr("Goal: Let operators check whether the service can safely accept work before deployment, including whether the database is usable and migrations are current.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			f := &retrievalFixture{files: map[string]string{
				retrievalSHA + ":internal/a.go":             "package app\n" + strings.Repeat("// unrelated prefix\n", 230) + "func Preflight(source Source) error {\n defer source.Close()\n return source.Stat()\n}\n",
				retrievalSHA + ":internal/observability.go": "package app\nfunc readiness(database Database) bool {\n return database.Ping() == nil && database.MigrationsCurrent()\n}\n",
				retrievalSHA + ":internal/unrelated.go":     "package app\nfunc Close() {}\nfunc Stat() {}\n",
			}, hints: map[string][]string{"readiness": {"internal/observability.go", "internal/default-only.go", "internal/unrelated.go"}, "Close": {"internal/unrelated.go"}, "Stat": {"internal/unrelated.go"}}}

			if mode == "beyond-index" {
				for i := 0; i < 600; i++ {
					f.files[fmt.Sprintf("%s:internal/aaa%03d.go", retrievalSHA, i)] = "package app\nvar unrelated = 1\n"
				}
			}
			ai := &conceptAI{t: t, mode: mode}
			ai.planOutputs = []string{
				`{"paths":[],"searches":[{"term":"readiness","question":"Where does runtime readiness verify database usability and migration currency?"}],"gap":"Locate runtime readiness"}`,
				`{"paths":[{"path":"internal/observability.go","start_line":2,"end_line":4,"reason":"Runtime database and migration checks"}],"gap":"Read the runtime checks"}`,
			}
			if mode == "late-body" {
				source := "package app\n"
				for i := 0; i < 3; i++ {
					source += fmt.Sprintf("var metric%d = struct { readiness bool }{}\n", i) + strings.Repeat("// unrelated metrics padding\n", 45)
				}
				source += "func readiness(database Database) bool {\n ok := database.Ping() == nil\n" + strings.Repeat("// behavior body padding\n", 110) + " return ok && database.MigrationsCurrent()\n}\n"
				f.files[retrievalSHA+":internal/observability.go"] = source
				// Reproduce the actual planner's irrelevant refinement. Exact named
				// behavior must already be retained from the concept search read.
				ai.planOutputs[1] = `{"paths":[{"path":"internal/observability.go","start_line":2,"end_line":4,"reason":"Inspect metrics helper"}],"gap":"Inspect the selected helper"}`
			}
			if mode == "refine-excerpt" {
				ai.planOutputs[0] = `{"paths":[{"path":"internal/a.go","start_line":232,"end_line":235,"reason":"Inspect offline preflight"}],"gap":"Is this the runtime check?"}`
				ai.planOutputs[1] = `{"paths":[],"searches":[{"term":"readiness","question":"Where are the runtime database and migration checks rather than offline file validation?"}],"gap":"Locate runtime checks"}`
			}
			if mode == "unsafe-query" {
				ai.planOutputs[0] = strings.Replace(ai.planOutputs[0], `"readiness"`, `"readiness repo:private/other"`, 1)
			}
			if mode == "empty-question" {
				ai.planOutputs[0] = strings.Replace(ai.planOutputs[0], `Where does runtime readiness verify database usability and migration currency?`, "", 1)
			}
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
			run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: mode, Fingerprint: mode})
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if mode == "unsafe-query" || mode == "empty-question" {
				job, _ := data.GetGithubAuditJob(ctx, run.ID)
				assessmentReceipt(t, "criterion-linked-"+mode, map[string]any{"job": job, "reads": f.reads, "queries": f.queries, "prompts": ai.prompts})
				if len(f.queries) != 0 || job.Outcome != "failed" {
					t.Fatalf("invalid model query escaped validation: job=%+v queries=%v", job, f.queries)
				}
				return
			}
			e, err := data.GetGithubAuditEvidence(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			assessmentReceipt(t, "criterion-linked-"+mode, map[string]any{"evidence": e, "reads": f.reads, "queries": f.queries, "prompts": ai.prompts})
			if len(f.queries) != 1 || !strings.Contains(f.queries[0], "readiness") {
				t.Fatalf("criterion search absent or irrelevant calls chased: %v", f.queries)
			}
			found := false
			for _, item := range e.Items {
				found = found || item.Path == "internal/observability.go" && strings.Contains(item.Text, "MigrationsCurrent") && item.SHA == retrievalSHA && len(item.CriterionIDs) > 0
			}
			if !found {
				t.Fatalf("runtime behavior missing: reads=%v gaps=%+v", f.reads, e.CriterionGaps)
			}
			if f.reads[retrievalSHA+":internal/default-only.go"] != 0 || f.reads[retrievalSHA+":internal/unrelated.go"] != 0 {
				t.Fatalf("hint escaped exact tree/criterion: %v", f.reads)
			}
			if mode == "beyond-index" && ai.plans != 2 {
				t.Fatalf("search hit did not become a refinement candidate: plans=%d", ai.plans)
			}
			if ai.plans > 2 || len(f.reads) > 2 {
				t.Fatalf("bounded search grew budgets: plans=%d reads=%v", ai.plans, f.reads)
			}
		})
	}
}

type conceptAI struct {
	assessmentAI
	t       *testing.T
	mode    string
	prompts []map[string]any
}

func (a *conceptAI) DraftEvidence(ctx context.Context, owner string, request codexruntime.RunRequest) (codexruntime.RunResult, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(request.Prompt), &input); err != nil {
		return codexruntime.RunResult{}, err
	}
	if input["phase"] == "discovery" {
		a.prompts = append(a.prompts, input)
		if a.mode == "refine-excerpt" && a.plans == 1 {
			raw, _ := json.Marshal(input["inspected"])
			if !strings.Contains(string(raw), "source.Stat") {
				a.t.Error("refinement lost selected body beyond file prefix")
			}
		}
	}
	return a.assessmentAI.DraftEvidence(ctx, owner, request)
}
