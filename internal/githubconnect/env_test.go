package githubconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Sentinels: one distinct string per kind of secret. No log line and no
// error string may ever contain any of them.
const (
	accessSentinel  = "SENTINELACCESS"
	refreshSentinel = "SENTINELREFRESH"
	deviceSentinel  = "SENTINELDEVICECODE"
	userCode        = "QZXQ-JVKW"
	testActor       = "actor-alpha"
	otherActor      = "actor-bravo"
	testClientID    = "Iv23liTESTCLIENT"
)

var sentinels = []string{accessSentinel, refreshSentinel, deviceSentinel, userCode, strings.ReplaceAll(userCode, "-", "")}

type fakeInstallation struct {
	id    int64
	repos []string
}

// fakeGitHub serves the C4 endpoints. Tokens rotate like GitHub's: a refresh
// invalidates the old pair at once.
type fakeGitHub struct {
	*httptest.Server
	mu sync.Mutex

	deviceReply   string // raw body override for /login/device/code
	deviceStatus  int    // status override
	deviceCalls   int
	deviceForm    []string // client_id of each call
	pollScript    []string // outcomes for device-code polls; empty means pending
	polls         int
	refreshScript []string // outcomes for refreshes; empty means rotate
	refreshCalls  int
	refreshGate   chan struct{} // when set, a refresh waits on it
	noExpiry      bool

	issued        int
	validAccess   map[string]bool
	validRefresh  map[string]bool
	user          string // raw /user body; empty means octocat
	installations []fakeInstallation
	apiCalls      int
	reject401     int // the next N API calls answer 401
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{validAccess: map[string]bool{}, validRefresh: map[string]bool{}}
	g.installations = []fakeInstallation{{id: 7, repos: []string{"KanterLabs/Helm", "KanterLabs/docs"}}}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/login/device/code":
		_ = r.ParseForm()
		g.deviceCalls++
		g.deviceForm = append(g.deviceForm, r.PostForm.Get("client_id"))
		if g.deviceStatus != 0 {
			w.WriteHeader(g.deviceStatus)
		}
		if g.deviceReply != "" {
			_, _ = w.Write([]byte(strings.ReplaceAll(g.deviceReply, "{web}", g.URL)))
			return
		}
		writeJSON(w, map[string]any{"device_code": fmt.Sprintf("%s%04d", deviceSentinel, g.deviceCalls), "user_code": userCode,
			"verification_uri": g.URL + "/login/device", "expires_in": 900, "interval": 5})
	case r.Method == http.MethodPost && r.URL.Path == "/login/oauth/access_token":
		_ = r.ParseForm()
		if r.PostForm.Get("grant_type") == "refresh_token" {
			g.refresh(w, r.PostForm.Get("refresh_token"))
			return
		}
		g.poll(w, r.PostForm.Get("device_code"))
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/user"):
		g.apiCalls++
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if g.reject401 > 0 || !g.validAccess[token] {
			if g.reject401 > 0 {
				g.reject401--
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		g.api(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

// issue mints a new pair; the caller holds g.mu.
func (g *fakeGitHub) issue(w http.ResponseWriter, withRefresh bool) {
	g.issued++
	access := fmt.Sprintf("ghu_%s%03d", accessSentinel, g.issued)
	g.validAccess[access] = true
	if g.noExpiry {
		writeJSON(w, map[string]any{"access_token": access, "token_type": "bearer", "scope": ""})
		return
	}
	reply := map[string]any{"access_token": access, "token_type": "bearer", "expires_in": 28800, "scope": ""}
	if withRefresh {
		refresh := fmt.Sprintf("ghr_%s%03d", refreshSentinel, g.issued)
		g.validRefresh[refresh] = true
		reply["refresh_token"], reply["refresh_token_expires_in"] = refresh, 15897600
	}
	writeJSON(w, reply)
}

func (g *fakeGitHub) poll(w http.ResponseWriter, deviceCode string) {
	g.polls++
	if !strings.HasPrefix(deviceCode, deviceSentinel) {
		writeJSON(w, map[string]any{"error": "incorrect_device_code"})
		return
	}
	outcome := "authorization_pending"
	if len(g.pollScript) > 0 {
		outcome, g.pollScript = g.pollScript[0], g.pollScript[1:]
	}
	switch outcome {
	case "success":
		g.issue(w, true)
	case "500":
		w.WriteHeader(http.StatusInternalServerError)
	case "garbage":
		_, _ = w.Write([]byte(`{"access_token":`))
	case "slow_down":
		writeJSON(w, map[string]any{"error": "slow_down", "interval": 10})
	default:
		writeJSON(w, map[string]any{"error": outcome, "error_description": "upstream text " + deviceSentinel})
	}
}

func (g *fakeGitHub) refresh(w http.ResponseWriter, token string) {
	g.refreshCalls++
	if gate := g.refreshGate; gate != nil {
		g.mu.Unlock()
		<-gate
		g.mu.Lock()
	}
	outcome := ""
	if len(g.refreshScript) > 0 {
		outcome, g.refreshScript = g.refreshScript[0], g.refreshScript[1:]
	}
	if !g.validRefresh[token] {
		writeJSON(w, map[string]any{"error": "bad_refresh_token", "error_description": "bad " + token})
		return
	}
	switch outcome {
	case "500":
		w.WriteHeader(http.StatusInternalServerError)
		return
	case "bad_refresh_token":
		writeJSON(w, map[string]any{"error": "bad_refresh_token"})
		return
	case "refused":
		writeJSON(w, map[string]any{"error": "incorrect_client_credentials", "error_description": "upstream " + token})
		return
	}
	delete(g.validRefresh, token)
	for access := range g.validAccess {
		delete(g.validAccess, access)
	}
	g.issue(w, outcome != "no_refresh")
	if outcome == "no_refresh" { // the old refresh token stays usable
		g.validRefresh[token] = true
	}
}

func (g *fakeGitHub) api(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/user":
		if g.user != "" {
			_, _ = w.Write([]byte(g.user))
			return
		}
		writeJSON(w, map[string]any{"login": "octocat", "id": 583231})
	case r.URL.Path == "/user/installations":
		items := []map[string]any{}
		for _, inst := range g.installations {
			items = append(items, map[string]any{"id": inst.id})
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage > 0 && len(items) > perPage {
			items = items[:perPage]
		}
		writeJSON(w, map[string]any{"total_count": len(g.installations), "installations": items})
	case strings.HasPrefix(r.URL.Path, "/user/installations/") && strings.HasSuffix(r.URL.Path, "/repositories"):
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/user/installations/"), "/repositories"), 10, 64)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		for _, inst := range g.installations {
			if inst.id != id {
				continue
			}
			start, end := (page-1)*perPage, page*perPage
			start, end = min(max(start, 0), len(inst.repos)), min(max(end, 0), len(inst.repos))
			items := []map[string]any{}
			for _, name := range inst.repos[start:end] {
				items = append(items, map[string]any{"full_name": name, "private": true})
			}
			writeJSON(w, map[string]any{"total_count": len(inst.repos), "repositories": items})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (g *fakeGitHub) set(f func(g *fakeGitHub)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f(g)
}

func (g *fakeGitHub) count(f func(g *fakeGitHub) int) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return f(g)
}

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// timers replaces the poll loop's sleep: every wait is recorded and fires
// only when the test says so.
type timers struct {
	mu      sync.Mutex
	waits   []time.Duration
	pending chan time.Time
}

func (s *timers) after(d time.Duration) <-chan time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waits = append(s.waits, d)
	s.pending = make(chan time.Time, 1)
	return s.pending
}

func (s *timers) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.waits)
}

func (s *timers) last() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.waits) == 0 {
		return 0
	}
	return s.waits[len(s.waits)-1]
}

