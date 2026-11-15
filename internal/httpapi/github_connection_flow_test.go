package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/githubconnect"
	"github.com/KanterLabs/helm/internal/store"
)

// No ceiling configured: HELM_GITHUB_REPOSITORIES is not required, and the
// provider allowlist follows connect, sync, a restart (load) and disconnect.
func TestGithubConnectionFlowFollowsTheAllowlist(t *testing.T) {
	e := newConnectionEnv(t)
	before := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if len(before.AllowedRepositories) != 0 || before.Connection.State != githubconnect.StateDisconnected {
		t.Fatalf("before: %+v", before)
	}
	connected := e.connect()
	if want := []string{"example/docs", "example/synthetic"}; !slices.Equal(connected.AllowedRepositories, want) || connected.Connection.Login != "octocat" {
		t.Fatalf("after connect: %+v", connected)
	}
	// check runs Sync first: a newly installed repository appears.
	e.gh.set(func(g *connGitHub) { g.repos = append(g.repos, "Example/Added") })
	checked := e.document(e.call("owner", http.MethodPost, checkRoute, "", "Idempotency-Key", "check-1"))
	if want := []string{"example/added", "example/docs", "example/synthetic"}; !slices.Equal(checked.AllowedRepositories, want) {
		t.Fatalf("after check: %v", checked.AllowedRepositories)
	}
	if checked.State != "connected" {
		t.Fatalf("check did not verify access: %+v", checked)
	}
	// Restart: a new manager loads the saved connection with no network.
	_ = e.s.StopGithubServices(context.Background())
	requests := e.gh.read(func(g *connGitHub) int { return g.requests })
	e.install()
	if e.gh.read(func(g *connGitHub) int { return g.requests }) != requests {
		t.Fatal("startup contacted GitHub")
	}
	loaded := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if !slices.Equal(loaded.AllowedRepositories, checked.AllowedRepositories) || loaded.Connection.State != githubconnect.StateConnected {
		t.Fatalf("after load: %+v", loaded)
	}
	// Connect while connected is a state problem.
	if code := e.errorCode(e.call("owner", http.MethodPost, connRoute, ""), http.StatusConflict); code != "github_already_connected" {
		t.Fatalf("connect while connected: %s", code)
	}
	gone := e.document(e.call("admin", http.MethodPost, discRoute, ""))
	if len(gone.AllowedRepositories) != 0 || gone.Connection.State != githubconnect.StateDisconnected || gone.Connection.Login != "" {
		t.Fatalf("after disconnect: %+v", gone)
	}
	if got := e.s.Github.Provider().AllowedRepositories(); len(got) != 0 {
		t.Fatalf("provider still allows %v", got)
	}
	// Disconnecting twice is fine.
	e.document(e.call("owner", http.MethodPost, discRoute, ""))
}

func TestGithubConnectionCeilingNarrowsTheAllowlist(t *testing.T) {
	e := newConnectionEnv(t, func(c *config.Config) { c.GitHubRepositories = []string{"example/synthetic", "example/elsewhere"} })
	if got := e.s.Github.Provider().AllowedRepositories(); len(got) != 0 {
		t.Fatalf("the ceiling alone became the allowlist: %v", got)
	}
	connected := e.connect()
	if want := []string{"example/synthetic"}; !slices.Equal(connected.AllowedRepositories, want) {
		t.Fatalf("ceiling not applied: %v", connected.AllowedRepositories)
	}
}

