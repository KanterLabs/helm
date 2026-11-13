package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/forgeconnect"
	"github.com/KanterLabs/helm/internal/store"
)

func probeBody(base string, insecure bool) string {
	return fmt.Sprintf(`{"base_url":%q,"allow_insecure_http":%t}`, base, insecure)
}

func TestGiteaProbeErrors(t *testing.T) {
	h := newGiteaHarness(t)
	owner := h.as(h.owner)
	expect := func(name string, w *httptest.ResponseRecorder, status int, code string) map[string]any {
		t.Helper()
		body := decodeGitea(t, w)
		envelope, _ := body["error"].(map[string]any)
		if w.Code != status || envelope["code"] != code || envelope["message"] == "" {
			t.Errorf("%s = %d %v, want %d %s", name, w.Code, body, status, code)
		}
		return envelope
	}
	expect("odd scheme", h.call(owner, http.MethodPost, giteaPrefix+"/probe", probeBody("ftp://gitea.example", false)), 400, "invalid_server_url")
	expect("http without opt-in", h.call(owner, http.MethodPost, giteaPrefix+"/probe", probeBody(h.forge.base(), false)), 400, "insecure_http")

	h.forge.set(func(f *giteaFake) { f.issuer = "https://elsewhere.example" })
	envelope := expect("issuer elsewhere", h.call(owner, http.MethodPost, giteaPrefix+"/probe", probeBody(h.forge.base(), true)), 400, "issuer_mismatch")
	if details, _ := envelope["details"].(map[string]any); details["issuer"] != "https://elsewhere.example" {
		t.Errorf("issuer_mismatch details = %v", envelope["details"])
	}
	h.forge.set(func(f *giteaFake) { f.issuer = "" })

	notForge := httptest.NewServer(http.NotFoundHandler())
	defer notForge.Close()
	expect("not a forge", h.call(owner, http.MethodPost, giteaPrefix+"/probe", probeBody(notForge.URL, true)), 400, "not_a_forge")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + listener.Addr().String()
	listener.Close()
	expect("unreachable", h.call(owner, http.MethodPost, giteaPrefix+"/probe", probeBody(closed, true)), 502, "server_unreachable")

	start := func(clientID string) *httptest.ResponseRecorder {
		return h.call(owner, http.MethodPost, giteaPrefix+"/oauth/start", fmt.Sprintf(`{"base_url":%q,"client_id":%q,"allow_insecure_http":true}`, h.forge.base(), clientID))
	}
	expect("bad client ID", start("bad client id?"), 400, "client_unknown")
	expect("repositories while disconnected", h.call(owner, http.MethodGet, giteaPrefix+"/repositories", ""), 409, "unavailable")
	h.connect()
	expect("start while connected", start(giteaClientID), 409, "already_connected")
}

func TestGiteaErrorStatusTable(t *testing.T) {
	want := map[string]int{
		forgeconnect.CodeInvalidServerURL: 400, forgeconnect.CodeInsecureHTTP: 400, forgeconnect.CodeNotAForge: 400,
		forgeconnect.CodeIssuerMismatch: 400, forgeconnect.CodeClientUnknown: 400, forgeconnect.CodeStateInvalid: 400,
		forgeconnect.CodeClientConfidential: 400, forgeconnect.CodeAlreadyConnected: 409, forgeconnect.CodeReconnectRequired: 409,
		forgeconnect.CodeServerUnreachable: 502, forgeconnect.CodeTLSError: 502, forgeconnect.CodeExchangeFailed: 502,
		forgeconnect.CodeAuthorizeFailed: 502, forgeconnect.CodeDenied: 403, forgeconnect.CodeScopeConflict: 409,
		forgeconnect.CodeUnavailable: 503, forgeconnect.CodeStorageFailed: 500,
	}
	for code, status := range want {
		got, message := giteaErrorStatus(code)
		if got != status || message == "" || strings.Contains(message, code) {
			t.Errorf("%s = %d %q, want %d and a fixed message", code, got, message, status)
		}
	}
	if got, _ := giteaErrorStatus("mystery"); got != http.StatusInternalServerError {
		t.Errorf("unknown code = %d", got)
	}
}

