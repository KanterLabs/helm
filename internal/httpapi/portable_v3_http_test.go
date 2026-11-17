package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// HTTP failure scenarios: implicit exports must select complete v4; explicit
// v3 must identify its body/download correctly; unsupported, empty, or repeated
// versions must fail closed. Retired GitHub evidence must remain denied with
// zero findings, live jobs, retained imported history, or cached review responses.
// Denial must happen before JSON, bundle, or finding bytes can expose source text;
// generic human audit findings and legacy v2 exports must stay usable.
func TestPortableHTTPExportVersionNegotiation(t *testing.T) {
	s, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("VERSION"), Name: stringPtr("Synthetic export")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/v1/export", "/api/v1/projects/" + project.ID + "/export"} {
		for _, tc := range []struct {
			query   string
			version int
		}{{"", 4}, {"?version=2", 2}, {"?version=3", 3}, {"?version=4", 4}} {
			response := request(t, s, http.MethodGet, route+tc.query, nil, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("export %s%s: status=%d body=%s", route, tc.query, response.Code, response.Body.String())
			}
			var archive store.PortableArchive
			if err := json.Unmarshal(response.Body.Bytes(), &archive); err != nil {
				t.Fatal(err)
			}
			version := strconv.Itoa(tc.version)
			if archive.Version != tc.version || response.Header().Get("X-Helm-Portable-Version") != version || response.Header().Get("Content-Disposition") != `attachment; filename="helm-portable-v`+version+`.json"` {
				t.Fatalf("version=%d headers=%v", archive.Version, response.Header())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Helm-Portable-Format") != store.PortableFormat {
				t.Fatalf("export headers=%v", response.Header())
			}
		}
		for _, query := range []string{"?version=1", "?version=5", "?version=three", "?version=", "?version=3&version=2", "?version=3&version=3"} {
			response := request(t, s, http.MethodGet, route+query, nil, nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid version %s: status=%d body=%s", query, response.Code, response.Body.String())
			}
		}
	}
}

func TestPortableV3HTTPDeniesRetiredGithubEvidence(t *testing.T) {
	for _, findings := range []bool{false, true} {
		t.Run("findings="+strconv.FormatBool(findings), func(t *testing.T) {
			s, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, err := data.EnsureDisabledActor(ctx)
			if err != nil {
				t.Fatal(err)
			}
			project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("PRIVATE"), Name: stringPtr("Synthetic private source")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Synthetic task")}, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := data.StartGithubAudit(ctx, project.ID, actor.ID, store.GithubAuditStartInput{Repositories: selection.Repositories, SelectionVersion: selection.Version, TaskIDs: []string{task.ID}, RequestKey: "export", Fingerprint: "export"})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := data.ClaimGithubAudit(ctx, "portable-export-test", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			evidence := store.GithubAuditEvidence{AuditID: run.ID, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Repositories: []store.GithubAuditRepository{{Repository: "example/synthetic", Ref: "main", SHA: strings.Repeat("a", 40), RepositoryID: 1}}, Items: []store.GithubAuditItem{{ID: "github:file:1", Kind: "file", Text: "SYNTHETIC_PRIVATE_EXPORT_TEXT"}}, Tasks: []store.GithubAuditTask{}, Omissions: []string{}}
			if err = data.SaveGithubAuditEvidence(ctx, lease, evidence); err != nil {
				t.Fatal(err)
			}
			if findings {
				if err = data.HeartbeatGithubAudit(ctx, lease, "validating"); err != nil {
					t.Fatal(err)
				}
				err = data.PublishGithubAudit(ctx, lease, []store.AuditFindingInput{{TaskID: task.ID, Verdict: "correct", Confidence: 0.8, Reason: "SYNTHETIC_PRIVATE_FINDING_REASON", EvidenceRefs: []string{"github:file:1"}}}, "complete", "")
			} else {
				_, err = data.CancelGithubAudit(ctx, run.ID, "cancelled")
			}
			if err != nil {
				t.Fatal(err)
			}
			// Exercise both pre-removal jobs and history retained through portable import.
			for _, retained := range []bool{false, true} {
				if retained {
					raw, err := json.Marshal(evidence)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = data.DB.ExecContext(ctx, `INSERT INTO github_audit_retained_evidence(audit_id,evidence_json) VALUES (?,?)`, run.ID, string(raw)); err != nil {
						t.Fatal(err)
					}
					if _, err = data.DB.ExecContext(ctx, `DELETE FROM github_audit_jobs WHERE audit_id=?`, run.ID); err != nil {
						t.Fatal(err)
					}
				}
				var proof []map[string]any
				for _, route := range []string{
					"/api/v1/projects/" + project.ID + "/export?version=3",
					"/api/v1/projects/" + project.ID + "/export?version=4",
					"/api/v1/projects/" + project.ID + "/export",
					"/api/v1/projects/" + project.ID + "/export?format=bundle",
					"/api/v1/export?version=3", "/api/v1/export?version=4", "/api/v1/export?format=bundle",
					"/api/v1/audits/" + run.ID + "/findings",
				} {
					denied := request(t, s, http.MethodGet, route, nil, nil)
					if denied.Code != http.StatusConflict || strings.Contains(denied.Body.String(), "SYNTHETIC_PRIVATE_") {
						t.Fatalf("retired source retained=%t route=%s status=%d body=%s", retained, route, denied.Code, denied.Body.String())
					}
					proof = append(proof, map[string]any{"route": route, "status": denied.Code, "error": json.RawMessage(denied.Body.Bytes())})
				}
				if findings {
					values, _, err := data.ListAuditFindings(ctx, run.ID, 50, 0)
					if err != nil || len(values) != 1 {
						t.Fatalf("findings=%v err=%v", values, err)
					}
					denied := request(t, s, http.MethodPatch, "/api/v1/audit-findings/"+values[0].ID, map[string]any{"review_state": "approved"}, map[string]string{"If-Match": auditFindingETag(values[0]), "Idempotency-Key": "retired-review"})
					if denied.Code != http.StatusConflict || strings.Contains(denied.Body.String(), "SYNTHETIC_PRIVATE_") {
						t.Fatalf("retired review: status=%d body=%s", denied.Code, denied.Body.String())
					}
				}
				proofDir, err := os.MkdirTemp("", "helm-portable-retired-proof-")
				if err != nil {
					t.Fatal(err)
				}
				body, err := json.MarshalIndent(proof, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				proofPath := filepath.Join(proofDir, "denied-http-responses.json")
				if err = os.WriteFile(proofPath, body, 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("Synthetic retired source denial artifact: %s", proofPath)
			}
			route := "/api/v1/projects/" + project.ID + "/export?version=3"
			legacy := request(t, s, http.MethodGet, strings.TrimSuffix(route, "?version=3")+"?version=2", nil, nil)
			if legacy.Code != http.StatusOK || strings.Contains(legacy.Body.String(), "SYNTHETIC_PRIVATE_") {
				t.Fatalf("legacy export: status=%d body=%s", legacy.Code, legacy.Body.String())
			}
		})
	}
}
