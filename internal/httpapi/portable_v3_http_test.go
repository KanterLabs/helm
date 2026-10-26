package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// HTTP failure scenarios: implicit exports must retain v2 compatibility; explicit
// v3 must identify its body/download correctly; unsupported, empty, or repeated
// versions must fail closed. Retained private evidence must be reauthorized even
// with zero findings, and loss of provider access or service availability must
// reject the entire archive without returning source text or finding reasons.
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
		}{{"", 2}, {"?version=2", 2}, {"?version=3", 3}} {
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
		for _, query := range []string{"?version=1", "?version=4", "?version=three", "?version=", "?version=3&version=2", "?version=3&version=3"} {
			response := request(t, s, http.MethodGet, route+query, nil, nil)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid version %s: status=%d body=%s", query, response.Code, response.Body.String())
			}
		}
	}
}

func TestPortableV3HTTPRevalidatesEveryRetainedEvidence(t *testing.T) {
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
			var revoked atomic.Bool
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if revoked.Load() {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"message":"SYNTHETIC_PRIVATE_PROVIDER_ERROR"}`))
				} else if strings.Contains(r.URL.Path, "/commits/") {
					_, _ = w.Write([]byte(`{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
				} else {
					_, _ = w.Write([]byte(`{"id":1,"full_name":"example/synthetic","default_branch":"main","permissions":{"pull":true}}`))
				}
			}))
			t.Cleanup(fake.Close)
			token := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(token, []byte("SYNTHETIC_EXPORT_PROVIDER_TOKEN"), 0600); err != nil {
				t.Fatal(err)
			}
			provider := githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: token, Repositories: []string{"example/synthetic"}, HTTPClient: fake.Client(), Endpoint: fake.URL})
			s.GithubAudits = NewGithubAuditService(data, provider, nil, nil)
			route := "/api/v1/projects/" + project.ID + "/export?version=3"
			allowed := request(t, s, http.MethodGet, route, nil, nil)
			if allowed.Code != http.StatusOK || !strings.Contains(allowed.Body.String(), "SYNTHETIC_PRIVATE_EXPORT_TEXT") {
				t.Fatalf("authorized export: status=%d body=%s", allowed.Code, allowed.Body.String())
			}
			// Captured HTTP responses provide a repeatable synthetic proof artifact.
			proofDir, err := os.MkdirTemp("", "helm-portable-v3-proof-")
			if err != nil {
				t.Fatal(err)
			}
			proof := filepath.Join(proofDir, "portable-v3-response.json")
			if err := os.WriteFile(proof, allowed.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			t.Logf("Synthetic portable HTTP response: %s", proof)
			revoked.Store(true)
			for _, exportRoute := range []string{route, "/api/v1/export?version=3"} {
				denied := request(t, s, http.MethodGet, exportRoute, nil, nil)
				if denied.Code != http.StatusConflict || strings.Contains(denied.Body.String(), "SYNTHETIC_PRIVATE_") || strings.Contains(denied.Body.String(), "SYNTHETIC_EXPORT_PROVIDER_TOKEN") {
					t.Fatalf("revoked export: status=%d body=%s", denied.Code, denied.Body.String())
				}
			}
			s.GithubAudits = nil
			unavailable := request(t, s, http.MethodGet, route, nil, nil)
			if unavailable.Code != http.StatusConflict || strings.Contains(unavailable.Body.String(), "SYNTHETIC_PRIVATE_") {
				t.Fatalf("unavailable source: status=%d body=%s", unavailable.Code, unavailable.Body.String())
			}
			legacy := request(t, s, http.MethodGet, strings.TrimSuffix(route, "?version=3"), nil, nil)
			if legacy.Code != http.StatusOK || strings.Contains(legacy.Body.String(), "SYNTHETIC_PRIVATE_") {
				t.Fatalf("legacy export: status=%d body=%s", legacy.Code, legacy.Body.String())
			}
		})
	}
}
