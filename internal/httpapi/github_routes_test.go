package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// Exercise the actual HTTP admission/router/store boundary. Disabled provider
// access must preserve legitimate explicit links without claiming verification.
func TestGithubArtifactHTTPUnverifiedRoundTrip(t *testing.T) {
	server, data := testServer(t, "disabled")
	server.Github = NewGithubService(data, githubprovider.New(githubprovider.Options{Enabled: true, Repositories: []string{"acme/widgets"}}))
	ctx := context.Background()
	actor, err := data.CreateActor(ctx, store.Actor{Kind: "human", Name: "Synthetic artifact owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPointerGithub("GHP"), Name: stringPointerGithub("Synthetic PRs")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPointerGithub("Synthetic linked task")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "acme/widgets", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Content-Type": "application/json", "If-Match": fmt.Sprintf(`"v%d"`, task.Version)}
	linked := request(t, server, http.MethodPost, "/api/v1/tasks/"+task.ID+"/artifacts", map[string]string{"url": "https://github.com/Acme/Widgets/pull/7?view=files#top"}, headers)
	if linked.Code != http.StatusCreated {
		t.Fatalf("link status=%d %s", linked.Code, linked.Body.String())
	}
	var result struct {
		Artifact    store.GithubArtifact `json:"artifact"`
		TaskVersion int64                `json:"task_version"`
	}
	if err = json.Unmarshal(linked.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Artifact.Snapshot != nil || result.Artifact.Verification == "verified" || result.TaskVersion != task.Version+1 {
		t.Fatalf("false verification: %+v", result)
	}
	listed := request(t, server, http.MethodGet, "/api/v1/tasks/"+task.ID+"/artifacts", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list=%d %s", listed.Code, listed.Body.String())
	}
	summary := request(t, server, http.MethodGet, "/api/v1/artifact-summaries?task_ids="+task.ID, nil, nil)
	if summary.Code != http.StatusOK {
		t.Fatalf("summary=%d %s", summary.Code, summary.Body.String())
	}
	removed := request(t, server, http.MethodDelete, "/api/v1/tasks/"+task.ID+"/artifacts/"+result.Artifact.ID, nil, map[string]string{"If-Match": fmt.Sprintf(`"v%d"`, result.TaskVersion)})
	if removed.Code != http.StatusOK {
		t.Fatalf("detach=%d %s", removed.Code, removed.Body.String())
	}
	artifact := filepath.Join(githubResponseEvidenceDir(t), "github-artifact-http-responses.json")
	evidence := map[string]json.RawMessage{"linked": linked.Body.Bytes(), "listed": listed.Body.Bytes(), "summaries": summary.Body.Bytes(), "detached": removed.Body.Bytes()}
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if err = os.WriteFile(artifact, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("sanitized response evidence: %s", artifact)
}
func stringPointerGithub(value string) *string { return &value }

// Evidence deliberately survives Go's TempDir cleanup for review. Fixtures
// contain only synthetic task/provider data and no credentials.
func githubResponseEvidenceDir(t *testing.T) string {
	t.Helper()
	directory := os.Getenv("HELM_TEST_ARTIFACT_DIR")
	var err error
	if directory == "" {
		directory, err = os.MkdirTemp("", "helm-github-response-evidence-")
	} else {
		err = os.MkdirAll(directory, 0700)
	}
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
