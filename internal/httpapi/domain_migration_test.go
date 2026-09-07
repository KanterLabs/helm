package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/store"
)

func migrationTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	server, data := testServer(t, "cloudflare")
	cfg := config.Config{
		AuthMode:      "cloudflare",
		PublicOrigin:  "https://helm.test",
		LegacyOrigin:  "https://tc.test",
		AdminEmail:    "owner@example.com",
		SecureCookies: true,
	}
	server.Cfg = cfg
	server.Auth = auth.NewManagerWithVerifier(data, cfg, staticCloudflareVerifier{
		claims: auth.CloudflareClaims{Email: "owner@example.com", Name: "Owner"},
	})
	return server, data
}

func migrationRequest(t *testing.T, handler http.Handler, method, target, host, remoteAddr string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Host = host
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, req)
	return result
}

func migrationErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, response.Body.String())
	}
	return body.Error.Code
}

func TestDomainMigrationDisabledPreservesUnknownHost(t *testing.T) {
	server, _ := testServer(t, "disabled")
	response := migrationRequest(t, server, http.MethodGet, "/", "unconfigured.test", "192.0.2.10:1234", nil, nil)
	if response.Code != http.StatusOK || response.Header().Get("Location") != "" {
		t.Fatalf("disabled migration response = %d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	status := migrationRequest(t, server, http.MethodGet, "/api/v1/auth/status", "unconfigured.test", "192.0.2.10:1234", nil, nil)
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "canonical_origin") || strings.Contains(status.Body.String(), "legacy_origin") {
		t.Fatalf("disabled auth status = %d body=%s", status.Code, status.Body.String())
	}
}

func TestDomainMigrationHostAllowlistAndLoopbackHealth(t *testing.T) {
	server, _ := migrationTestServer(t)
	for _, tt := range []struct {
		name  string
		host  string
		path  string
		extra map[string]string
	}{
		{name: "unknown host", host: "evil.test", path: "/"},
		{name: "forwarded host spoof", host: "evil.test", path: "/", extra: map[string]string{"X-Forwarded-Host": "helm.test"}},
		{name: "cross port", host: "helm.test:443", path: "/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := migrationRequest(t, server, http.MethodGet, tt.path, tt.host, "192.0.2.10:1234", nil, tt.extra)
			if response.Code != http.StatusMisdirectedRequest || migrationErrorCode(t, response) != "host_not_allowed" {
				t.Fatalf("response = %d body=%s", response.Code, response.Body.String())
			}
		})
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := migrationRequest(t, server, method, "/readyz", "installer.invalid", "127.0.0.1:32451", nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("loopback %s ready status = %d body=%s", method, response.Code, response.Body.String())
		}
	}
	nonLoopback := migrationRequest(t, server, http.MethodGet, "/readyz", "installer.invalid", "192.0.2.10:32451", nil, nil)
	if nonLoopback.Code != http.StatusMisdirectedRequest {
		t.Fatalf("non-loopback ready status = %d body=%s", nonLoopback.Code, nonLoopback.Body.String())
	}
	canonical := migrationRequest(t, server, http.MethodGet, "/healthz", "helm.test", "192.0.2.10:32451", nil, nil)
	if canonical.Code != http.StatusOK {
		t.Fatalf("canonical health status = %d body=%s", canonical.Code, canonical.Body.String())
	}
}

func TestStagedHostAudienceAllowlistRejectsUnknownHosts(t *testing.T) {
	server, data := migrationTestServer(t)
	server.Cfg.PublicOrigin = "https://tc.test"
	server.Cfg.LegacyOrigin = ""
	server.Cfg.CloudflareHostAudiences = map[string][]string{
		"tc.test":   {"old-ui"},
		"helm.test": {"new-ui"},
	}
	server.Auth = auth.NewManagerWithVerifier(data, server.Cfg, staticCloudflareVerifier{
		claims: auth.CloudflareClaims{Email: "owner@example.com", Name: "Owner"},
	})
	unknownBearer := migrationRequest(t, server, http.MethodGet, "/api/v1/projects", "evil.test", "192.0.2.10:1234", nil, map[string]string{"Authorization": "Bearer not-reached", "X-Forwarded-Host": "tc.test"})
	if unknownBearer.Code != http.StatusMisdirectedRequest || migrationErrorCode(t, unknownBearer) != "host_not_allowed" {
		t.Fatalf("staged unknown bearer host = %d body=%s", unknownBearer.Code, unknownBearer.Body.String())
	}
	futureHost := migrationRequest(t, server, http.MethodGet, "/", "helm.test", "192.0.2.10:1234", nil, nil)
	if futureHost.Code != http.StatusOK || futureHost.Header().Get("Location") != "" {
		t.Fatalf("staged future host = %d location=%q body=%s", futureHost.Code, futureHost.Header().Get("Location"), futureHost.Body.String())
	}
	installerHealth := migrationRequest(t, server, http.MethodGet, "/healthz", "installer.invalid", "127.0.0.1:32451", nil, nil)
	if installerHealth.Code != http.StatusOK {
		t.Fatalf("staged loopback health = %d body=%s", installerHealth.Code, installerHealth.Body.String())
	}
}

