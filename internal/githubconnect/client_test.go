package githubconnect

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A1: beta reaches GitHub only through the operator's proxy. The production
// client takes its proxy policy from proxyFromEnvironment, which is
// http.ProxyFromEnvironment outside tests.
func TestRequestsGoThroughTheProxy(t *testing.T) {
	if reflect.ValueOf(proxyFromEnvironment).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Fatal("the production proxy policy is not http.ProxyFromEnvironment")
	}
	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.String())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"device_code":"%s1","user_code":"%s","verification_uri":"http://github.test/login/device","expires_in":900,"interval":5}`, deviceSentinel, userCode)
	}))
	defer proxy.Close()
	target, _ := url.Parse(proxy.URL)
	real := proxyFromEnvironment
	proxyFromEnvironment = http.ProxyURL(target)
	defer func() { proxyFromEnvironment = real }()

	var logs []string
	m, err := newManager(Options{ClientID: testClientID, StateDir: t.TempDir() + "/github", WebOrigin: "http://github.test", APIOrigin: "http://api.github.test"},
		func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Begin(t.Context(), testActor); err != nil {
		t.Fatalf("Begin through the proxy: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []string{"POST http://github.test/login/device/code"}) {
		t.Fatalf("proxy saw %v", seen)
	}
	if len(logs) != 0 {
		t.Fatalf("logs %v", logs)
	}
}

func TestProductionClientDefaults(t *testing.T) {
	c := newHTTPClient(proxyFromEnvironment)
	transport, ok := c.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil || c.Timeout != requestTimeout || c.CheckRedirect == nil {
		t.Fatalf("client %+v", c)
	}
	if err := c.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirects followed: %v", err)
	}
}

func TestInjectedClientIsConfined(t *testing.T) {
	for _, in := range []*http.Client{{}, {Timeout: time.Hour}, {Timeout: time.Second}} {
		c := confine(in)
		if c.Timeout <= 0 || c.Timeout > requestTimeout || c.CheckRedirect == nil || c == in {
			t.Fatalf("confined client %+v", c)
		}
	}
}

func TestRedirectIsRefused(t *testing.T) {
	var hits int
	var mu sync.Mutex
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/login/device/code", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	e := newEnv(t, func(o *Options) { o.WebOrigin, o.HTTPClient = redirector.URL, &http.Client{} })
	_, err := e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeGitHubUnreachable)
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatal("redirect followed")
	}
}

func TestOversizedReplyIsRefused(t *testing.T) {
	e := newEnv(t)
	body := `{"device_code":"` + deviceSentinel + `1","user_code":"` + userCode + `","verification_uri":"{web}/login/device","expires_in":900,"pad":"` + strings.Repeat("x", 70<<10) + `"}`
	e.gh.set(func(g *fakeGitHub) { g.deviceReply = body })
	_, err := e.m.Begin(e.ctx(), testActor)
	wantCode(t, e.err(err), CodeGitHubUnreachable)
}

func TestOriginsAreValidated(t *testing.T) {
	for _, origin := range []string{"ftp://github.test", "https://github.test/path", "https://user@github.test", "not a url"} {
		if _, err := newManager(Options{StateDir: t.TempDir(), WebOrigin: origin}, func(string, ...any) {}); err == nil {
			t.Fatalf("accepted web origin %q", origin)
		}
	}
	if _, err := newManager(Options{ClientID: "bad id"}, func(string, ...any) {}); err == nil {
		t.Fatal("accepted a client ID with a space")
	}
	if _, err := newManager(Options{ClientID: testClientID}, func(string, ...any) {}); err == nil {
		t.Fatal("accepted a missing state directory")
	}
	m, err := newManager(Options{StateDir: t.TempDir()}, func(string, ...any) {})
	if err != nil || m.web != "https://github.com" || m.api != "https://api.github.com" {
		t.Fatalf("defaults %v %q %q", err, m.web, m.api)
	}
}

func TestErrorCodesAreStable(t *testing.T) {
	want := []string{"already_connected", "github_refused", "github_unreachable", "invalid_request", "not_connected",
		"operator_managed", "reconnect_required", "signin_unavailable", "storage_failed"}
	got := slices.Clone(Codes)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("codes %v", got)
	}
	err := fmt.Errorf("wrapped: %w", fail(CodeStorageFailed))
	if Code(err) != CodeStorageFailed || Code(nil) != "" || Code(errors.New("x")) != "" {
		t.Fatal("Code does not unwrap")
	}
	if fail(CodeGitHubRefused).Error() != "github connection: github_refused" {
		t.Fatal("error text changed")
	}
}
