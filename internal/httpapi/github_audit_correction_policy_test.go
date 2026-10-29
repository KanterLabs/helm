package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func TestAuditCorrectionHTTPRequiredChecklistRemainsRecommendation(t *testing.T) {
	f := newCorrectionFixture(t, "complete-checklist")
	code, raw := f.post(t, "/apply", f.request(true, "", nil), "")
	r := correctionRow(t, code, raw)
	if r["outcome"] != "skipped" || r["target_column_id"] != *f.finding.Assessment.TargetColumnID {
		t.Fatalf("blocked recommendation disappeared %s", raw)
	}
	task, _ := f.data.GetTask(context.Background(), f.task.ID)
	if task.Version != f.task.Version {
		t.Fatal("checklist block changed card")
	}
	correctionArtifact(t, "required-checklist", json.RawMessage(raw))
}
func TestAuditCorrectionHTTPCustomDestinationAndOwnClaim(t *testing.T) {
	f := newCorrectionFixture(t, "complete-custom")
	// Capture a fresh assessment of the claimed snapshot, exercising the normal
	// human owner completion rule without ever stealing/releasing a foreign claim.
	ctx := context.Background()
	claimed, err := f.data.ClaimTask(ctx, f.task.ID, f.actor.ID, time.Minute, f.task.Version)
	if err != nil {
		t.Fatal(err)
	}
	code, raw := f.post(t, "/start", map[string]any{}, "claimed-start")
	if code != 202 {
		t.Fatalf("start %d %s", code, raw)
	}
	var start GithubAuditStartResponse
	json.Unmarshal(raw, &start)
	if err = f.s.GithubAudits.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	f.job, _ = f.data.GetGithubAuditJob(ctx, start.Audit.ID)
	findings, _, _ := f.data.ListAuditFindings(ctx, start.Audit.ID, 10, 0)
	f.finding = findings[0]
	code, raw = f.post(t, "/apply", f.request(true, "", nil), "")
	r := correctionRow(t, code, raw)
	if r["outcome"] != "eligible" {
		t.Fatalf("owned preview %s", raw)
	}
	token := r["confirmation_token"].(string)
	code, raw = f.post(t, "/apply", f.request(false, token, nil), "owned-apply")
	r = correctionRow(t, code, raw)
	if r["outcome"] != "applied" {
		t.Fatalf("owned apply %s", raw)
	}
	task, _ := f.data.GetTask(ctx, f.task.ID)
	col, _ := f.data.GetColumn(ctx, task.ColumnID)
	if task.Version != claimed.Version+1 || task.ClaimedBy != nil || col.Name != "Verified and shipped" || task.ColumnID != *f.finding.Assessment.TargetColumnID {
		t.Fatalf("wrong lifecycle result %+v %+v", task, col)
	}
	correctionArtifact(t, "custom-column-own-claim", json.RawMessage(raw))
}
func TestAuditCorrectionHTTPImportedHistoryHasNoApplyAuthority(t *testing.T) {
	source := newCorrectionFixture(t, "complete")
	ctx := context.Background()
	archive, err := source.data.ExportPortableVersion(ctx, []string{source.project.ID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	target, d := testServer(t, "disabled")
	actor, _ := d.EnsureDisabledActor(ctx)
	_, err = d.ImportPortable(ctx, archive, store.PortableImportOptions{ActorID: actor.ID})
	if err != nil {
		t.Fatal(err)
	}
	var auditID string
	if err = d.DB.QueryRow(`SELECT audit_id FROM audit_imports LIMIT 1`).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/apply", nil)
	target.githubAuditJob(response, req, auth.Identity{Actor: actor}, auditID, "apply")
	if response.Code != 404 {
		t.Fatalf("import apply %d %s", response.Code, response.Body.String())
	}
	var n int
	if err = d.DB.QueryRow(`SELECT count(*) FROM github_audit_correction_receipts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("imported operational authority", err, n)
	}
	correctionArtifact(t, "import-history", map[string]any{"http_status": response.Code, "operational_receipts": n})
}