func TestGiteaStrictRequests(t *testing.T) {
	h := newGiteaHarness(t)
	owner := h.as(h.owner)
	for _, body := range []string{`{}`, `[]`, `null`, `{"base_url":1}`, `{"base_url":"x","extra":true}`, `{"base_url":"x","allow_insecure_http":"yes"}`, `{"base_url":`, `{"base_url":null}`} {
		if w := h.call(owner, http.MethodPost, giteaPrefix+"/probe", body); w.Code != http.StatusBadRequest || giteaErrorCode(t, w) != "invalid_request" {
			t.Errorf("probe %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{"base_url":"x"}`, `{"client_id":"x"}`, `{"base_url":"x","client_id":"y","scope":"admin"}`, `{"base_url":"x","client_id":7}`} {
		if w := h.call(owner, http.MethodPost, giteaPrefix+"/oauth/start", body); w.Code != http.StatusBadRequest || giteaErrorCode(t, w) != "invalid_request" {
			t.Errorf("start %s = %d %s", body, w.Code, w.Body.String())
		}
	}
	for _, route := range []giteaRoute{
		{http.MethodGet, giteaPrefix + "/probe", ""}, {http.MethodPost, giteaPrefix, ""}, {http.MethodDelete, giteaPrefix, ""},
		{http.MethodGet, giteaPrefix + "/nope", ""}, {http.MethodPost, giteaPrefix + "/oauth/callback", ""}, {http.MethodGet, "/api/v1/integrations/gitlab", ""},
	} {
		if w := h.call(owner, route.method, route.path, route.body); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d", route.method, route.path, w.Code)
		}
	}
	if h.forge.callCount() != 0 {
		t.Fatal("a refused request reached the forge")
	}
}

func TestGiteaNoReplayStore(t *testing.T) {
	h := newGiteaHarness(t)
	h.connect()
	key := giteaCaller{cookie: h.owner, headers: map[string]string{"Idempotency-Key": "same-key"}}
	first := decodeGitea(t, h.call(key, http.MethodPost, giteaPrefix+"/check", ""))
	h.forge.set(func(f *giteaFake) { f.repos = 99 })
	second := decodeGitea(t, h.call(key, http.MethodPost, giteaPrefix+"/check", ""))
	if first["repository_count"] != float64(12) || second["repository_count"] != float64(99) {
		t.Fatalf("a repeated key was replayed: %v then %v", first["repository_count"], second["repository_count"])
	}
	if status := decodeGitea(t, h.call(key, http.MethodPost, giteaPrefix+"/disconnect", "")); status["state"] != "disconnected" {
		t.Fatalf("disconnect with a reused key = %v", status)
	}
}

func TestGiteaDisabledAndNoOrigin(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		change       func(*forgeconnect.Options)
	}{
		{"disabled", "disabled", func(o *forgeconnect.Options) { o.Enabled = false }},
		{"no origin", "public_origin_missing", func(o *forgeconnect.Options) { o.PublicOrigin = "" }},
	} {
		t.Run(c.name, func(t *testing.T) { giteaUnavailableCase(t, c.reason, c.change) })
	}
	t.Run("no manager", giteaNoManagerCase)
}

func giteaUnavailableCase(t *testing.T, reason string, change func(*forgeconnect.Options)) {
	{
		h := newGiteaHarness(t, change)
		owner := h.as(h.owner)
		status := decodeGitea(t, h.call(owner, http.MethodGet, giteaPrefix, ""))
		if status["available"] != false || status["unavailable_reason"] != reason || status["state"] != "disconnected" {
			t.Errorf("status = %v", status)
		}
		for _, route := range giteaRoutes(h.forge.base())[1:3] {
			if w := h.call(owner, route.method, route.path, route.body); w.Code != http.StatusServiceUnavailable || giteaErrorCode(t, w) != "unavailable" {
				t.Errorf("%s = %d %s", route.path, w.Code, w.Body.String())
			}
		}
		if w := h.call(owner, http.MethodGet, giteaPrefix+"/oauth/callback?code=c&state=s", ""); w.Header().Get("Location") != "/settings?gitea_error=unavailable" {
			t.Errorf("callback = %q", w.Header().Get("Location"))
		}
		if h.forge.callCount() != 0 {
			t.Error("the forge was contacted")
		}
	}
}

