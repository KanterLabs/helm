package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// This uses the provider HTTP fixture for observations; the decorator changes
// exactly the remote fact under test after preview, preserving its generation.
type correctionDriftProvider struct {
	githubprovider.Provider
	generation                uint64
	branch, denied, check, pr bool
	afterResolve              func()
}

func (p *correctionDriftProvider) Generation() uint64        { return p.generation }
func (p *correctionDriftProvider) AccessAllowed(string) bool { return !p.denied }
func (p *correctionDriftProvider) Resolve(ctx context.Context, repo, ref string) (githubprovider.ResolvedRef, error) {
	r, e := p.Provider.Resolve(ctx, repo, ref)
	if p.branch {
		r.SHA = retrievalHead
	}
	if p.afterResolve != nil {
		p.afterResolve()
	}
	return r, e
}
func (p *correctionDriftProvider) FetchPullRequest(ctx context.Context, repo string, n int) (githubprovider.PullRequestSnapshot, error) {
	r, e := p.Provider.FetchPullRequest(ctx, repo, n)
	if p.check {
		r.ChecksState = "failure"
	}
	if p.pr {
		r.State = "closed"
	}
	return r, e
}

type disconnectedCorrectionRunner struct{ GithubEvidenceRunner }

func (r disconnectedCorrectionRunner) Account(context.Context, string, bool) (codexruntime.AccountStatus, error) {
	return codexruntime.AccountStatus{}, errors.New("synthetic account disconnected")
}

func TestAuditCorrectionHTTPRemoteDrift(t *testing.T) {
	for _, kind := range []string{"branch", "access", "check", "pr", "provider", "account", "policy", "generation-during-item"} {
		t.Run(kind, func(t *testing.T) {
			f := newCorrectionFixture(t, "complete-pr")
			p := &correctionDriftProvider{Provider: f.s.GithubAudits.provider, generation: f.s.GithubAudits.provider.(interface{ Generation() uint64 }).Generation()}
			f.s.GithubAudits.provider = p
			code, raw := f.post(t, "/apply", f.request(true, "", nil), "")
			r := correctionRow(t, code, raw)
			token, _ := r["confirmation_token"].(string)
			if token == "" {
				t.Fatalf("preview %s", raw)
			}
			switch kind {
			case "branch":
				p.branch = true
			case "access":
				p.denied = true
			case "check":
				p.check = true
			case "pr":
				p.pr = true
			case "provider":
				p.generation++
			case "account":
				f.s.GithubAudits.runner = disconnectedCorrectionRunner{f.s.GithubAudits.runner}
			case "policy":
				f.s.GithubAudits.guard = func(context.Context, string, string) error { return store.ErrForbidden }
			case "generation-during-item":
				p.afterResolve = func() { p.generation++ }
			}
			before := f.snapshot(t)
			code, raw = f.post(t, "/apply", f.request(false, token, nil), "remote-"+kind)
			r = correctionRow(t, code, raw)
			if r["outcome"] != "skipped" {
				t.Fatalf("stale %s", raw)
			}
			if f.snapshot(t) != before {
				t.Fatal("source drift changed lifecycle")
			}
		})
	}
}

func TestAuditCorrectionHTTPMixedBatch(t *testing.T) {
	f := newCorrectionFixture(t, "complete", 2)
	findings, _, err := f.data.ListAuditFindings(context.Background(), f.job.AuditID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{}
	for _, finding := range findings {
		items = append(items, map[string]any{"finding_id": finding.ID})
	}
	request := map[string]any{"assessment_version": 2, "preview": true, "items": items}
	code, raw := f.post(t, "/apply", request, "")
	if code != 200 {
		t.Fatalf("preview %d %s", code, raw)
	}
	var preview struct {
		Data []store.GithubAuditCorrectionResult `json:"data"`
	}
	json.Unmarshal(raw, &preview)
	if len(preview.Data) != 3 {
		t.Fatalf("preview %s", raw)
	}
	for i, r := range preview.Data {
		if r.Outcome != "eligible" {
			t.Fatalf("preview %s", raw)
		}
		items[i]["confirmation_token"] = r.ConfirmationToken
	}
	if _, err = f.data.DB.Exec(`UPDATE tasks SET version=version+1 WHERE id=?`, findings[1].TaskID); err != nil {
		t.Fatal(err)
	}
	// A real SQLite write failure in the third item must roll back only that item.
	if _, err = f.data.DB.Exec(`CREATE TRIGGER synthetic_correction_failure BEFORE UPDATE ON tasks WHEN OLD.id='` + findings[2].TaskID + `' BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	request["preview"] = false
	code, raw = f.post(t, "/apply", request, "mixed-once")
	if code != 200 {
		t.Fatalf("apply %d %s", code, raw)
	}
	var receipt struct {
		Data []store.GithubAuditCorrectionResult `json:"data"`
	}
	json.Unmarshal(raw, &receipt)
	for i, want := range []string{"applied", "skipped", "failed"} {
		if receipt.Data[i].Outcome != want {
			t.Fatalf("mixed %s", raw)
		}
	}
	code, retry := f.post(t, "/apply", request, "mixed-once")
	if code != 200 {
		t.Fatal(code)
	}
	json.Unmarshal(retry, &receipt)
	for i, want := range []string{"already_applied", "skipped", "failed"} {
		if receipt.Data[i].Outcome != want {
			t.Fatalf("retry %s", retry)
		}
	}
	correctionArtifact(t, "mixed-batch", map[string]any{"apply": json.RawMessage(raw), "retry": json.RawMessage(retry)})
	// Reusing a logical request key with a changed body is a conflict, never a new approval.
	items[0]["reason"] = "different intent"
	code, _ = f.post(t, "/apply", request, "mixed-once")
	if code != 409 {
		t.Fatalf("key reuse %d", code)
	}
}
