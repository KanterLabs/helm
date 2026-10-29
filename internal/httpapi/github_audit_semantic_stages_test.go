package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Real provider HTTP -> store -> worker regressions. The initial search term
// intentionally names no function; the correct file has constructor/metric
// distractions before a receiver-qualified behavior body beyond the prefix.
func TestGithubAssessmentSearchFirstDeclarationRetrieval(t *testing.T) {
	for _, mode := range []string{"bypass", "body", "two-declarations", "same-file-helper", "qualified-helper", "literal-dot-search", "truncated-body", "missing-selector", "ambiguous-selector", "empty-unresolved", "empty-complete", "single-complete", "complete-missing-owner"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, _ := data.EnsureDisabledActor(ctx)
			project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("STAGE"), Name: stringPtr("Synthetic staged retrieval")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Deployment"), Description: stringPtr("Goal: Let operators check whether the service can safely accept work before deployment, including whether the database is usable and migrations are current.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			sel, err := data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			source := "package app\nfunc NewInspector() *Inspector { return &Inspector{} }\n"
			for i := 0; i < 3; i++ {
				source += "var metric = struct { admission bool }{}\n" + strings.Repeat("// unrelated padding\n", 45)
			}
			source += "func (i *Inspector) report() bool { return i.inspectWork(i.Database) }\nfunc (i *Inspector) inspectWork(db Database) bool {\n ok := db.Ping() == nil\n" + strings.Repeat("// body padding\n", 110) + " return ok && db.MigrationsCurrent()\n}\nfunc (m *Metrics) inspectWork() {}\n"
			fixture := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": source, retrievalSHA + ":internal/database.go": "package app\nfunc Preflight() {}\n"}, hints: map[string][]string{"admission": {"internal/opaque.go", "internal/default-only.go"}}}
			ai := &assessmentAI{planOutputs: []string{
				`{"paths":[],"searches":[{"term":"admission","question":"Where do operators check runtime database and migration readiness?"}],"gap":"Find runtime behavior"}`,
				`{"paths":[{"path":"internal/opaque.go","declaration":"Inspector.inspectWork","reason":"Runtime behavior"}],"searches":[],"source_complete":true,"gap":"Inspect the declared runtime behavior"}`,
			}}
			switch mode {
			case "literal-dot-search":
				fixture.hints["package.json"] = fixture.hints["admission"]
				ai.planOutputs[0] = strings.Replace(ai.planOutputs[0], `"admission"`, `"package.json"`, 1)
			case "same-file-helper", "qualified-helper":
				term, lexical := "InspectSchema", "InspectSchema"
				if mode == "same-file-helper" {
					fixture.files[retrievalSHA+":internal/opaque.go"] += "func InspectSchema(db Database) bool { return db.OriginalSchemaCurrent() }\n"
				} else {
					term, lexical = "Inspector.inspectWork", "inspectWork"
				}
				fixture.hints[lexical] = []string{"internal/opaque.go"}
				ai.planOutputs[1] = `{"paths":[{"path":"internal/opaque.go","declaration":"Inspector.report","reason":"Invocation"}],"searches":[{"term":"` + term + `","question":"Which original database checks does the invoked helper perform?"}],"source_complete":false,"gap":"Inspect the required helper body"}`
			case "single-complete":
				delete(fixture.files, retrievalSHA+":internal/database.go")
				fixture.files[retrievalSHA+":internal/opaque.go"] = "package app\nfunc readiness(db Database) bool { return db.Ping() == nil && db.MigrationsCurrent() }\n" + strings.Repeat("// trailing unrelated padding\n", 200)
				ai.planOutputs = []string{`{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":2,"reason":"Relevant behavior at start of the single owner"}],"gap":"Inspect readiness"}`, `{"paths":[],"searches":[],"source_complete":true,"gap":"All requested behavior is already captured"}`}
			case "two-declarations":
				ai.planOutputs[1] = strings.Replace(ai.planOutputs[1], `],"searches":[]`, `,{"path":"internal/opaque.go","declaration":"Inspector.report","reason":"Operator result access"}],"searches":[]`, 1)
			case "truncated-body":
				fixture.files[retrievalSHA+":internal/opaque.go"] = strings.Replace(source, "// body padding\n", strings.Repeat("// body padding\n", 3), -1)
			case "bypass":
				ai.planOutputs[0] = `{"paths":[{"path":"internal/database.go","start_line":1,"end_line":2,"reason":"Filename guess"}],"searches":[],"gap":"Find the owner"}`
			case "missing-selector":
				ai.planOutputs[1] = strings.ReplaceAll(ai.planOutputs[1], "Inspector.inspectWork", "Inspector.missing")
			case "ambiguous-selector":
				ai.planOutputs[1] = strings.ReplaceAll(ai.planOutputs[1], "Inspector.inspectWork", "inspectWork")
			case "empty-unresolved", "empty-complete", "complete-missing-owner":
				ai.planOutputs[1] = `{"paths":[],"searches":[],"source_complete":false,"gap":"Runtime migration evidence is still missing"}`
				if mode != "empty-unresolved" {
					ai.planOutputs[1] = strings.ReplaceAll(ai.planOutputs[1], "false", "true")
				}
				if mode == "complete-missing-owner" {
					fixture.hints = nil
				}
			}
			service := NewGithubAuditService(data, newRetrievalProvider(t, fixture), ai, nil)
			run, _, err := data.StartGithubAudit(ctx, project.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, RequestKey: mode, Fingerprint: mode})
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, _ := data.GetGithubAuditJob(ctx, run.ID)
			evidence, _ := data.GetGithubAuditEvidence(ctx, run.ID)
			assessmentReceipt(t, "search-first-"+mode, map[string]any{"job": job, "evidence": evidence, "reads": fixture.reads, "queries": fixture.queries, "planning": ai.receipts})
			if mode == "bypass" {
				if job.Outcome != "failed" || len(fixture.reads) != 0 || len(fixture.queries) != 0 {
					t.Fatalf("search bypass escaped: %+v reads=%v", job, fixture.reads)
				}
				return
			}
			expectedQueries := 1
			if mode == "same-file-helper" || mode == "qualified-helper" {
				expectedQueries = 2
			}
			if mode == "single-complete" {
				expectedQueries = 0
			}
			if len(fixture.queries) != expectedQueries || ai.plans != 2 {
				t.Fatalf("unbounded/absent staged discovery: queries=%v plans=%d", fixture.queries, ai.plans)
			}
			if mode == "literal-dot-search" && !strings.Contains(fixture.queries[0], "package.json") {
				t.Fatalf("literal dotted search mistaken for a method selector: %v", fixture.queries)
			}
			if mode == "missing-selector" || mode == "ambiguous-selector" {
				if job.Outcome != "failed" || ai.infer != 0 {
					t.Fatalf("invented/ambiguous declaration escaped: %+v", job)
				}
				return
			}
			if mode == "body" || mode == "two-declarations" {
				found := false
				for _, item := range evidence.Items {
					found = found || item.Path == "internal/opaque.go" && item.SHA == retrievalSHA && strings.Contains(item.Text, "MigrationsCurrent")
				}
				if mode == "two-declarations" {
					report := false
					for _, item := range evidence.Items {
						report = report || strings.Contains(item.Text, "return i.inspectWork(i.Database)")
					}
					if !report {
						t.Fatal("one file lost a required declaration")
					}
				}
				if !found || len(evidence.CriterionGaps) != 0 {
					t.Fatalf("wrong body retained: found=%v gaps=%+v", found, evidence.CriterionGaps)
				}
				if fixture.reads[retrievalSHA+":internal/opaque.go"] != 1 || len(fixture.reads) != 1 {
					t.Fatalf("body refinement did not reuse pinned read: %v", fixture.reads)
				}
				var refined map[string]json.RawMessage
				json.Unmarshal(ai.receipts[1], &refined)
				if !strings.Contains(string(refined["inspected"]), "Inspector.inspectWork") {
					t.Fatal("server declaration selectors absent")
				}
			}
			if mode == "same-file-helper" || mode == "qualified-helper" {
				marker := "OriginalSchemaCurrent"
				if mode == "qualified-helper" {
					marker = "MigrationsCurrent"
				}
				found := false
				for _, item := range evidence.Items {
					found = found || item.Path == "internal/opaque.go" && strings.Contains(item.Text, marker)
				}
				if !found || len(evidence.CriterionGaps) > 0 || fixture.reads[retrievalSHA+":internal/opaque.go"] != 1 {
					t.Fatalf("required same-file helper lost: found=%v gaps=%+v reads=%v", found, evidence.CriterionGaps, fixture.reads)
				}
			}
			if mode == "truncated-body" && (len(evidence.CriterionGaps) == 0 || evidence.CriterionGaps[0].Code != "excerpt_budget_exhausted") {
				t.Fatal("truncated declaration lost its required source gap")
			}
			if (mode == "empty-complete" || mode == "single-complete") && len(evidence.CriterionGaps) != 0 {
				t.Fatalf("explicit no-more-source became unresolved: %+v", evidence.CriterionGaps)
			}
			if mode == "empty-unresolved" || mode == "complete-missing-owner" {
				if len(evidence.CriterionGaps) == 0 {
					t.Fatal("unresolved evidence question silently dropped")
				}
				if mode == "empty-unresolved" && !strings.Contains(evidence.CriterionGaps[0].Message, "Runtime migration evidence is still missing") {
					t.Fatal("named unresolved question was discarded")
				}
			}
		})
	}
}
