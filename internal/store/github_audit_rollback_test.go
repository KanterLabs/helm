package store

import (
	"context"
	"github.com/KanterLabs/helm/internal/db"
	"testing"
	"time"
)

// Exercise the old binary's SQL against a populated upgraded database. A
// queued row must remain resumable while legacy append/finalize cannot own it.
func TestGithubAuditRetainedSQLGuardsPreserveQueuedJobs(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	actor, _ := s.CreateActor(ctx, Actor{Kind: "human", Name: "Rollback owner"}, "")
	p, _ := s.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("ROLLGH"), Name: stringPtrForTest("Retained SQL")}, actor.ID)
	task, _ := s.CreateTask(ctx, p.ID, TaskInput{Title: stringPtrForTest("Existing task")}, actor.ID)
	selection, _ := s.UpdateGithubSelection(ctx, p.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	run, _, err := s.StartGithubAudit(ctx, p.ID, actor.ID, GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "retained", Fingerprint: "retained"})
	if err != nil {
		t.Fatal(err)
	}
	oldAppend := func(id, audit string) error {
		_, err := database.ExecContext(ctx, `INSERT INTO audit_findings(id,audit_id,task_id,captured_version,source_column,verdict,confidence,reason,created_at,updated_at) VALUES(?,?,?,?,?,'needs_attention',0.5,'Old synthetic client',?,?)`, id, audit, task.ID, task.Version, task.ColumnID, now(), now())
		return err
	}
	if err = oldAppend("old-injection", run.ID); err == nil {
		t.Fatal("old SQL appended to queued worker run")
	}
	if _, err = database.ExecContext(ctx, `UPDATE audit_runs SET status='complete',finalized_at=?,updated_at=? WHERE id=?`, now(), now(), run.ID); err == nil {
		t.Fatal("old SQL finalized queued worker run")
	}
	queued, _ := s.GetGithubAuditJob(ctx, run.ID)
	if queued.Stage != "queued" || queued.Outcome != "" {
		t.Fatal("guard destroyed resumable queue")
	}
	lease, err := s.ClaimGithubAudit(ctx, "restarted", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.HeartbeatGithubAudit(ctx, lease, "validating"); err != nil {
		t.Fatal(err)
	}
	evidence := GithubAuditEvidence{AuditID: run.ID, CapturedAt: now(), Items: []GithubAuditItem{{ID: "github:file:1", Kind: "file"}}, Repositories: []GithubAuditRepository{}, Omissions: []string{}}
	if err = s.SaveGithubAuditEvidence(ctx, lease, evidence); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishGithubAudit(ctx, lease, []AuditFindingInput{{TaskID: task.ID, Verdict: "needs_attention", Confidence: 0.4, Reason: "Synthetic retained source reviewed", EvidenceRefs: []string{"github:file:1"}}}, "complete", ""); err != nil {
		t.Fatal(err)
	}
	external, err := s.CreateAuditRun(ctx, p.ID, actor.ID, AuditRunInput{Scope: "external"})
	if err != nil {
		t.Fatal(err)
	}
	if err = oldAppend("external-finding", external.ID); err != nil {
		t.Fatalf("old external append broken: %v", err)
	}
	if _, err = database.ExecContext(ctx, `UPDATE audit_runs SET status='complete',finalized_at=?,updated_at=? WHERE id=?`, now(), now(), external.ID); err != nil {
		t.Fatalf("old external finalize broken: %v", err)
	}
	preserved, err := s.GetTask(ctx, task.ID)
	if err != nil || preserved.Version != task.Version {
		t.Fatalf("existing task changed %+v %v", preserved, err)
	}
}
