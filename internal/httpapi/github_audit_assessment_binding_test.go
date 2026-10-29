package httpapi

import (
	"context"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory from real TC311 inference: conclusive model assessments
// copied null actual target IDs. Actual columns belong to the immutable server
// capture. Bind absent IDs only; explicit conflicts and duplicate mappings must
// still fail the normal atomic proof/lifecycle publication boundary.
func TestGithubAssessmentWorkerBindsCapturedDestination(t *testing.T) {
	cases := []struct {
		name            string
		completed, fail bool
		mutate          func(map[string]any)
	}{
		{name: "complete-without-copied-id", mutate: func(r map[string]any) { delete(r, "target_column_id") }},
		{name: "correct-without-copied-target", completed: true, mutate: func(r map[string]any) { r["target_column_id"] = nil; r["target_semantic_state"] = nil }},
		{name: "explicit-id-conflict", fail: true, mutate: func(r map[string]any) { r["target_column_id"] = "invented" }},
		{name: "explicit-correct-state-conflict", completed: true, fail: true, mutate: func(r map[string]any) { r["target_semantic_state"] = "active" }},
		{name: "duplicate-backlog-is-not-first", fail: true, mutate: func(r map[string]any) { r["target_column_id"] = nil; r["target_semantic_state"] = "backlog" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, err := data.EnsureDisabledActor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("BIND"), Name: stringPtr("Synthetic binding")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Synthetic binding"), Description: stringPtr("Goal: Implement internal/opaque.go.")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.completed {
				task, err = data.CompleteTask(ctx, task.ID, actor.ID, task.Version)
				if err != nil {
					t.Fatal(err)
				}
			}
			selection, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			provider := newRetrievalProvider(t, &retrievalFixture{files: map[string]string{retrievalSHA + ":internal/opaque.go": "package app\nfunc Render() string { return \"done\" }\n"}})
			ai := &assessmentAI{mutate: tc.mutate}
			service := NewGithubAuditService(data, provider, ai, nil)
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
			findings, _, err := data.ListAuditFindings(ctx, run.ID, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			assessmentReceipt(t, "binding-"+tc.name, map[string]any{"job": job, "findings": findings})
			if tc.fail {
				if job.Outcome != "failed" || len(findings) != 0 {
					t.Fatalf("conflicting target published: %+v", job)
				}
				return
			}
			if job.Outcome == "failed" || len(findings) != 1 || findings[0].Assessment == nil {
				t.Fatalf("server-owned target failed: %+v", job)
			}
			a := findings[0].Assessment
			if a.TargetColumnID == nil || a.TargetSemanticState == nil || *a.TargetSemanticState != "completed" {
				t.Fatalf("destination not captured: %+v", a)
			}
			if tc.completed && *a.TargetColumnID != task.ColumnID {
				t.Fatal("correct card did not preserve exact source")
			}
			current, err := data.GetTask(ctx, task.ID)
			if err != nil || current.Version != task.Version || current.ColumnID != task.ColumnID {
				t.Fatal("analysis mutated task")
			}
		})
	}
}