func TestLegacyDocumentRedirectSafety(t *testing.T) {
	server, _ := migrationTestServer(t)
	rootNavigation := migrationRequest(t, server, http.MethodGet, "/?session=secret", "tc.test", "192.0.2.10:1234", nil, map[string]string{"Accept": "text/html,application/xhtml+xml"})
	if rootNavigation.Code != http.StatusTemporaryRedirect || rootNavigation.Header().Get("Location") != "https://helm.test/" {
		t.Fatalf("root navigation = %d location=%q body=%s", rootNavigation.Code, rootNavigation.Header().Get("Location"), rootNavigation.Body.String())
	}
	redirect := migrationRequest(t, server, http.MethodGet, "/p/example?access_token=secret&next=%2Fadmin", "tc.test", "192.0.2.10:1234", nil, map[string]string{"Accept": "text/html,application/xhtml+xml"})
	if redirect.Code != http.StatusTemporaryRedirect || redirect.Header().Get("Location") != "https://helm.test/p/example" {
		t.Fatalf("redirect = %d location=%q body=%s", redirect.Code, redirect.Header().Get("Location"), redirect.Body.String())
	}
	if redirect.Header().Get("Cache-Control") != "no-store" || redirect.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("redirect cache/referrer headers = cache %q referrer %q", redirect.Header().Get("Cache-Control"), redirect.Header().Get("Referrer-Policy"))
	}
	for _, target := range []string{"/", "/p/%2e%2e/admin", "/p/%252e%252e/admin", "/p//admin", "/p/example"} {
		headers := map[string]string{"Accept": "text/html"}
		if target == "/p/example" {
			headers["Authorization"] = "Bearer old-agent-token"
		}
		response := migrationRequest(t, server, http.MethodGet, target, "tc.test", "192.0.2.10:1234", nil, headers)
		if target == "/" {
			// A bare request without navigation evidence is kept available to
			// service-worker/programmatic fetches and is not redirected.
			delete(headers, "Accept")
			response = migrationRequest(t, server, http.MethodGet, target, "tc.test", "192.0.2.10:1234", nil, headers)
			if response.Code != http.StatusOK || response.Header().Get("Location") != "" {
				t.Fatalf("bare service-worker root = %d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
			}
		}
		if target == "/p/example" && response.Code == http.StatusTemporaryRedirect {
			t.Fatalf("authorized old-host UI request redirected: location=%q", response.Header().Get("Location"))
		}
		if target != "/p/example" && target != "/" && response.Code == http.StatusTemporaryRedirect {
			t.Fatalf("path trick %q redirected: location=%q", target, response.Header().Get("Location"))
		}
	}

	for _, target := range []string{
		"/index.html",
		"/sw.js",
		"/assets/index-CuZVuKUf.js",
		"/icons/icon-192.png",
		"/manifest.webmanifest",
		"/favicon.svg",
		"/helm-mark.svg",
	} {
		response := migrationRequest(t, server, http.MethodGet, target, "tc.test", "192.0.2.10:1234", nil, map[string]string{"Accept": "text/html"})
		if response.Code != http.StatusOK || response.Header().Get("Location") != "" {
			t.Fatalf("compatibility path %q redirected: status=%d location=%q", target, response.Code, response.Header().Get("Location"))
		}
		if target == "/sw.js" && !strings.Contains(response.Body.String(), "PRECACHE_URLS") {
			t.Fatalf("legacy service worker response did not contain the embedded worker")
		}
	}
	missingAsset := migrationRequest(t, server, http.MethodGet, "/assets/not-found.js", "tc.test", "192.0.2.10:1234", nil, map[string]string{"Accept": "text/html"})
	if missingAsset.Code != http.StatusNotFound || missingAsset.Header().Get("Location") != "" {
		t.Fatalf("missing legacy asset = %d location=%q body=%s", missingAsset.Code, missingAsset.Header().Get("Location"), missingAsset.Body.String())
	}
}