func (s *timers) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		s.pending <- time.Time{}
		s.pending = nil
	}
}

type env struct {
	t      *testing.T
	gh     *fakeGitHub
	dir    string
	clock  *clock
	timers *timers
	m      *Manager
	opts   Options

	mu   sync.Mutex
	logs []string
	errs []error
}

// newEnv builds a manager against a fake GitHub and, at cleanup, closes it
// and scans every log line and returned error for every sentinel.
func newEnv(t *testing.T, edit ...func(*Options)) *env {
	t.Helper()
	e := &env{t: t, gh: newFakeGitHub(t), dir: t.TempDir() + "/github", clock: &clock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}, timers: &timers{}}
	e.opts = Options{ClientID: testClientID, AppSlug: "kanterlabs-helm", StateDir: e.dir, HTTPClient: e.gh.Client(),
		WebOrigin: e.gh.URL, APIOrigin: e.gh.URL, Now: e.clock.now}
	for _, f := range edit {
		f(&e.opts)
	}
	e.m = e.build()
	t.Cleanup(func() {
		e.m.Close()
		e.scan()
	})
	return e
}

func (e *env) build() *Manager {
	e.t.Helper()
	m, err := newManager(e.opts, e.logf)
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	m.after = e.timers.after
	return m
}

// restart simulates a process restart over the same state directory.
func (e *env) restart() {
	e.m.Close()
	e.m = e.build()
}

