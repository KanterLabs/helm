package forgeconnect

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUnavailable(t *testing.T) {
	disabled := newEnv(t, func(o *Options) { o.Enabled = false })
	status := disabled.m.Status()
	if status.Available || status.UnavailableReason != "disabled" {
		t.Fatalf("disabled status = %+v", status)
	}
	_, err := disabled.m.Begin(disabled.ctx(), testActor, BeginInput{BaseURL: disabled.forge.base(), ClientID: testClientID, AllowInsecureHTTP: true})
	wantCode(t, disabled.err(err), CodeUnavailable)
	wantCode(t, disabled.err(disabled.m.Finish(disabled.ctx(), testActor, "s", "c", "", "")), CodeUnavailable)
	_, _, err = disabled.m.Fresh(disabled.ctx())
	wantCode(t, disabled.err(err), CodeUnavailable)

	noOrigin := newEnv(t, func(o *Options) { o.PublicOrigin = "" })
	status = noOrigin.m.Status()
	if status.Available || status.UnavailableReason != "public_origin_missing" || status.RedirectURI != "" {
		t.Fatalf("no-origin status = %+v", status)
	}
	_, err = noOrigin.m.Begin(noOrigin.ctx(), testActor, BeginInput{BaseURL: noOrigin.forge.base(), ClientID: testClientID, AllowInsecureHTTP: true})
	wantCode(t, noOrigin.err(err), CodeUnavailable)
	if noOrigin.forge.totalCalls()+disabled.forge.totalCalls() != 0 {
		t.Fatal("unavailable sign-in contacted the forge")
	}
	for _, origin := range []string{"helm.example", "https://helm.example/path", "https://u@helm.example", "ftp://helm.example"} {
		if _, err := New(Options{Enabled: true, PublicOrigin: origin, StateDir: t.TempDir()}); err == nil {
			t.Errorf("public origin %q accepted", origin)
		}
	}
	if _, err := New(Options{Enabled: true, PublicOrigin: testOrigin}); err == nil {
		t.Error("empty state directory accepted")
	}
}

func TestBeginValidatesClientID(t *testing.T) {
	e := newEnv(t)
	for _, id := range []string{"", "has space", strings.Repeat("x", 129), "a/b"} {
		_, err := e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base(), ClientID: id, AllowInsecureHTTP: true})
		wantCode(t, e.err(err), CodeClientUnknown)
	}
	if e.forge.totalCalls() != 0 {
		t.Fatal("a malformed client ID reached the network")
	}
	_, err := e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base(), ClientID: "  " + testClientID + "\n", AllowInsecureHTTP: true})
	if e.err(err) != nil {
		t.Fatalf("surrounding whitespace must be trimmed: %v", err)
	}
	_, err = e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base(), ClientID: testClientID})
	wantCode(t, e.err(err), CodeInsecureHTTP)
}

func TestBeginAuthorizeURL(t *testing.T) {
	e := newEnv(t)
	authorize := e.begin(testActor)
	if !strings.HasPrefix(authorize, e.forge.base()+"/login/oauth/authorize?") {
		t.Fatalf("authorize URL = %q", authorize)
	}
	u, _ := url.Parse(authorize)
	q := u.Query()
	if strings.Contains(q.Get("scope"), "openid") || q.Get("scope") != Scope {
		t.Fatalf("scope = %q", q.Get("scope"))
	}
	state, err := base64.RawURLEncoding.DecodeString(q.Get("state"))
	if err != nil || len(state) != 32 || strings.Contains(q.Get("state"), "=") {
		t.Fatalf("state is not 32 random bytes in base64url: %q", q.Get("state"))
	}
	challenge, err := base64.RawURLEncoding.DecodeString(q.Get("code_challenge"))
	if err != nil || len(challenge) != sha256.Size {
		t.Fatal("challenge is not a SHA-256 in base64url")
	}
	code, state2 := e.forge.approve(authorize)
	if err := e.err(e.m.Finish(e.ctx(), testActor, state2, code, "", "")); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	e.forge.mu.Lock()
	verifier, redirect := e.forge.verifiers[0], e.forge.lastRedirect
	e.forge.mu.Unlock()
	if raw, err := base64.RawURLEncoding.DecodeString(verifier); err != nil || len(raw) != 48 {
		t.Fatalf("verifier is not 48 random bytes in base64url")
	}
	if redirect != q.Get("redirect_uri") || redirect != e.m.Status().RedirectURI {
		t.Fatal("redirect_uri differs between authorize and exchange")
	}
	status := e.m.Status()
	if status.Account == nil || status.Account.Login != "helm-bot" || status.Access != AccessScoped || status.RepositoryCount != 12 || status.Server.Version != "28.3.0" {
		t.Fatalf("status after sign-in = %+v", status)
	}
}

