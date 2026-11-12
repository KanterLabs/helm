package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// Endpoint failures recorded in test/githubbeta/FAILURES.md, "Branches view":
// no selection, an unselected repository, foreign or under-scoped tokens and
// revoked upstream access must never reach or reveal private branch data.
func TestProjectGithubBranchesAdmissionAndStates(t *testing.T) {
	const credential = "SYNTHETIC_BRANCH_SENTINEL"
	tips := map[string]string{"main": strings.Repeat("1", 40), "beta": strings.Repeat("2", 40), "feat/private-branch": strings.Repeat("3", 40)}
	counts := map[string][2]int{tips["main"]: {0, 0}, tips["beta"]: {1, 2}, tips["feat/private-branch"]: {18, 3}}
	var requests atomic.Int64
	var revoked atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if revoked.Load() || r.Header.Get("Authorization") != "Bearer "+credential {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/repos/acme/widgets/branches":
			listed := []map[string]any{}
			for name, sha := range tips {
				listed = append(listed, map[string]any{"name": name, "commit": map[string]string{"sha": sha}})
			}
			_ = json.NewEncoder(w).Encode(listed)
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/compare/"):
			source, target, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/compare/"), "...")
			ahead, behind := counts[source][0], counts[source][1]
			if target == tips["beta"] {
				ahead, behind = 0, 3
			}
			fmt.Fprintf(w, `{"ahead_by":%d,"behind_by":%d,"base_commit":{"sha":%q,"commit":{"committer":{"date":"2026-11-01T18:06:44Z"}}}}`, ahead, behind, source)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "synthetic-token")
	if err := os.WriteFile(tokenPath, []byte(credential), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := newDependencyHTTPFixture(t, "GHBR")
	fixture.server.Github = NewGithubService(fixture.data, githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: tokenPath,
		Repositories: []string{"acme/widgets", "acme/unselected"}, HTTPClient: upstream.Client(), Endpoint: upstream.URL}))
	ctx := context.Background()
	path := "/api/v1/projects/" + fixture.project.ID + "/github/branches"
	type reply struct {
		State        string                    `json:"state"`
		Message      string                    `json:"message"`
		Repository   string                    `json:"repository"`
		Repositories []string                  `json:"repositories"`
		Base         *githubprovider.BranchTip `json:"base"`
		Staging      *githubprovider.Branch    `json:"staging"`
		Branches     []githubprovider.Branch   `json:"branches"`
		Partial      bool                      `json:"partial"`
	}
	read := func(target string, headers map[string]string) (*httptest.ResponseRecorder, reply) {
		t.Helper()
		response := request(t, fixture.server, http.MethodGet, target, nil, headers)
		if strings.Contains(response.Body.String(), credential) {
			t.Fatal("credential in response")
		}
		var decoded reply
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode: %v %s", err, response.Body.String())
			}
		}
		return response, decoded
	}

	unselected, empty := read(path, nil)
	if unselected.Code != http.StatusOK || empty.State != "no_repository" || empty.Message == "" || empty.Base != nil || len(empty.Branches) != 0 || empty.Repositories == nil || requests.Load() != 0 {
		t.Fatalf("no selection: %d %s requests=%d", unselected.Code, unselected.Body.String(), requests.Load())
	}
	if _, err := fixture.data.UpdateGithubSelection(ctx, fixture.project.ID, []store.GithubRepository{{Repository: "acme/widgets", Ref: "main"}, {Repository: "acme/widgets", Ref: "beta"}}, 1); err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{"acme/unselected", "elsewhere/repo", "not a repository"} {
		rejected := request(t, fixture.server, http.MethodGet, path+"?repository="+strings.ReplaceAll(other, " ", "%20"), nil, nil)
		if rejected.Code != http.StatusBadRequest || responseErrorCode(t, rejected.Body.Bytes()) != "invalid_request" || requests.Load() != 0 {
			t.Fatalf("unselected %q: %d %s requests=%d", other, rejected.Code, rejected.Body.String(), requests.Load())
		}
	}
	otherProject, err := fixture.data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GHBO"), Name: stringPtr("Other project")}, fixture.actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, foreign := dependencyHTTPToken(t, fixture, "foreign reader", []string{"tasks:read"}, []string{otherProject.ID}, "agent")
	_, writer := dependencyHTTPToken(t, fixture, "writer only", []string{"tasks:write"}, []string{fixture.project.ID}, "agent")
	_, reader := dependencyHTTPToken(t, fixture, "project reader", []string{"tasks:read"}, []string{fixture.project.ID}, "agent")
	for name, token := range map[string]string{"foreign project": foreign, "missing scope": writer} {
		if denied, _ := read(path, map[string]string{"Authorization": "Bearer " + token}); denied.Code != http.StatusForbidden || requests.Load() != 0 {
			t.Fatalf("%s: %d %s requests=%d", name, denied.Code, denied.Body.String(), requests.Load())
		}
	}
	if posted := request(t, fixture.server, http.MethodPost, path, map[string]string{}, map[string]string{"Content-Type": "application/json"}); posted.Code != http.StatusMethodNotAllowed || requests.Load() != 0 {
		t.Fatalf("post: %d requests=%d", posted.Code, requests.Load())
	}

	ready, report := read(path+"?repository=Acme/Widgets", map[string]string{"Authorization": "Bearer " + reader})
	if ready.Code != http.StatusOK || report.State != "ready" || report.Repository != "acme/widgets" || len(report.Repositories) != 1 || report.Partial {
		t.Fatalf("ready: %d %s", ready.Code, ready.Body.String())
	}
	if report.Base == nil || report.Base.Name != "main" || report.Staging == nil || report.Staging.Ahead != 2 || report.Staging.Behind != 1 {
		t.Fatalf("trunks: %s", ready.Body.String())
	}
	if len(report.Branches) != 1 || report.Branches[0].Name != "feat/private-branch" || report.Branches[0].Ahead != 3 || report.Branches[0].Behind != 18 || report.Branches[0].InStaging == nil || *report.Branches[0].InStaging {
		t.Fatalf("branches: %s", ready.Body.String())
	}
	if ready.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("private branch data must not be cached: %q", ready.Header().Get("Cache-Control"))
	}

	revoked.Store(true)
	lost, hidden := read(path, nil)
	if lost.Code != http.StatusOK || hidden.State != "permission_required" || hidden.Message == "" || hidden.Base != nil || hidden.Staging != nil || len(hidden.Branches) != 0 || strings.Contains(lost.Body.String(), "feat/private-branch") {
		t.Fatalf("revoked access still shows branches: %d %s", lost.Code, lost.Body.String())
	}
	evidence := map[string]json.RawMessage{"no_selection": unselected.Body.Bytes(), "ready": ready.Body.Bytes(), "revoked": lost.Body.Bytes()}
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	artifact := filepath.Join(githubResponseEvidenceDir(t), "github-branches-http-responses.json")
	if err = os.WriteFile(artifact, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("sanitized branch evidence: %s", artifact)
}