func (e *env) logf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logs = append(e.logs, fmt.Sprintf(format, args...))
}

func (e *env) logLines() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.logs...)
}

// err records an error for the sentinel scan and returns it.
func (e *env) err(err error) error {
	if err != nil {
		e.mu.Lock()
		e.errs = append(e.errs, err)
		e.mu.Unlock()
	}
	return err
}

func (e *env) scan() {
	e.mu.Lock()
	defer e.mu.Unlock()
	texts := append([]string{}, e.logs...)
	for _, err := range e.errs {
		texts = append(texts, err.Error(), fmt.Sprintf("%+v", err))
	}
	for _, text := range texts {
		for _, s := range sentinels {
			if strings.Contains(text, s) {
				e.t.Errorf("secret %q leaked into %q", s, text)
			}
		}
	}
}

func (e *env) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	e.t.Cleanup(cancel)
	return ctx
}

func (e *env) begin(actor string) Status {
	e.t.Helper()
	status, err := e.m.Begin(e.ctx(), actor)
	if e.err(err) != nil {
		e.t.Fatalf("Begin: %v", err)
	}
	return status
}

func (e *env) start() {
	e.m.Start(context.Background())
}

// step fires the poll timer and waits until that poll has been handled.
func (e *env) step() {
	e.t.Helper()
	waitFor(e.t, "a poll timer", func() bool { return e.timers.count() > 0 && e.hasPending() })
	polls, waits := e.gh.count(func(g *fakeGitHub) int { return g.polls }), e.timers.count()
	e.timers.fire()
	waitFor(e.t, "the poll to finish", func() bool {
		if e.gh.count(func(g *fakeGitHub) int { return g.polls }) <= polls {
			return false
		}
		return e.timers.count() > waits || !e.attemptPending()
	})
}

func (e *env) hasPending() bool {
	e.timers.mu.Lock()
	defer e.timers.mu.Unlock()
	return e.timers.pending != nil
}

func (e *env) attemptPending() bool {
	s := e.m.Status(testActor)
	return s.Attempt != nil && s.Attempt.State == AttemptPending
}

// connect runs a full sign-in for testActor.
func (e *env) connect() {
	e.t.Helper()
	e.start()
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.begin(testActor)
	e.step()
	waitFor(e.t, "connected", func() bool { return e.m.Status(testActor).State == StateConnected })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := Code(err); got != code {
		t.Fatalf("error code = %q (%v), want %q", got, err, code)
	}
}

// quiet asserts nothing more reaches GitHub's token endpoint for a while.
func (e *env) quiet() {
	e.t.Helper()
	polls := e.gh.count(func(g *fakeGitHub) int { return g.polls })
	e.timers.fire()
	time.Sleep(50 * time.Millisecond)
	if got := e.gh.count(func(g *fakeGitHub) int { return g.polls }); got != polls {
		e.t.Fatalf("polling continued: %d polls, want %d", got, polls)
	}
}
