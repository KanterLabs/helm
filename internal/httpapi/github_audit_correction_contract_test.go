package httpapi

import (
	"encoding/json"
	"testing"
)

// Public contract journeys are specified before authoring the OpenAPI fragment.
func TestAuditCorrectionOpenAPIContract(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(openAPIDocument, &doc); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"GithubAuditCorrectionRequest", "GithubAuditCorrectionInput", "GithubAuditCorrectionResult", "GithubAuditCorrectionResponse"} {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
}
func TestAuditCorrectionHTTPMalformedVersionNeverFallsBack(t *testing.T) {
	f := newCorrectionFixture(t, "complete")
	for _, request := range []any{
		map[string]any{"assessment_version": 1, "preview": true, "items": []any{}},
		map[string]any{"assessment_version": nil, "preview": true, "finding_ids": []string{f.finding.ID}},
		map[string]any{"assessment_version": 2, "preview": true, "items": []any{map[string]any{"finding_id": f.finding.ID, "invented": true}}},
	} {
		code, raw := f.post(t, "/apply", request, "")
		if code != 400 {
			t.Fatalf("malformed HTTP %d %s", code, raw)
		}
	}
	code, raw := f.post(t, "/apply", map[string]any{"preview": true, "finding_ids": []string{f.finding.ID}}, "")
	row := correctionRow(t, code, raw)
	if row["outcome"] != "skipped" {
		t.Fatalf("v2 bypass via legacy %s", raw)
	}
}
