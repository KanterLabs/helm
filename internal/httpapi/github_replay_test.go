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
	"testing"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func TestGithubLinkReplayHidesRevokedPrivateSnapshot(t *testing.T) {
	sha := strings.Repeat("a", 40)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/pulls/7/reviews"):
			fmt.Fprint(w, `[]`)
		case strings.Contains(r.URL.Path, "/check-runs"):
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case strings.Contains(r.URL.Path, "/statuses"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			fmt.Fprintf(w, `{"title":"Synthetic private title","state":"open","head":{"sha":%q},"base":{"sha":%q,"repo":{"id":123}}}`, sha, sha)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "synthetic-token")
	if err := os.WriteFile(tokenPath, []byte("SYNTHETIC_TEST_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	server, data := testServer(t, "disabled")
	server.Github = NewGithubService(data, githubprovider.New(githubprovider.Options{Enabled: true, TokenFile: tokenPath, Repositories: []string{"acme/widgets"}, HTTPClient: upstream.Client(), Endpoint: upstream.URL}))
	ctx := context.Background()
	actor, err := data.CreateActor(ctx, store.Actor{Kind: "human", Name: "Synthetic replay owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPointerGithub("GRP"), Name: stringPointerGithub("Replay privacy")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPointerGithub("Replay task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "acme/widgets", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json", "If-Match": `"v1"`, "Idempotency-Key": "synthetic-link-replay"}
	payload := map[string]string{"url": "https://github.com/acme/widgets/pull/7"}
	path := "/api/v1/tasks/" + task.ID + "/artifacts"
	first := request(t, server, http.MethodPost, path, payload, headers)
	if first.Code != 201 || !strings.Contains(first.Body.String(), "Synthetic private title") {
		t.Fatalf("initial verification: %d %s", first.Code, first.Body.String())
	}
	if err = os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	replay := request(t, server, http.MethodPost, path, payload, headers)
	if replay.Code != 201 || strings.Contains(replay.Body.String(), "Synthetic private title") {
		t.Fatalf("private replay after revocation: %d %s", replay.Code, replay.Body.String())
	}
	var evidence json.RawMessage = replay.Body.Bytes()
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	artifact := filepath.Join(githubResponseEvidenceDir(t), "github-revoked-replay.json")
	if err = os.WriteFile(artifact, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("sanitized revoked replay evidence: %s", artifact)
}
