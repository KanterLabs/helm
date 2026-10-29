package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory: docs/AUDIT_COLUMN_RELEASE_VALIDATION.md. These use the
// real provider HTTP client, populated store and worker; receipts contain only
// synthetic fixture state and the closed public job message.
func TestGithubAuditClosedDiagnosticsWorker(t *testing.T) {
	cases := []struct {
		name, diagnostic, plan string
		mutate                 func(map[string]any)
	}{
		{name: "identity", diagnostic: "assessment_identity", mutate: func(r map[string]any) { r["captured_version"] = 999 }},
		{name: "condition", diagnostic: "assessment_condition_identity", mutate: func(r map[string]any) { r["conditions"].([]map[string]any)[0]["text"] = "private sentinel" }},
		{name: "citation", diagnostic: "assessment_citation", mutate: func(r map[string]any) {
			r["conditions"].([]map[string]any)[0]["citation_ids"] = []string{"private sentinel"}
		}},
		{name: "proof", diagnostic: "assessment_proof", mutate: func(r map[string]any) { r["conditions"].([]map[string]any)[0]["citation_ids"] = []string{} }},
		{name: "missing-target-state", diagnostic: "assessment_destination_state_missing", mutate: func(r map[string]any) { r["target_semantic_state"] = nil }},
		{name: "unknown-state-without-target", diagnostic: "assessment_destination_id_missing", mutate: func(r map[string]any) { r["target_column_id"] = nil; r["target_semantic_state"] = "not_a_state" }},
		{name: "destination", diagnostic: "assessment_destination_identity", mutate: func(r map[string]any) { r["target_column_id"] = "private sentinel" }},
		{name: "basis", diagnostic: "assessment_basis_shape", mutate: func(r map[string]any) {
			r["basis"] = []any{map[string]any{"kind": "implementation", "explanation": "private sentinel", "citation_ids": []string{}}}
		}},
		{name: "discovery-decode", diagnostic: "discovery_decode", plan: `{"private sentinel":true}`},
		{name: "discovery-schema", diagnostic: "discovery_schema", plan: `{"paths":null,"gap":"private sentinel"}`},
		{name: "discovery-path", diagnostic: "discovery_path", plan: `{"paths":[{"path":"private sentinel","start_line":1,"end_line":1,"reason":"private sentinel"}],"gap":"private sentinel"}`},
		{name: "discovery-range", diagnostic: "discovery_range", plan: `{"paths":[{"path":"internal/opaque.go","start_line":1,"end_line":400,"reason":"private sentinel"}],"gap":"private sentinel"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, err := data.EnsureDisabledActor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("DIAG"), Name: stringPtr("Synthetic diagnostics")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			description := "Goal: Implement internal/opaque.go."
			if tc.plan != "" {
				description = "Goal: Show task dependencies on cards."
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic diagnostic"), Description: &description}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			f := &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n"}}
			ai := &assessmentAI{mutate: tc.mutate, planOutput: tc.plan}
			service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
			run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: tc.name, Fingerprint: tc.name})
			if err != nil {
				t.Fatal(err)
			}
			if err = service.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			job, err := data.GetGithubAuditJob(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := data.GetAuditRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			assessmentReceipt(t, "diagnostic-"+tc.name, map[string]any{"job": job, "finding_count": result.FindingCount})
			if job.Outcome != "failed" || result.FindingCount != 0 || !strings.Contains(job.Message, "["+tc.diagnostic+"]") {
				t.Fatalf("missing closed diagnostic %s: %+v count=%d", tc.diagnostic, job, result.FindingCount)
			}
			if strings.Contains(job.Message, "private sentinel") {
				t.Fatal("raw model content escaped into diagnostic")
			}
		})
	}
}
