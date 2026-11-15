package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/githubconnect"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

// One distinct sentinel per kind of secret. The user code may reach only the
// administrator who began the attempt; the others never leave the process.
const (
	connAccessSentinel  = "SENTINELHTTPACCESS"
	connRefreshSentinel = "SENTINELHTTPREFRESH"
	connDeviceSentinel  = "SENTINELHTTPDEVICE"
	connUserCode        = "HQZX-VKWJ"
	connClientID        = "Iv23liHTTPTEST"
)

var connSecrets = []string{connAccessSentinel, connRefreshSentinel, connDeviceSentinel}
var connUserCodes = []string{connUserCode, strings.ReplaceAll(connUserCode, "-", "")}

// connGitHub serves the C4 endpoints and the repository reads the provider
// makes. Nothing here contacts github.com.
type connGitHub struct {
	*httptest.Server
	mu           sync.Mutex
	approve      bool
	deviceCalls  int
	requests     int
	issued       int
	repos        []string
	installFail  int // HTTP status for installation listing; 0 means OK
	deviceStatus int
}

func newConnGitHub(t *testing.T) *connGitHub {
	g := &connGitHub{repos: []string{"Example/Synthetic", "example/docs"}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

func (g *connGitHub) set(f func(*connGitHub)) { g.mu.Lock(); f(g); g.mu.Unlock() }
func (g *connGitHub) read(f func(*connGitHub) int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return f(g)
}

func (g *connGitHub) issue(w http.ResponseWriter) {
	g.issued++
	writeConnJSON(w, map[string]any{"access_token": fmt.Sprintf("ghu_%s%03d", connAccessSentinel, g.issued), "token_type": "bearer",
		"expires_in": 28800, "refresh_token": fmt.Sprintf("ghr_%s%03d", connRefreshSentinel, g.issued), "refresh_token_expires_in": 15897600})
}

func (g *connGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("Content-Type", "application/json")
	_ = r.ParseForm()
	switch {
	case r.URL.Path == "/login/device/code":
		g.deviceCalls++
		if g.deviceStatus != 0 {
			w.WriteHeader(g.deviceStatus)
			_, _ = w.Write([]byte(`{"message":"upstream words ` + connDeviceSentinel + `"}`))
			return
		}
		writeConnJSON(w, map[string]any{"device_code": fmt.Sprintf("%s%04d", connDeviceSentinel, g.deviceCalls), "user_code": connUserCode,
			"verification_uri": g.URL + "/login/device", "expires_in": 900, "interval": 5})
	case r.URL.Path == "/login/oauth/access_token" && r.PostForm.Get("grant_type") == "refresh_token":
		g.issue(w)
	case r.URL.Path == "/login/oauth/access_token":
		if !g.approve {
			writeConnJSON(w, map[string]any{"error": "authorization_pending", "error_description": "upstream words"})
			return
		}
		g.issue(w)
	case r.URL.Path == "/user":
		writeConnJSON(w, map[string]any{"login": "octocat", "id": 583231})
	case r.URL.Path == "/user/installations":
		if g.installFail != 0 {
			w.WriteHeader(g.installFail)
			return
		}
		writeConnJSON(w, map[string]any{"total_count": 1, "installations": []map[string]any{{"id": 7}}})
	case r.URL.Path == "/user/installations/7/repositories":
		items := []map[string]any{}
		for _, name := range g.repos {
			items = append(items, map[string]any{"full_name": name})
		}
		writeConnJSON(w, map[string]any{"total_count": len(items), "repositories": items})
	case strings.Contains(r.URL.Path, "/git/trees/"):
		_, _ = w.Write([]byte(`{"tree":[{"path":"main.go","type":"blob","mode":"100644","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":16}],"truncated":false}`))
	case strings.Contains(r.URL.Path, "/contents/"):
		_, _ = w.Write([]byte(`{"type":"file","encoding":"base64","content":"c3ludGhldGljIHNvdXJjZQ==","size":16,"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`))
	case strings.Contains(r.URL.Path, "/commits/"):
		_, _ = w.Write([]byte(`{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
	case strings.HasPrefix(r.URL.Path, "/repos/"):
		_, _ = w.Write([]byte(`{"id":1,"full_name":"example/synthetic","default_branch":"main","permissions":{"pull":true}}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeConnJSON(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

type connClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *connClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *connClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// connLog captures log output so cleanup can scan it.
type connLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *connLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *connLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

type connResponse struct {
	caller string
	body   string
}

type connectionEnv struct {
	t      *testing.T
	s      *Server
	data   *store.Store
	cfg    config.Config
	gh     *connGitHub
	clock  *connClock
	logs   *connLog
	owner  store.Actor
	admin  store.Actor
	cookie map[string]string // caller name -> session cookie
	token  string            // an agent's bearer token
	mu     sync.Mutex
	seen   []connResponse
	codeOK map[string]bool // callers allowed to have seen a user code
}

func (e *connectionEnv) allowCode(caller string) { e.mu.Lock(); e.codeOK[caller] = true; e.mu.Unlock() }

// newConnectionEnv starts a server in device_flow mode (unless edit changes
// the configuration) against a fake GitHub, with an owner, a second
// administrator, a member and an agent token. Cleanup stops the GitHub
// services and scans every response, log line and idempotency row.
func newConnectionEnv(t *testing.T, edit ...func(*config.Config)) *connectionEnv {
	t.Helper()
	e := &connectionEnv{t: t, gh: newConnGitHub(t), clock: &connClock{t: time.Now().UTC().Truncate(time.Second)}, logs: &connLog{}, cookie: map[string]string{}, codeOK: map[string]bool{"owner": true}}
	prior := log.Writer()
	log.SetOutput(e.logs)
	database, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	e.data = store.New(database)
	e.cfg = config.Config{AuthMode: "local", PublicOrigin: "http://helm.test", GitHubEnabled: true, GitHubAppClientID: connClientID,
		GitHubAppSlug: "kanterlabs-helm", GitHubStateDir: t.TempDir() + "/github", GitHubRepositories: []string{}}
	for _, f := range edit {
		f(&e.cfg)
	}
	e.s = New(e.data, auth.NewManager(e.data, e.cfg), e.cfg)
	e.install()
	t.Cleanup(func() {
		_ = e.s.StopGithubServices(context.Background())
		e.scan()
		log.SetOutput(prior)
		_ = database.Close()
	})
	ctx := context.Background()
	email := "owner@example.test"
	if e.owner, err = e.data.CreateFirstAdmin(ctx, store.Actor{Kind: "human", Name: "Owner", Email: &email}, "hash"); err != nil {
		t.Fatal(err)
	}
	second := "admin@example.test"
	member, err := e.data.CreateMember(ctx, store.Actor{Name: "Second admin", Email: &second}, store.WorkspaceRoleAdmin, nil, "hash", e.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.admin = member.Actor
	plain := "member@example.test"
	regular, err := e.data.CreateMember(ctx, store.Actor{Name: "Member", Email: &plain}, store.WorkspaceRoleMember, nil, "hash", e.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"owner": e.owner.ID, "admin": e.admin.ID, "member": regular.ID} {
		e.cookie[name] = "cookie-" + name
		if err = e.data.CreateSession(ctx, id, e.cookie[name], time.Now().UTC().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	agent, err := e.data.CreateActor(ctx, store.Actor{Kind: "agent", Name: "Agent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, e.token, err = e.data.CreateToken(ctx, agent.ID, "agent token", []string{"tasks:read", "tasks:write"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	return e
}

// install points the server's GitHub services at the fake GitHub and starts
// them, as cmd/helm does after New.
func (e *connectionEnv) install() {
	e.s.installGithubServices(githubSeams{
		connect:  githubconnect.Options{HTTPClient: e.gh.Client(), WebOrigin: e.gh.URL, APIOrigin: e.gh.URL, Now: e.clock.now},
		provider: githubprovider.Options{HTTPClient: e.gh.Client(), Endpoint: e.gh.URL},
	})
	e.s.StartGithubServices(context.Background())
}

// call sends a request as caller ("owner", "admin", "member", "token" or
// "none") through the full router and records the response for the scan.
func (e *connectionEnv) call(caller, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	switch caller {
	case "token":
		req.Header.Set("Authorization", "Bearer "+e.token)
	case "none":
	default:
		req.AddCookie(&http.Cookie{Name: "roadmap_session", Value: e.cookie[caller]})
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", "http://helm.test")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res := httptest.NewRecorder()
	e.s.ServeHTTP(res, req)
	e.mu.Lock()
	e.seen = append(e.seen, connResponse{caller: caller, body: res.Body.String()})
	e.mu.Unlock()
	return res
}

// document decodes an integration document, failing on any other status.
func (e *connectionEnv) document(res *httptest.ResponseRecorder) connDocument {
	e.t.Helper()
	if res.Code != http.StatusOK {
		e.t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var doc connDocument
	if err := json.Unmarshal(res.Body.Bytes(), &doc); err != nil {
		e.t.Fatal(err)
	}
	return doc
}

type connDocument struct {
	State               string               `json:"state"`
	Configured          bool                 `json:"configured"`
	AllowedRepositories []string             `json:"allowed_repositories"`
	Connection          githubconnect.Status `json:"connection"`
}

// connect runs the whole device flow through the routes: begin, approve on
// the fake GitHub, wake the poller with a stale cancel, wait for connected.
func (e *connectionEnv) connect() connDocument {
	e.t.Helper()
	started := e.document(e.call("owner", http.MethodPost, "/api/v1/integrations/github/connect", ""))
	if started.Connection.Attempt == nil || started.Connection.Attempt.UserCode != connUserCode {
		e.t.Fatalf("no pending attempt: %+v", started.Connection)
	}
	e.gh.set(func(g *connGitHub) { g.approve = true })
	e.clock.advance(6 * time.Second)
	e.document(e.call("owner", http.MethodPost, "/api/v1/integrations/github/connect/cancel", `{"attempt_id":"stale-attempt-wakes-the-poller"}`))
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		doc := e.document(e.call("owner", http.MethodGet, "/api/v1/integrations/github", ""))
		if doc.Connection.State == githubconnect.StateConnected {
			return doc
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("sign-in never completed")
	return connDocument{}
}

// errorCode decodes error.code, failing unless status matches.
func (e *connectionEnv) errorCode(res *httptest.ResponseRecorder, status int) string {
	e.t.Helper()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if res.Code != status || json.Unmarshal(res.Body.Bytes(), &body) != nil {
		e.t.Fatalf("want %d, got %d %s", status, res.Code, res.Body.String())
	}
	return body.Error.Code
}

func (e *connectionEnv) scan() {
	t := e.t
	logs := e.logs.String()
	for _, secret := range append(append([]string{}, connSecrets...), connUserCodes...) {
		if strings.Contains(logs, secret) {
			t.Errorf("log output contains secret %q", secret)
		}
	}
	e.mu.Lock()
	for _, seen := range e.seen {
		for _, secret := range connSecrets {
			if strings.Contains(seen.body, secret) {
				t.Errorf("response to %s contains secret %q", seen.caller, secret)
			}
		}
		for _, code := range connUserCodes {
			if !e.codeOK[seen.caller] && strings.Contains(seen.body, code) {
				t.Errorf("response to %s contains the owner's user code", seen.caller)
			}
		}
	}
	e.mu.Unlock()
	rows, err := e.data.DB.QueryContext(context.Background(), `SELECT path, response_body FROM idempotency_keys`)
	if err != nil {
		t.Error(err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var path, body string
		if err := rows.Scan(&path, &body); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(path, "/integrations/github") {
			t.Errorf("idempotency row stored for %s", path)
		}
		for _, secret := range append(append([]string{}, connSecrets...), connUserCodes...) {
			if strings.Contains(body, secret) {
				t.Errorf("idempotency row for %s contains secret %q", path, secret)
			}
		}
	}
}
