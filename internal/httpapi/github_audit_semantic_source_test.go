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

// Failure inventory: docs/AUDIT_SOURCE_RETRIEVAL_FAILURES.md (written first).
// This is the real provider HTTP -> capture -> store -> worker boundary. The
// planner supplies deterministic windows; it never supplies source or proof.
func TestGithubAssessmentSemanticSourceBehaviorBeforeReferences(t *testing.T) {
	for _, mode := range []string{"complete", "missing-owned", "denied-owned", "cancel-refinement", "revoke-refinement", "invented-refinement", "source-drift"} {
		t.Run(mode, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, err := data.EnsureDisabledActor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REGION"), Name: stringPtr("Synthetic source behavior")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Readiness"), Description: stringPtr("Goal: Let operators check whether the service can safely accept work before deployment, including whether the database is usable and migrations are current.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			prefix := "package app\nimport dbsql \"database/sql\"\nfunc open() {\n database, _ := dbsql.Open(\"sqlite\", \"copy\")\n database.SetMaxIdleConns(8)\n configureOwnedAdapter()\n initializeOwnedQueue()\n}\n" + strings.Repeat("// Initial ownership padding\n", 120)
			body := "package app\nimport dbsql \"database/sql\"\n" + strings.Repeat("// Unrelated source prefix\n", 38)
			body += "func operatorReady(database *dbsql.DB, pool *Pool) bool {\n local, _ := dbsql.Open(\"sqlite\", \"copy\")\n local.SetMaxIdleConns(8)\n database.SetMaxIdleConns(8)\n"
			body += strings.Repeat(" // Relevant behavior window padding\n", 36)
			body += " pool.SetMaxIdleConns(1)\n if !CheckSchemaCurrency(database) { return false }\n"
			body += strings.Repeat(" // Relevant behavior window padding\n", 55)
			body += " return database.Ping() == nil\n}\n"
			f := &retrievalFixture{files: map[string]string{
				retrievalSHA + ":internal/connection.go": prefix,
				retrievalSHA + ":internal/probe.go":      body,
				retrievalSHA + ":internal/policy.go":     "package app\nfunc (p *Pool) SetMaxIdleConns(n int) {}\nfunc CheckSchemaCurrency(database *DB) bool { return true }\n",
			}, hints: map[string][]string{"SetMaxIdleConns": {"internal/policy.go"}, "CheckSchemaCurrency": {"internal/policy.go"}}}
			for i := 0; i < 710; i++ {
				name := fmt.Sprintf("docs/guide%03d.md", i)
				if i%2 == 0 {
					name = fmt.Sprintf("internal/connection_%03d_test.go", i)
				}
				f.files[retrievalSHA+":"+name] = "// Synthetic supplementary source\n"
			}
			if mode == "missing-owned" {
				delete(f.files, retrievalSHA+":internal/policy.go")
			}
			if mode == "denied-owned" {
				f.denied = map[string]bool{retrievalSHA + ":internal/policy.go": true}
			}
			ai := &semanticSourceAI{t: t, fixture: f, assessmentAI: assessmentAI{planOutputs: []string{
				`{"paths":[{"path":"internal/connection.go","start_line":1,"end_line":5,"reason":"Initial ownership guess"}],"gap":"Locate operator behavior"}`,
				`{"paths":[{"path":"internal/probe.go","start_line":41,"end_line":141,"reason":"Operator checks in another production owner"}],"gap":"Read database and schema behavior","searches":[{"term":"SetMaxIdleConns","question":"Does the owned pool enforce the criterion capacity check?"},{"term":"CheckSchemaCurrency","question":"Does the owned helper reject stale migrations?"}]}`,
			}}}
			revoked := false
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, func(context.Context, string, string) error {
				if revoked {
					return fmt.Errorf("synthetic revocation")
				}
				return nil
			})
			run, _, err := data.StartGithubAudit(ctx, project.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: mode, Fingerprint: mode})
			if err != nil {
				t.Fatal(err)
			}
			ai.beforeRefine = func() {
				switch mode {
				case "cancel-refinement":
					_, err = data.CancelGithubAudit(ctx, run.ID, "cancelled")
				case "revoke-refinement":
					revoked = true
				case "invented-refinement":
					ai.planOutputs[1] = `{"paths":[{"path":"../../private","start_line":1,"end_line":1,"reason":"Invented"}],"gap":"Untrusted path"}`
				case "source-drift":
					_, err = data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "different"}}, selection.Version)
				}
				if err != nil {
					t.Error(err)
				}
			}
			if mode != "complete" {
				ai.mutate = func(row map[string]any) {
					for _, c := range row["conditions"].([]map[string]any) {
						c["result"] = "unknown"
						c["gaps"] = []string{"Required owned source is unavailable"}
					}
					row["placement"] = "unable_to_verify"
					row["lifecycle_action"] = "none"
					row["target_column_id"] = nil
					row["target_semantic_state"] = nil
				}
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, _ := data.GetGithubAuditJob(ctx, run.ID)
			evidence, _ := data.GetGithubAuditEvidence(ctx, run.ID)
			got, _ := data.GetAuditRun(ctx, run.ID)
			assessmentReceipt(t, "semantic-source-"+mode, map[string]any{"job": job, "evidence": evidence, "reads": f.reads, "queries": f.queries, "plans": ai.plans})
			if ai.plans != 2 {
				t.Fatalf("body refinement did not precede helper chasing: plans=%d queries=%v", ai.plans, f.queries)
			}
			if mode == "cancel-refinement" || mode == "revoke-refinement" || mode == "invented-refinement" || mode == "source-drift" {
				if ai.infer != 0 || got.FindingCount != 0 || len(f.reads) != 1 {
					t.Fatalf("read/inference/publication escaped refinement fence: reads=%v infer=%d findings=%d", f.reads, ai.infer, got.FindingCount)
				}
				return
			}
			if mode == "denied-owned" {
				// Provider 404 invalidates source readiness: the existing auth fence
				// aborts capture rather than publishing a partial assessment.
				if job.Outcome != "failed" || job.ErrorCode != "authorization_changed" || ai.infer != 0 || got.FindingCount != 0 {
					t.Fatalf("inaccessible provider source escaped authorization fence: %+v", job)
				}
				return
			}
			middle := false
			for _, item := range evidence.Items {
				middle = middle || item.Path == "internal/probe.go" && strings.Contains(item.Text, "CheckSchemaCurrency(database)")
				if item.Kind == "file" && item.SHA != retrievalSHA {
					t.Fatal("source baseline changed")
				}
			}
			if !middle {
				t.Fatal("planned behavior region lost the middle call")
			}
			if len(f.reads) > 3 {
				t.Fatalf("prefix helpers consumed read budget: %v", f.reads)
			}
			if mode == "complete" {
				if job.Outcome == "failed" || got.FindingCount != 1 || len(evidence.CriterionGaps) != 0 {
					t.Fatalf("relevant source not established: job=%+v gaps=%+v", job, evidence.CriterionGaps)
				}
				if f.reads[retrievalSHA+":internal/policy.go"] != 1 {
					t.Fatal("same-name OWNED method was not followed exactly once")
				}
			} else {
				named := false
				for _, gap := range evidence.CriterionGaps {
					named = named || gap.Code == "source_reference_unresolved" && strings.Contains(gap.Message, "SetMaxIdleConns") && strings.Contains(gap.Message, "CheckSchemaCurrency")
				}
				if !named {
					t.Fatalf("missing owned body was silently dropped: %+v", evidence.CriterionGaps)
				}
			}
		})
	}
}

type semanticSourceAI struct {
	assessmentAI
	t            *testing.T
	fixture      *retrievalFixture
	beforeRefine func()
}

func (a *semanticSourceAI) DraftEvidence(ctx context.Context, owner string, r codexruntime.RunRequest) (codexruntime.RunResult, error) {
	var input struct {
		Phase      string              `json:"phase"`
		Directory  bool                `json:"directory_index"`
		Candidates []semanticCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return codexruntime.RunResult{}, err
	}
	if input.Phase == "discovery" {
		if input.Directory {
			a.t.Error("supplementary tests/docs forced production owners into directory routing")
		}
		found := false
		for _, c := range input.Candidates {
			found = found || c.Path == "internal/probe.go"
		}
		if !found {
			a.t.Error("global production candidate disappeared after initial guess")
		}
		if a.plans == 1 {
			if len(a.fixture.queries) != 0 {
				a.t.Errorf("initial prefix chased helpers before refinement: %v", a.fixture.queries)
			}
			if a.beforeRefine != nil {
				a.beforeRefine()
			}
		}
	}
	return a.assessmentAI.DraftEvidence(ctx, owner, r)
}
