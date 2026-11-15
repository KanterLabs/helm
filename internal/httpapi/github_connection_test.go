package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/githubconnect"
)

const (
	connRoute    = "/api/v1/integrations/github/connect"
	cancelRoute  = "/api/v1/integrations/github/connect/cancel"
	discRoute    = "/api/v1/integrations/github/disconnect"
	docRoute     = "/api/v1/integrations/github"
	checkRoute   = "/api/v1/integrations/github/check"
	cancelSample = `{"attempt_id":"some-attempt"}`
)

func TestGithubConnectionRoutesRejectTokensAndMembers(t *testing.T) {
	e := newConnectionEnv(t)
	routes := []struct{ method, path, body string }{
		{http.MethodPost, connRoute, ""}, {http.MethodPost, cancelRoute, cancelSample}, {http.MethodPost, discRoute, ""},
		{http.MethodGet, docRoute, ""}, {http.MethodPost, checkRoute, ""},
	}
	for _, route := range routes {
		for _, caller := range []string{"token", "member"} {
			if res := e.call(caller, route.method, route.path, route.body); res.Code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: %d %s", route.method, route.path, caller, res.Code, res.Body.String())
			}
		}
		if res := e.call("none", route.method, route.path, route.body); res.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: %d", route.method, route.path, res.Code)
		}
		if route.method == http.MethodPost {
			if res := e.call("owner", route.method, route.path, route.body, "Origin", "https://evil.example"); res.Code != http.StatusForbidden {
				t.Fatalf("%s cross-origin: %d", route.path, res.Code)
			}
		}
	}
	if calls := e.gh.read(func(g *connGitHub) int { return g.requests }); calls != 0 {
		t.Fatalf("a refused caller reached GitHub %d times", calls)
	}
}

func TestGithubConnectionMethodsAndCancelBody(t *testing.T) {
	e := newConnectionEnv(t)
	for _, c := range []struct{ method, path string }{{http.MethodGet, connRoute}, {http.MethodGet, cancelRoute}, {http.MethodDelete, discRoute}, {http.MethodPut, connRoute}} {
		if res := e.call("owner", c.method, c.path, ""); res.Code != http.StatusMethodNotAllowed || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d", c.method, c.path, res.Code)
		}
	}
	bad := []string{``, `{}`, `[]`, `"x"`, `{"attempt_id":null}`, `{"attempt_id":""}`, `{"attempt_id":"   "}`, `{"attempt_id":5}`,
		`{"attempt_id":["a"]}`, `{"attempt_id":"a","extra":true}`, `{"attempt_id":"a"}{"attempt_id":"b"}`, `{"attempt_id":"a"`,
		`{"attempt_id":"` + strings.Repeat("a", 201) + `"}`}
	for _, body := range bad {
		if code := e.errorCode(e.call("owner", http.MethodPost, cancelRoute, body), http.StatusBadRequest); code != "invalid_request" {
			t.Fatalf("body %q: code %s", body, code)
		}
	}
	e.document(e.call("owner", http.MethodPost, cancelRoute, `{"attempt_id":"`+strings.Repeat("a", 200)+`"}`))
}

func TestGithubConnectUserCodeOnlyForTheBeginner(t *testing.T) {
	e := newConnectionEnv(t)
	began := e.document(e.call("owner", http.MethodPost, connRoute, ""))
	attempt := began.Connection.Attempt
	if attempt == nil || attempt.UserCode != connUserCode || attempt.State != githubconnect.AttemptPending {
		t.Fatalf("beginner: %+v", began.Connection)
	}
	// The same administrator pressing again gets the same attempt, not a new code.
	again := e.document(e.call("owner", http.MethodPost, connRoute, ""))
	if again.Connection.Attempt == nil || again.Connection.Attempt.ID != attempt.ID || e.gh.read(func(g *connGitHub) int { return g.deviceCalls }) != 1 {
		t.Fatalf("double press started another attempt: %+v", again.Connection.Attempt)
	}
	for _, res := range []string{docRoute} {
		other := e.document(e.call("admin", http.MethodGet, res, ""))
		if other.Connection.Attempt == nil || other.Connection.Attempt.ID != attempt.ID || other.Connection.Attempt.UserCode != "" {
			t.Fatalf("other admin view: %+v", other.Connection.Attempt)
		}
	}
	// A stale cancel from the other admin leaves the attempt; the scan at
	// cleanup proves no response to them carried the code.
	stale := e.document(e.call("admin", http.MethodPost, cancelRoute, `{"attempt_id":"not-the-attempt"}`))
	if stale.Connection.Attempt == nil || stale.Connection.Attempt.ID != attempt.ID {
		t.Fatalf("stale cancel ended the attempt: %+v", stale.Connection)
	}
	mine := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if mine.Connection.Attempt == nil || mine.Connection.Attempt.UserCode != connUserCode {
		t.Fatalf("beginner lost the code: %+v", mine.Connection.Attempt)
	}
	// Another administrator replaces the attempt; the owner no longer sees a code.
	replaced := e.document(e.call("admin", http.MethodPost, connRoute, ""))
	if replaced.Connection.Attempt == nil || replaced.Connection.Attempt.ID == attempt.ID {
		t.Fatalf("second admin did not replace: %+v", replaced.Connection.Attempt)
	}
	ownerView := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if ownerView.Connection.Attempt == nil || ownerView.Connection.Attempt.UserCode != "" {
		t.Fatalf("owner sees the replacement's code: %+v", ownerView.Connection.Attempt)
	}
	cancelled := e.document(e.call("admin", http.MethodPost, cancelRoute, `{"attempt_id":"`+replaced.Connection.Attempt.ID+`"}`))
	if cancelled.Connection.Attempt != nil || cancelled.Connection.State != githubconnect.StateDisconnected {
		t.Fatalf("cancel kept the attempt: %+v", cancelled.Connection)
	}
	// The second admin rightly saw its own code; every earlier response to
	// them was checked above to carry none of the owner's.
	e.allowCode("admin")
}