// giteaNoManagerCase: the manager could not be built at startup.
func giteaNoManagerCase(t *testing.T) {
	h := newGiteaHarness(t)
	h.server.Gitea = nil
	owner := h.as(h.owner)
	status := decodeGitea(t, h.call(owner, http.MethodGet, giteaPrefix, ""))
	if status["available"] != false || status["unavailable_reason"] != "disabled" || status["state"] != "disconnected" || status["access"] != "unknown" {
		t.Fatalf("no manager status = %v", status)
	}
	for _, route := range giteaRoutes(h.forge.base())[1:] {
		w := h.call(owner, route.method, route.path, route.body)
		if strings.Contains(route.path, "callback") {
			if w.Header().Get("Location") != "/settings?gitea_error=unavailable" {
				t.Errorf("no manager callback = %q", w.Header().Get("Location"))
			}
		} else if w.Code != http.StatusServiceUnavailable || giteaErrorCode(t, w) != "unavailable" {
			t.Errorf("no manager %s = %d", route.path, w.Code)
		}
	}
}

func giteaConfiguredServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	database, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := store.New(database)
	server := New(data, auth.NewManager(data, cfg), cfg)
	t.Cleanup(server.StopGiteaConnection)
	return server
}

func TestGiteaManagerOptionalAtStartup(t *testing.T) {
	h := newGiteaHarness(t) // captures the log
	base := config.Config{AuthMode: "local", PublicOrigin: giteaOrigin}
	plain := giteaConfiguredServer(t, base)
	ready := httptest.NewRecorder()
	plain.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	for name, cfg := range map[string]config.Config{
		"no state directory": {AuthMode: "local", PublicOrigin: giteaOrigin, GiteaOAuth: true},
		"bad public origin":  {AuthMode: "local", PublicOrigin: "http://helm.test/path?" + giteaUpstreamSentinel, GiteaOAuth: true, ForgeStateDir: t.TempDir()},
	} {
		server := giteaConfiguredServer(t, cfg)
		if server.Gitea != nil {
			t.Errorf("%s: manager built", name)
		}
		server.StartGiteaConnection(context.Background())
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if w.Code != ready.Code {
			t.Errorf("%s: /readyz = %d, want %d", name, w.Code, ready.Code)
		}
	}
	if !strings.Contains(h.logs.String(), `"msg":"gitea connection unavailable"`) || strings.Contains(h.logs.String(), giteaUpstreamSentinel) {
		t.Fatalf("startup log = %s", h.logs.String())
	}

	enabled := giteaConfiguredServer(t, config.Config{AuthMode: "local", PublicOrigin: giteaOrigin, GiteaOAuth: true, ForgeStateDir: t.TempDir()})
	if enabled.Gitea == nil || !enabled.Gitea.Status().Available {
		t.Fatal("configured manager not built or not available")
	}
	if disabled := giteaConfiguredServer(t, base); disabled.Gitea == nil || disabled.Gitea.Status().UnavailableReason != "disabled" {
		t.Fatal("HELM_GITEA_OAUTH=off must report disabled")
	}
}

func TestGiteaLifecycle(t *testing.T) {
	server := giteaConfiguredServer(t, config.Config{AuthMode: "local", PublicOrigin: giteaOrigin, GiteaOAuth: true, ForgeStateDir: t.TempDir()})
	server.StopGiteaConnection() // before start: harmless
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.StartGiteaConnection(ctx)
	server.StartGiteaConnection(ctx)
	done := make(chan struct{})
	go func() {
		// Stop must end the keep-alive by itself while the parent context
		// is still alive, and may be called twice.
		server.StopGiteaConnection()
		server.StopGiteaConnection()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StopGiteaConnection did not return")
	}
	(&Server{}).StartGiteaConnection(ctx)
	(&Server{}).StopGiteaConnection()
}
