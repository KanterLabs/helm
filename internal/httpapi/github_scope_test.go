package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func TestGithubArtifactLinkRequiresReadAndWriteScopes(t *testing.T) {
	fixture := newDependencyHTTPFixture(t, "GPSCOPE")
	fixture.server.Github = NewGithubService(fixture.data, githubprovider.New(githubprovider.Options{Enabled: true, Repositories: []string{"acme/widgets"}}))
	ctx := context.Background()
	if _, err := fixture.data.UpdateGithubSelection(ctx, fixture.project.ID, []store.GithubRepository{{Repository: "acme/widgets", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	_, writeOnly := dependencyHTTPToken(t, fixture, "artifact write only", []string{"tasks:write"}, []string{fixture.project.ID}, "agent")
	path := "/api/v1/tasks/" + fixture.dependent.ID + "/artifacts"
	payload := map[string]string{"url": "https://github.com/acme/widgets/pull/7"}
	headers := map[string]string{"Authorization": "Bearer " + writeOnly, "Content-Type": "application/json", "If-Match": `"v1"`, "Idempotency-Key": "scope-link"}
	denied := request(t, fixture.server, http.MethodPost, path, payload, headers)
	if denied.Code != http.StatusForbidden || responseErrorCode(t, denied.Body.Bytes()) != "insufficient_scope" {
		t.Fatalf("write-only link status=%d body=%s", denied.Code, denied.Body.String())
	}
	task, err := fixture.data.GetTask(ctx, fixture.dependent.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := fixture.data.ListGithubArtifacts(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Version != fixture.dependent.Version || len(artifacts) != 0 {
		t.Fatalf("denied request mutated task: version=%d artifacts=%d", task.Version, len(artifacts))
	}
	_, readerWriter := dependencyHTTPToken(t, fixture, "artifact reader writer", []string{"tasks:read", "tasks:write"}, []string{fixture.project.ID}, "agent")
	headers["Authorization"] = "Bearer " + readerWriter
	accepted := request(t, fixture.server, http.MethodPost, path, payload, headers)
	if accepted.Code != http.StatusCreated {
		t.Fatalf("reader/writer link status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	headers["Authorization"] = "Bearer " + writeOnly
	replay := request(t, fixture.server, http.MethodPost, path, payload, headers)
	if replay.Code != http.StatusForbidden || responseErrorCode(t, replay.Body.Bytes()) != "insufficient_scope" {
		t.Fatalf("write-only replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	evidence := map[string]json.RawMessage{"write_only": denied.Body.Bytes(), "reader_writer": accepted.Body.Bytes(), "write_only_replay": replay.Body.Bytes()}
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	artifactPath := filepath.Join(githubResponseEvidenceDir(t), "github-link-scope-responses.json")
	if err = os.WriteFile(artifactPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("sanitized scope evidence: %s", artifactPath)
}