func TestDomainMigrationMovedHumanWritesAndCanonicalWrites(t *testing.T) {
	server, data := migrationTestServer(t)
	old := migrationRequest(t, server, http.MethodPost, "/api/v1/projects", "tc.test", "192.0.2.10:1234", map[string]any{"key": "OLD", "name": "Old write"}, map[string]string{
		"Content-Type":            "application/json",
		"Origin":                  "https://helm.test",
		"Cf-Access-Jwt-Assertion": "valid-access-assertion",
	})
	if old.Code != http.StatusConflict || migrationErrorCode(t, old) != "instance_moved" || !strings.Contains(old.Body.String(), `"canonical_origin":"https://helm.test"`) {
		t.Fatalf("old human write = %d body=%s", old.Code, old.Body.String())
	}
	canonical := migrationRequest(t, server, http.MethodPost, "/api/v1/projects", "helm.test", "192.0.2.10:1234", map[string]any{"key": "NEW", "name": "Canonical write"}, map[string]string{
		"Content-Type":            "application/json",
		"Origin":                  "https://helm.test",
		"Cf-Access-Jwt-Assertion": "valid-access-assertion",
	})
	if canonical.Code != http.StatusCreated {
		t.Fatalf("canonical write = %d body=%s", canonical.Code, canonical.Body.String())
	}
	projects, err := data.ListProjects(context.Background(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Key != "NEW" {
		t.Fatalf("projects after migration writes = %#v", projects)
	}

	for _, target := range []string{"/api/v1/auth/login", "/api/v1/auth/login/extra", "/api/v1/auth/setup/extra"} {
		oldAuth := migrationRequest(t, server, http.MethodPost, target, "tc.test", "192.0.2.10:1234", map[string]any{"email": "owner@example.com", "password": "not-used"}, map[string]string{"Content-Type": "application/json", "Origin": "https://helm.test"})
		if oldAuth.Code != http.StatusConflict || migrationErrorCode(t, oldAuth) != "instance_moved" {
			t.Fatalf("old auth %s = %d body=%s", target, oldAuth.Code, oldAuth.Body.String())
		}
	}
	for _, origin := range []string{"", "https://helm.test"} {
		logout := migrationRequest(t, server, http.MethodPost, "/api/v1/auth/logout", "tc.test", "192.0.2.10:1234", nil, map[string]string{"Origin": origin})
		if logout.Code != http.StatusForbidden {
			t.Fatalf("legacy logout origin %q = %d body=%s", origin, logout.Code, logout.Body.String())
		}
	}
	logout := migrationRequest(t, server, http.MethodPost, "/api/v1/auth/logout/extra", "tc.test", "192.0.2.10:1234", nil, map[string]string{"Origin": "https://tc.test"})
	if logout.Code != http.StatusOK {
		t.Fatalf("legacy logout = %d body=%s", logout.Code, logout.Body.String())
	}
}

func TestLegacyBearerAPIWriteRemainsAvailable(t *testing.T) {
	server, data := migrationTestServer(t)
	actor, err := data.CreateActor(context.Background(), store.Actor{Kind: "agent", Name: "legacy agent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, rawToken, err := data.CreateToken(context.Background(), actor.ID, "legacy API", []string{"projects:write"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := migrationRequest(t, server, http.MethodPost, "/api/v1/projects", "tc.test", "192.0.2.10:1234", map[string]any{"key": "AGENT", "name": "Agent write"}, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + rawToken,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("legacy bearer write = %d body=%s", response.Code, response.Body.String())
	}
}

func TestMigrationStatusMetadataOnlyWhenEnabled(t *testing.T) {
	server, _ := migrationTestServer(t)
	response := migrationRequest(t, server, http.MethodGet, "/api/v1/auth/status", "tc.test", "192.0.2.10:1234", nil, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"canonical_origin":"https://helm.test"`) || !strings.Contains(response.Body.String(), `"legacy_origin":"https://tc.test"`) {
		t.Fatalf("migration status = %d body=%s", response.Code, response.Body.String())
	}
	browser := migrationRequest(t, server, http.MethodGet, "/api/v1/auth/status", "tc.test", "192.0.2.10:1234", nil, map[string]string{
		"Accept":                  "text/html,application/xhtml+xml",
		"Cf-Access-Jwt-Assertion": "valid-access-assertion",
	})
	if browser.Code != http.StatusOK || strings.Contains(browser.Header().Get("Location"), "/") || !strings.Contains(browser.Body.String(), `"legacy_origin":"https://tc.test"`) {
		t.Fatalf("legacy browser auth status = %d location=%q body=%s", browser.Code, browser.Header().Get("Location"), browser.Body.String())
	}
}
