package httpapi

import "testing"

// Failure inventory: protected GitHub finding prose can enter general task
// comments/events through the normal lifecycle reason. Keep provenance public,
// and use the already-public task blocker rather than protected model prose.
func TestAuditCorrectionHTTPBlockKeepsSourceReasonProtected(t *testing.T) {
	f := newCorrectionFixture(t, "block-private")
	code, raw := f.post(t, "/apply", f.request(true, "", nil), "")
	preview := correctionRow(t, code, raw)
	if preview["outcome"] != "eligible" {
		t.Fatalf("preview %s", raw)
	}
	code, raw = f.post(t, "/apply", f.request(false, preview["confirmation_token"].(string), nil), "block-source-privacy")
	if correctionRow(t, code, raw)["outcome"] != "applied" {
		t.Fatalf("apply %s", raw)
	}
	var leaked, retained, blocker int
	if err := f.data.DB.QueryRow(`SELECT (SELECT count(*) FROM comments WHERE body LIKE '%PRIVATE_SOURCE_REASON_SENTINEL%')+(SELECT count(*) FROM events WHERE payload LIKE '%PRIVATE_SOURCE_REASON_SENTINEL%')`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if err := f.data.DB.QueryRow(`SELECT count(*) FROM audit_findings WHERE reason='PRIVATE_SOURCE_REASON_SENTINEL'`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if err := f.data.DB.QueryRow(`SELECT count(*) FROM comments WHERE task_id=? AND body='Waiting for external approval.'`, f.task.ID).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 || retained != 1 || blocker != 1 {
		t.Fatalf("source privacy lost: leaked=%d retained=%d public_blocker=%d", leaked, retained, blocker)
	}
}