func TestGithubConnectionOperatorTokenMode(t *testing.T) {
	e := newConnectionEnv(t, func(c *config.Config) { c.GitHubTokenFile = "/nonexistent/helm-test-token" })
	if e.s.GithubConnection != nil {
		t.Fatal("a connection manager was built in operator_token mode")
	}
	for _, route := range []struct{ path, body string }{{connRoute, ""}, {cancelRoute, cancelSample}, {discRoute, ""}} {
		res := e.call("owner", http.MethodPost, route.path, route.body)
		if code := e.errorCode(res, http.StatusConflict); code != "github_operator_managed" || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %s", route.path, code)
		}
	}
	doc := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	want := githubconnect.Status{Mode: githubconnect.ModeOperatorToken, State: githubconnect.StateDisconnected}
	if doc.Connection != want {
		t.Fatalf("operator connection: %+v", doc.Connection)
	}
	e.document(e.call("owner", http.MethodPost, checkRoute, "")) // no Sync: it still answers 200
	if calls := e.gh.read(func(g *connGitHub) int { return g.requests }); calls != 0 {
		t.Fatalf("operator_token mode called the sign-in endpoints %d times", calls)
	}
}

func TestGithubConnectionWithoutClientID(t *testing.T) {
	e := newConnectionEnv(t, func(c *config.Config) { c.GitHubAppClientID = "" })
	if code := e.errorCode(e.call("owner", http.MethodPost, connRoute, ""), http.StatusConflict); code != "github_signin_unavailable" {
		t.Fatalf("code %s", code)
	}
	doc := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if doc.Connection.Mode != githubconnect.ModeDeviceFlow || doc.Connection.SigninAvailable {
		t.Fatalf("connection: %+v", doc.Connection)
	}
}

func TestGithubConnectionDisabledBuildsNothing(t *testing.T) {
	e := newConnectionEnv(t, func(c *config.Config) { c.GitHubEnabled = false })
	if e.s.GithubConnection != nil {
		t.Fatal("a manager was built with GitHub disabled")
	}
	if code := e.errorCode(e.call("owner", http.MethodPost, connRoute, ""), http.StatusConflict); code != "github_signin_unavailable" {
		t.Fatalf("code %s", code)
	}
	doc := e.document(e.call("owner", http.MethodGet, docRoute, ""))
	if doc.Connection.Mode != githubconnect.ModeDeviceFlow || doc.Connection.SigninAvailable || doc.Connection.State != githubconnect.StateDisconnected {
		t.Fatalf("connection: %+v", doc.Connection)
	}
}

// The full table. No code may map to 502 or 504 (amendment A2), and the
// codes connection.ts names keep their exact spelling.
func TestGithubConnectionErrorTable(t *testing.T) {
	want := map[string]struct {
		status int
		code   string
	}{
		githubconnect.CodeSigninUnavailable: {409, "github_signin_unavailable"},
		githubconnect.CodeOperatorManaged:   {409, "github_operator_managed"},
		githubconnect.CodeInvalidRequest:    {400, "invalid_request"},
		githubconnect.CodeAlreadyConnected:  {409, "github_already_connected"},
		githubconnect.CodeNotConnected:      {409, "github_not_connected"},
		githubconnect.CodeReconnectRequired: {409, "github_reconnect_required"},
		githubconnect.CodeGitHubUnreachable: {424, "github_unreachable"},
		githubconnect.CodeGitHubRefused:     {424, "github_refused"},
		githubconnect.CodeStorageFailed:     {500, "github_storage_failed"},
		githubconnect.CodeTooManyAttempts:   {429, "rate_limited"},
	}
	if len(want) != len(githubconnect.Codes) {
		t.Fatalf("table covers %d codes, manager has %d", len(want), len(githubconnect.Codes))
	}
	messages := map[string]bool{}
	for _, code := range githubconnect.Codes {
		status, wire, message := githubConnectionError(&githubconnect.Error{Code: code})
		if status == http.StatusBadGateway || status == http.StatusGatewayTimeout || status != want[code].status || wire != want[code].code {
			t.Fatalf("%s -> %d %s", code, status, wire)
		}
		if message == "" || strings.Contains(message, code) || messages[message] {
			t.Fatalf("%s message %q is not a distinct sentence of ours", code, message)
		}
		messages[message] = true
	}
	status, wire, message := githubConnectionError(errors.New("upstream said " + connAccessSentinel))
	if status != 500 || wire != "internal_error" || strings.Contains(message, connAccessSentinel) {
		t.Fatalf("foreign error -> %d %s %q", status, wire, message)
	}
	// connection.ts names these two codes; the rest it reads by status.
	for _, named := range []string{"github_operator_managed", "github_signin_unavailable"} {
		found := false
		for _, entry := range want {
			found = found || entry.code == named
		}
		if !found {
			t.Fatalf("panel code %s missing", named)
		}
	}
}

