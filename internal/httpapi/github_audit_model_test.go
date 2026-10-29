package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KanterLabs/helm/internal/codexruntime"
)

type auditProfileRecorder struct {
	assessmentAI
	model, effort string
}

func (a *auditProfileRecorder) DraftEvidence(ctx context.Context, owner string, request codexruntime.RunRequest) (codexruntime.RunResult, error) {
	a.model, a.effort = request.Model, request.Effort
	return a.assessmentAI.DraftEvidence(ctx, owner, request)
}

func TestGithubAuditServerProfileCapturedAcrossReplayAndExecution(t *testing.T) {
	f := newCorrectionFixture(t, "complete")
	if f.job.Model != "gpt-5.6-sol" || f.job.Effort != "medium" {
		t.Fatalf("audit default = %s/%s", f.job.Model, f.job.Effort)
	}
	f.s.Cfg.AuditModel, f.s.Cfg.AuditEffort = "gpt-5.6-sol", "high"
	f.s.Cfg.CodexModel, f.s.Cfg.CodexEffort = "everyday-model", "low"
	for _, body := range []map[string]any{{"model": "untrusted-model"}, {"effort": "max"}} {
		status, _ := f.post(t, "/start", body, "client-profile")
		if status != 400 {
			t.Fatalf("client profile accepted: %d", status)
		}
	}
	status, raw := f.post(t, "/start", map[string]any{}, "captured-profile")
	if status != 202 {
		t.Fatalf("start: %d", status)
	}
	var started GithubAuditStartResponse
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatal(err)
	}
	job, err := f.data.GetGithubAuditJob(context.Background(), started.Audit.ID)
	if err != nil || job.Model != "gpt-5.6-sol" || job.Effort != "high" {
		t.Fatalf("profile not captured: %s/%s %v", job.Model, job.Effort, err)
	}
	f.s.Cfg.AuditModel, f.s.Cfg.AuditEffort = "gpt-6-sol", "low"
	status, raw = f.post(t, "/start", map[string]any{}, "captured-profile")
	var replay GithubAuditStartResponse
	if err = json.Unmarshal(raw, &replay); err != nil || status != 202 || replay.Audit.ID != started.Audit.ID {
		t.Fatalf("configuration change broke replay: status=%d error=%v", status, err)
	}
	ai := &auditProfileRecorder{}
	f.s.GithubAudits.runner = ai
	if err = f.s.GithubAudits.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ai.model != "gpt-5.6-sol" || ai.effort != "high" {
		t.Fatalf("worker ignored captured profile: %s/%s", ai.model, ai.effort)
	}
	old, err := f.data.GetGithubAuditJob(context.Background(), f.job.AuditID)
	if err != nil || old.Model != f.job.Model || old.Effort != f.job.Effort {
		t.Fatal("existing job profile changed")
	}
	if f.s.Cfg.CodexModel != "everyday-model" || f.s.Cfg.CodexEffort != "low" {
		t.Fatal("audit profile changed everyday AI")
	}
}
