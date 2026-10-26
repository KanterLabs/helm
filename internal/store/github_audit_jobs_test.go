package store

import (
	"context"
	"github.com/KanterLabs/helm/internal/db"
	"testing"
	"time"
)

func TestGithubAuditDurableFencesAndImmutableCapture(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	actor, err := s.CreateActor(ctx, Actor{Kind: "human", Name: "Synthetic owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("JOB"), Name: stringPtrForTest("Durable synthetic")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, p.ID, TaskInput{Title: stringPtrForTest("Captured task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.UpdateGithubSelection(ctx, p.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	input := GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "repeat", Fingerprint: "body", Model: "gpt-5.6-luna", Effort: "medium"}
	run, job, err := s.StartGithubAudit(ctx, p.ID, actor.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := s.StartGithubAudit(ctx, p.ID, actor.ID, input)
	if err != nil || again.ID != run.ID {
		t.Fatalf("replay=%+v %v", again, err)
	}
	lease, err := s.ClaimGithubAudit(ctx, "test-owner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if lease.AuditID != job.AuditID {
		t.Fatalf("wrong claim %+v", lease)
	}
	if _, err = s.FinalizeAuditRun(ctx, run.ID, "complete"); err == nil {
		t.Fatal("external finalize bypassed ownership")
	}
	if _, err = s.CancelGithubAudit(ctx, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	evidence := GithubAuditEvidence{AuditID: run.ID, CapturedAt: now(), Repositories: []GithubAuditRepository{}, Items: []GithubAuditItem{}, Omissions: []string{}}
	if err = s.SaveGithubAuditEvidence(ctx, lease, evidence); err == nil {
		t.Fatal("cancelled lease wrote evidence")
	}
	if err = s.PublishGithubAudit(ctx, lease, nil, "complete", ""); err == nil {
		t.Fatal("late publication succeeded")
	}
	terminal, _ := s.GetAuditRun(ctx, run.ID)
	if terminal.Status != "failed" {
		t.Fatalf("legacy projection %s", terminal.Status)
	}
	captured, err := s.GetGithubAuditCapture(ctx, run.ID)
	if err != nil || len(captured.Tasks) != 1 || captured.Tasks[0].Version != task.Version {
		t.Fatalf("capture=%+v %v", captured, err)
	}
	input.RequestKey = "retry"
	input.RetryOf = run.ID
	next, _, err := s.StartGithubAudit(ctx, p.ID, actor.ID, input)
	if err != nil || next.ID == run.ID {
		t.Fatalf("retry=%+v %v", next, err)
	}
	lease, err = s.ClaimGithubAudit(ctx, "expired", -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverGithubAudits(ctx); err != nil {
		t.Fatal(err)
	}
	interrupted, _ := s.GetGithubAuditJob(ctx, next.ID)
	if interrupted.Outcome != "interrupted" {
		t.Fatalf("recovery=%+v lease=%+v", interrupted, lease)
	}
}
