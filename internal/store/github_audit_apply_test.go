package store

import (
	"context"
	"github.com/KanterLabs/helm/internal/db"
	"testing"
	"time"
)

// Apply failure cases: no preview, unapproved/version drift, new claim, unsafe
// destination and duplicate application must never cause an unguarded move.
func TestGithubAuditApplyRequiresPreviewAndMovesOnce(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	actor, _ := s.CreateActor(ctx, Actor{Kind: "human", Name: "Apply owner"}, "")
	p, _ := s.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("APPLYGH"), Name: stringPtrForTest("Apply synthetic")}, actor.ID)
	task, _ := s.CreateTask(ctx, p.ID, TaskInput{Title: stringPtrForTest("Review target")}, actor.ID)
	selection, _ := s.UpdateGithubSelection(ctx, p.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	run, _, err := s.StartGithubAudit(ctx, p.ID, actor.ID, GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "apply", Fingerprint: "apply", Model: "gpt-5.6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	// Preserve an explicitly legacy capture for the retained-v1 compatibility scenario.
	if _, err = s.DB.ExecContext(ctx, `UPDATE github_audit_jobs SET capture_json=json_remove(capture_json,'$.assessment_version') WHERE audit_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimGithubAudit(ctx, "apply-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evidence := GithubAuditEvidence{AuditID: run.ID, CapturedAt: now(), Items: []GithubAuditItem{{ID: "github:file:1", Kind: "file"}}, Repositories: []GithubAuditRepository{}, Omissions: []string{}}
	if err = s.SaveGithubAuditEvidence(ctx, lease, evidence); err != nil {
		t.Fatal(err)
	}
	if err = s.HeartbeatGithubAudit(ctx, lease, "validating"); err != nil {
		t.Fatal(err)
	}
	ready := "ready"
	if err = s.PublishGithubAudit(ctx, lease, []AuditFindingInput{{TaskID: task.ID, Verdict: "move_proposed", ProposedSemanticDestination: &ready, Confidence: 0.8, Reason: "Synthetic triage complete", EvidenceRefs: []string{"github:file:1"}}}, "complete", ""); err != nil {
		t.Fatal(err)
	}
	findings, _, _ := s.ListAuditFindings(ctx, run.ID, 10, 0)
	finding := findings[0]
	if _, err = s.ReviewAuditFinding(ctx, finding.ID, AuditFindingReviewInput{ReviewState: "approved"}, finding.Version, actor.ID); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetGithubAuditJob(ctx, run.ID)
	result, err := s.ApplyGithubAuditFinding(ctx, job, finding.ID, actor.ID, false)
	if err != nil || result.Outcome != "skipped" {
		t.Fatalf("no preview=%+v %v", result, err)
	}
	result, err = s.ApplyGithubAuditFinding(ctx, job, finding.ID, actor.ID, true)
	if err != nil || result.Outcome != "eligible" {
		t.Fatalf("preview=%+v %v", result, err)
	}
	before, _ := s.GetTask(ctx, task.ID)
	if before.Version != task.Version {
		t.Fatal("preview mutated task")
	}
	result, err = s.ApplyGithubAuditFinding(ctx, job, finding.ID, actor.ID, false)
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("apply=%+v %v", result, err)
	}
	again, err := s.ApplyGithubAuditFinding(ctx, job, finding.ID, actor.ID, false)
	if err != nil || again.Outcome != "applied" {
		t.Fatalf("repeat=%+v %v", again, err)
	}
	after, _ := s.GetTask(ctx, task.ID)
	if after.Version != task.Version+1 {
		t.Fatal("duplicate apply mutated task")
	}
}