func TestFinishStateRules(t *testing.T) {
	e := newEnv(t)
	// Reuse.
	code, state := e.forge.approve(e.begin(testActor))
	if err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeStateInvalid)
	e.m.Disconnect(testActor)

	// Expiry.
	_, state = e.forge.approve(e.begin(testActor))
	e.clock.Advance(10*time.Minute + time.Second)
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, "x", "", "")), CodeStateInvalid)

	// Another actor's state, then the owner still succeeds.
	code, state = e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), "admin-2", state, code, "", "")), CodeStateInvalid)
	if err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")); err != nil {
		t.Fatalf("owner's sign-in broken by another actor: %v", err)
	}
	e.m.Disconnect(testActor)

	// A second Begin replaces the first.
	_, first := e.forge.approve(e.begin(testActor))
	code, second := e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, first, code, "", "")), CodeStateInvalid)
	// The mismatch consumed the pending sign-in.
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, second, code, "", "")), CodeStateInvalid)

	// Empty state, empty code.
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "", "x", "", "")), CodeStateInvalid)
	_, state = e.forge.approve(e.begin(testActor))
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, "", "", "")), CodeAuthorizeFailed)
}

func TestBeginWhileConnected(t *testing.T) {
	e := newEnv(t)
	e.connect()
	_, err := e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base(), ClientID: testClientID, AllowInsecureHTTP: true})
	wantCode(t, e.err(err), CodeAlreadyConnected)
}

func TestReconnectReusesServer(t *testing.T) {
	e := newEnv(t)
	e.connect()
	before := e.m.Status()
	_, identity, _ := e.m.Current()
	e.forge.set(func(f *fakeForge) { f.revoked = true })
	e.clock.Advance(25 * time.Hour)
	_, _, err := e.m.Fresh(e.ctx())
	wantCode(t, e.err(err), CodeReconnectRequired)
	e.forge.set(func(f *fakeForge) { f.revoked = false })
	authorize, err := e.m.Begin(e.ctx(), testActor, BeginInput{})
	if e.err(err) != nil || !strings.HasPrefix(authorize, before.Server.BaseURL+"/") {
		t.Fatalf("reconnect Begin = %q %v", authorize, err)
	}
	code, state := e.forge.approve(authorize)
	if err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")); err != nil {
		t.Fatal(err)
	}
	if _, next, _ := e.m.Current(); next == identity || next == "" {
		t.Fatal("identity must change on a new sign-in")
	}
}

func TestFinishUpstreamErrors(t *testing.T) {
	cases := []struct{ upstream, description, want string }{
		{"access_denied", "The resource owner or authorization server denied the request " + upstreamSentinel, CodeDenied},
		{"server_error", "a grant exists with different scope " + upstreamSentinel, CodeScopeConflict},
		{"server_error", "something else " + upstreamSentinel, CodeAuthorizeFailed},
		{"invalid_request", "PKCE is required " + upstreamSentinel, CodeAuthorizeFailed},
		{"unauthorized_client", upstreamSentinel, CodeAuthorizeFailed},
	}
	e := newEnv(t)
	for _, c := range cases {
		_, state := e.forge.approve(e.begin(testActor))
		wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, "", c.upstream, c.description)), c.want)
		// The pending sign-in is consumed.
		wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, "", c.upstream, c.description)), CodeStateInvalid)
	}
	// An upstream error with a forged state is a state problem.
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, "forged", "", "access_denied", "")), CodeStateInvalid)
}

func TestExchangeErrors(t *testing.T) {
	cases := map[string]struct {
		change func(*fakeForge)
		code   string
		want   string
	}{
		"confidential":      {func(f *fakeForge) { f.confidential = true }, "", CodeClientConfidential},
		"deleted":           {func(f *fakeForge) { f.appDeleted = true }, "", CodeClientUnknown},
		"used code":         {func(*fakeForge) {}, codeSentinel + "-unknown", CodeExchangeFailed},
		"server error":      {func(f *fakeForge) { f.tokenStatus = http.StatusInternalServerError }, "", CodeExchangeFailed},
		"not json":          {func(f *fakeForge) { f.tokenOverride = "<html>" + upstreamSentinel }, "", CodeExchangeFailed},
		"identity refused":  {func(f *fakeForge) { f.userStatus = http.StatusInternalServerError }, "", CodeExchangeFailed},
		"identity 401":      {func(f *fakeForge) { f.revoked = true }, "", CodeExchangeFailed},
		"limit check fails": {func(f *fakeForge) { f.orgsStatus = http.StatusInternalServerError }, "", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			code, state := e.forge.approve(e.begin(testActor))
			e.forge.set(c.change)
			if c.code != "" {
				code = c.code
			}
			err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", ""))
			wantCode(t, err, c.want)
			if c.want != "" && e.m.Status().State != StateDisconnected {
				t.Fatal("a failed exchange stored something")
			}
			if c.want == "" && e.m.Status().Access != AccessUnknown {
				t.Fatalf("limit check 500 must be unknown, got %q", e.m.Status().Access)
			}
		})
	}
	t.Run("wrong verifier", func(t *testing.T) {
		e := newEnv(t)
		authorize := e.begin(testActor)
		u, _ := url.Parse(authorize)
		q := u.Query()
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(make([]byte, 32)))
		u.RawQuery = q.Encode()
		code, state := e.forge.approve(u.String())
		wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeExchangeFailed)
	})
}

