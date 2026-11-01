package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

// The failure inventory in docs/GITHUB_AGENT_AUDITS.md predates this suite.
// Exercise real SQLite transactions: a revoked/changed grant must fence writes,
// including a retained lease and an idempotent request captured before revocation.
func TestGithubAuditAuthorizationDurableFences(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	s := New(database)
	owner, err := s.CreateActor(ctx, Actor{Kind: "human", Name: "Synthetic owner", Admin: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("GAT"), Name: stringPtrForTest("Grant synthetic")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, p.ID, TaskInput{Title: stringPtrForTest("Synthetic captured task")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.UpdateGithubSelection(ctx, p.ID, []GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateAgent(ctx, Actor{Kind: "agent", Name: "Synthetic agent", ProjectIDs: []string{p.ID}}, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.CreateToken(ctx, agent.ID, "Synthetic token", []string{"tasks:read"}, []string{p.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.CreateGithubAuditAuthorization(ctx, p.ID, owner.ID, CreateGithubAuditAuthorizationInput{AgentID: agent.ID, TokenID: token.ID, ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if grant.MaxTasks != 10 || grant.SelectionVersion != selection.Version {
		t.Fatalf("unexpected grant %+v", grant)
	}
	if _, err = s.ValidateGithubAuditAuthorization(ctx, grant.ID, p.ID, agent.ID, "other-token"); err == nil {
		t.Fatal("wrong token accepted")
	}
	input := GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "delegated", Fingerprint: "synthetic", AuthorizationID: grant.ID, RequestTokenID: token.ID, AccountOwnerID: owner.ID}
	run, job, err := s.StartGithubAudit(ctx, p.ID, agent.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if job.ActorID != agent.ID || job.AccountOwnerID != owner.ID || job.AuthorizationID != grant.ID {
		t.Fatalf("attribution %+v", job)
	}
	raw, _ := json.Marshal(job)
	var public map[string]any
	_ = json.Unmarshal(raw, &public)
	if public["requested_by"] != agent.ID || public["account_owner_id"] != owner.ID || public["request_token_id"] != nil {
		t.Fatalf("public attribution %s", raw)
	}
	if _, _, err = s.StartGithubAudit(ctx, p.ID, owner.ID, GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "owner-concurrent", Fingerprint: "owner"}); err == nil {
		t.Fatal("owner/project active limit bypassed")
	}
	lease, err := s.ClaimGithubAudit(ctx, "synthetic-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevokeGithubAuditAuthorization(ctx, grant.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	terminal, err := s.GetGithubAuditJob(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Outcome != "cancelled" || terminal.ErrorCode != "authorization_changed" {
		t.Fatalf("revocation did not durably fence %+v", terminal)
	}
	if err = s.HeartbeatGithubAudit(ctx, lease, "validating"); err == nil {
		t.Fatal("revoked heartbeat succeeded")
	}
	if err = s.SaveGithubAuditEvidence(ctx, lease, GithubAuditEvidence{AuditID: run.ID}); err == nil {
		t.Fatal("revoked capture succeeded")
	}
	if err = s.PublishGithubAudit(ctx, lease, nil, "complete", ""); err == nil {
		t.Fatal("revoked publication succeeded")
	}
	if _, _, err = s.StartGithubAudit(ctx, p.ID, agent.ID, input); err == nil {
		t.Fatal("revoked idempotent replay succeeded")
	}
	retained, err := s.GetGithubAuditAuthorization(ctx, grant.ID)
	if err != nil || retained.RevokedAt == nil {
		t.Fatalf("grant was not retained %+v %v", retained, err)
	}
	history, err := s.GetAuditRun(ctx, run.ID)
	if err != nil || history.Status != "failed" {
		t.Fatalf("run projection %+v %v", history, err)
	}
	// Additive columns keep retained-binary INSERTs valid, with human owner fallback.
	timestamp := now()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO audit_runs(id,project_id,actor_id,scope,status,started_at,created_at,updated_at) VALUES('legacy-human',?,?,'github','queued',?,?,?)`, p.ID, owner.ID, timestamp, timestamp, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO github_audit_jobs(audit_id,project_id,actor_id,request_key,fingerprint,selection_version,model,effort,capture_json,deadline,task_count,created_at,updated_at) VALUES('legacy-human',?,?,'legacy','legacy',?,'synthetic','','{}',?,1,?,?)`, p.ID, owner.ID, selection.Version, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), timestamp, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetGithubAuditJob(ctx, "legacy-human")
	if err != nil || legacy.AccountOwnerID != owner.ID {
		t.Fatalf("legacy ownership %+v %v", legacy, err)
	}
	t.Logf("synthetic receipt: grant retained; job=%s; run=%s; late writes denied; legacy insert readable", terminal.Outcome, history.Status)
}