// The connection object has exactly the keys of the TypeScript type, with
// attempt absent (not null) when there is none.
func TestGithubConnectionDocumentShape(t *testing.T) {
	e := newConnectionEnv(t)
	allowed := []string{"mode", "signin_available", "state", "login", "connected_at", "install_url", "authorizations_url", "repositories_truncated", "attempt"}
	attemptKeys := []string{"id", "state", "user_code", "verification_uri", "expires_at", "message"}
	shape := func(caller, method, path string) (map[string]json.RawMessage, map[string]json.RawMessage) {
		t.Helper()
		res := e.call(caller, method, path, "")
		var top map[string]json.RawMessage
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &top) != nil {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body.String())
		}
		for _, key := range []string{"enabled", "configured", "state", "message", "allowed_repositories", "checked_at", "connection"} {
			if _, ok := top[key]; !ok {
				t.Fatalf("document lost %s: %s", key, res.Body.String())
			}
		}
		var conn map[string]json.RawMessage
		_ = json.Unmarshal(top["connection"], &conn)
		for key := range conn {
			if !slices.Contains(allowed, key) {
				t.Fatalf("unknown connection key %s", key)
			}
		}
		var attempt map[string]json.RawMessage
		if raw, ok := conn["attempt"]; ok {
			if string(raw) == "null" || json.Unmarshal(raw, &attempt) != nil {
				t.Fatalf("attempt present but not an object: %s", raw)
			}
			for key := range attempt {
				if !slices.Contains(attemptKeys, key) {
					t.Fatalf("unknown attempt key %s", key)
				}
			}
		}
		return conn, attempt
	}
	conn, attempt := shape("owner", http.MethodGet, docRoute)
	if attempt != nil || len(conn) != 6 { // mode, signin_available, state, install_url, authorizations_url, repositories_truncated
		t.Fatalf("disconnected shape: %v", conn)
	}
	_, attempt = shape("owner", http.MethodPost, connRoute)
	if len(attempt) != 6 {
		t.Fatalf("beginner attempt shape: %v", attempt)
	}
	if _, attempt = shape("admin", http.MethodGet, docRoute); len(attempt) != 5 || attempt["user_code"] != nil {
		t.Fatalf("other admin attempt shape: %v", attempt)
	}
	e.connect()
	conn, attempt = shape("owner", http.MethodGet, docRoute)
	if attempt != nil || len(conn) != 8 || conn["login"] == nil || conn["connected_at"] == nil {
		t.Fatalf("connected shape: %v", conn)
	}
}

func TestGithubConnectionNoStoreAndNoReplay(t *testing.T) {
	e := newConnectionEnv(t)
	key := []string{"Idempotency-Key", "same-key-every-time"}
	first := e.call("owner", http.MethodPost, connRoute, "", key...)
	attempt := e.document(first).Connection.Attempt
	e.document(e.call("owner", http.MethodPost, cancelRoute, `{"attempt_id":"`+attempt.ID+`"}`, key...))
	second := e.document(e.call("owner", http.MethodPost, connRoute, "", key...))
	if second.Connection.Attempt == nil || second.Connection.Attempt.ID == attempt.ID || e.gh.read(func(g *connGitHub) int { return g.deviceCalls }) != 2 {
		t.Fatalf("a repeated Idempotency-Key replayed instead of reaching the manager: %+v", second.Connection.Attempt)
	}
	e.document(e.call("owner", http.MethodPost, discRoute, "", key...))
	e.document(e.call("owner", http.MethodPost, discRoute, "", key...))
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, docRoute, ""}, {http.MethodPost, connRoute, ""}, {http.MethodPost, cancelRoute, "{}"},
		{http.MethodPost, discRoute, ""}, {http.MethodPost, checkRoute, ""},
	} {
		if res := e.call("owner", c.method, c.path, c.body); res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s (%d) is cacheable", c.method, c.path, res.Code)
		}
	}
	// The cleanup scan fails on any idempotency row for these routes.
}

func TestGithubConnectionMetricRoutes(t *testing.T) {
	for _, path := range []string{docRoute, checkRoute, connRoute, cancelRoute, discRoute} {
		if got := metricRoute(path); got != path {
			t.Fatalf("%s labelled %s", path, got)
		}
	}
	if got := metricRoute("/api/v1/integrations/github/connect/other"); got != "/api/v1/other" {
		t.Fatalf("unknown path labelled %s", got)
	}
}
