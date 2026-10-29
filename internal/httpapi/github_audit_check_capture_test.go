package httpapi

import (
	"context"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Failure inventory: an observed baseline check can be discarded because source
// discovery cannot find an implementation owner for the check-only criterion.
// This must not require reading source, or allow checks at a different commit.
func TestGithubAssessmentObservedCheckDoesNotRequireImplementationOwner(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, _ := data.EnsureDisabledActor(ctx)
	p, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("CHECKCAP"), Name: stringPtr("Synthetic check capture")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, p.ID, store.TaskInput{Title: stringPtr("Observed CI"), Description: stringPtr("Goal: CI checks pass.")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	sel, err := data.UpdateGithubSelection(ctx, p.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &retrievalFixture{checkConclusion: "success", files: map[string]string{retrievalHead + ":unrelated.png": "unrelated image"}}
	ai := &assessmentAI{mutate: func(row map[string]any) {
		row["conditions"].([]map[string]any)[0]["citation_ids"] = []string{"github:baseline-check:1"}
	}}
	service := NewGithubAuditService(data, newRetrievalProvider(t, f), ai, nil)
	run, _, err := data.StartGithubAudit(ctx, p.ID, actor.ID, store.GithubAuditStartInput{Repositories: sel.Repositories, SelectionVersion: sel.Version, TaskIDs: []string{task.ID}, Baseline: &store.GithubAuditBaseline{Repository: "example/synthetic", PullRequest: 7}, RequestKey: "checks", Fingerprint: "checks"})
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
	evidence, err := data.GetGithubAuditEvidence(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	assessmentReceipt(t, "parent-observed-check", map[string]any{"job": job, "evidence": evidence, "planning_calls": ai.plans})
	if job.Outcome == "failed" || len(evidence.CriterionGaps) != 0 || ai.plans != 0 {
		t.Fatalf("observed baseline CI incorrectly requires source discovery: outcome=%s gaps=%+v plans=%d", job.Outcome, evidence.CriterionGaps, ai.plans)
	}
	findings, _, err := data.ListAuditFindings(ctx, run.ID, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Assessment == nil || findings[0].Assessment.TargetSemanticState == nil || *findings[0].Assessment.TargetSemanticState != "completed" {
		t.Fatal("observed required check did not support Done")
	}
}