func TestGithubCheckSyncFailures(t *testing.T) {
	e := newConnectionEnv(t)
	if code := e.errorCode(e.call("owner", http.MethodPost, checkRoute, ""), http.StatusConflict); code != "github_not_connected" {
		t.Fatalf("check while disconnected: %s", code)
	}
	e.connect()
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout} {
		e.gh.set(func(g *connGitHub) { g.installFail = status })
		res := e.call("owner", http.MethodPost, checkRoute, "")
		if code := e.errorCode(res, http.StatusFailedDependency); code != "github_unreachable" {
			t.Fatalf("GitHub %d: %s", status, code)
		}
	}
	e.gh.set(func(g *connGitHub) { g.installFail = 0 })
	e.document(e.call("owner", http.MethodPost, checkRoute, ""))
	// Begin while GitHub refuses or fails is 424, never 502/504.
	e.document(e.call("owner", http.MethodPost, discRoute, ""))
	for status, want := range map[int]string{http.StatusForbidden: "github_refused", http.StatusBadGateway: "github_unreachable"} {
		e.gh.set(func(g *connGitHub) { g.deviceStatus = status })
		if code := e.errorCode(e.call("owner", http.MethodPost, connRoute, ""), http.StatusFailedDependency); code != want {
			t.Fatalf("device code %d: %s", status, code)
		}
	}
}

// Disconnect while an audit job is analyzing: the provider generation fence
// fails the job instead of publishing findings made with the old credential.
func TestGithubDisconnectFencesARunningAudit(t *testing.T) {
	e := newConnectionEnv(t)
	e.connect()
	ctx := context.Background()
	project, err := e.data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("GHFENCE"), Name: stringPtr("Fence")}, e.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := e.data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Fenced task")}, e.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.data.UpdateGithubSelection(ctx, project.ID, []store.GithubRepository{{Repository: "example/synthetic", Ref: "main"}}, 1); err != nil {
		t.Fatal(err)
	}
	ai := &auditFlowAI{task: task.ID, entered: make(chan struct{}), release: make(chan struct{})}
	service := NewGithubAuditService(e.data, e.s.Github.Provider(), ai, nil)
	e.s.GithubAudits = service
	req := httptest.NewRequest(http.MethodPost, "/start", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "fence-start")
	res := httptest.NewRecorder()
	e.s.githubAuditStart(res, req, auth.Identity{Actor: e.owner}, project.ID)
	var started GithubAuditStartResponse
	if res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &started) != nil {
		t.Fatalf("start: %d %s", res.Code, res.Body.String())
	}
	finished := make(chan error, 1)
	go func() { finished <- service.RunOnce(ctx) }()
	<-ai.entered
	e.document(e.call("owner", http.MethodPost, discRoute, ""))
	close(ai.release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	job, err := e.data.GetGithubAuditJob(ctx, started.Audit.ID)
	if err != nil || job.Outcome != "failed" || job.ErrorCode != "authorization_changed" {
		t.Fatalf("job after disconnect: %+v %v", job, err)
	}
}

// The manager starts and stops with the GitHub services, after the services
// that read credentials from it, and leaves no goroutine behind.
func TestGithubConnectionLifecycle(t *testing.T) {
	e := newConnectionEnv(t)
	manager := e.s.GithubConnection
	if manager == nil {
		t.Fatal("no manager in device_flow mode")
	}
	e.document(e.call("owner", http.MethodPost, connRoute, "")) // a pending attempt keeps the poller busy
	_ = e.s.StopGithubServices(context.Background())
	_ = e.s.StopGithubServices(context.Background()) // twice is fine
	if status := manager.Status(e.owner.ID); status.SigninAvailable || status.Attempt != nil {
		t.Fatalf("manager still running after stop: %+v", status)
	}
	cycle := func() {
		e.install()
		e.document(e.call("owner", http.MethodPost, connRoute, ""))
		_ = e.s.StopGithubServices(context.Background())
	}
	cycle() // warm the shared HTTP transport before measuring
	baseline := runtime.NumGoroutine()
	for range 3 {
		cycle()
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > baseline {
		t.Fatalf("goroutines grew from %d to %d across restarts", baseline, now)
	}
	// Stopping a server whose services were never started is fine too.
	idle, _ := testServer(t, "disabled")
	if err := idle.StopGithubServices(context.Background()); err != nil {
		t.Fatal(err)
	}
}