func TestExchangeRedirectNotFollowed(t *testing.T) {
	e := newEnv(t)
	code, state := e.forge.approve(e.begin(testActor))
	hits := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer elsewhere.Close()
	e.forge.set(func(f *fakeForge) { f.redirectTo = elsewhere.URL })
	wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeExchangeFailed)
	if hits != 0 {
		t.Fatal("token request followed a redirect")
	}
}

func TestTokenResponseValidation(t *testing.T) {
	bodies := map[string]string{
		"no access token":   `{"token_type":"bearer","expires_in":3600,"refresh_token":"REFRESH-SENTINEL-x"}`,
		"empty access":      `{"access_token":"","token_type":"bearer","expires_in":3600}`,
		"not bearer":        `{"access_token":"ACCESS-SENTINEL-x","token_type":"mac","expires_in":3600}`,
		"no type":           `{"access_token":"ACCESS-SENTINEL-x","expires_in":3600}`,
		"no lifetime":       `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer"}`,
		"zero lifetime":     `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer","expires_in":0}`,
		"negative lifetime": `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer","expires_in":-5}`,
		"absurd lifetime":   `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer","expires_in":2592001}`,
		"fractional":        `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer","expires_in":1.5}`,
		"wrong type":        `{"access_token":12,"token_type":"bearer","expires_in":3600}`,
		"whitespace":        `{"access_token":"ACCESS-SENTINEL x","token_type":"bearer","expires_in":3600}`,
		"oversized":         `{"access_token":"ACCESS-SENTINEL-` + strings.Repeat("x", 8200) + `","token_type":"bearer","expires_in":3600}`,
		"bad refresh":       `{"access_token":"ACCESS-SENTINEL-x","token_type":"bearer","expires_in":3600,"refresh_token":"REFRESH-SENTINEL x"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			code, state := e.forge.approve(e.begin(testActor))
			e.forge.set(func(f *fakeForge) { f.tokenOverride = body })
			wantCode(t, e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")), CodeExchangeFailed)
			if e.forge.count("/api/v1/user") != 0 {
				t.Fatal("an invalid token was used")
			}
		})
	}
	t.Run("upper-case Bearer and 30 days are fine", func(t *testing.T) {
		e := newEnv(t)
		e.forge.set(func(f *fakeForge) { f.tokenType = "Bearer"; f.expiresIn = 2592000 })
		e.connect()
	})
}

func TestNoRefreshToken(t *testing.T) {
	e := newEnv(t)
	e.forge.set(func(f *fakeForge) { f.refreshEmpty = true })
	e.connect()
	if token, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil || !strings.HasPrefix(token, accessSentinel) {
		t.Fatalf("Fresh before expiry = %v", err)
	}
	e.clock.Advance(time.Hour)
	if e.m.Status().State != StateReconnectRequired {
		t.Fatalf("state after expiry = %q", e.m.Status().State)
	}
	_, _, err := e.m.Fresh(e.ctx())
	wantCode(t, e.err(err), CodeReconnectRequired)
	if e.forge.refreshes() != 0 {
		t.Fatal("refreshed without a refresh token")
	}
}

func TestSignInSubPath(t *testing.T) {
	e := newEnv(t)
	e.forge = newFakeForge(t, "/git")
	e.connect()
	if e.m.BaseURL() != e.forge.base() || e.forge.count("/api/v1/user") != 1 {
		t.Fatalf("sub-path connection = %q", e.m.BaseURL())
	}
	list, _, err := e.m.Repositories(e.ctx())
	if e.err(err) != nil || len(list) != 12 {
		t.Fatalf("repositories under a sub-path = %d %v", len(list), err)
	}
}
