package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/forgeconnect"
)

var giteaStatusKeys = []string{"access", "account", "available", "check", "checked_at", "client_id", "connected_at", "redirect_uri", "register_url", "repository_count", "revoke_url", "server", "state", "unavailable_reason"}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestGiteaJourney(t *testing.T) {
	h := newGiteaHarness(t)
	owner := h.as(h.owner)

	status := decodeGitea(t, h.call(owner, http.MethodGet, giteaPrefix, ""))
	if status["state"] != "disconnected" || status["available"] != true || status["redirect_uri"] != giteaOrigin+giteaPrefix+"/oauth/callback" {
		t.Fatalf("initial status = %v", status)
	}

	// The probe settles a deep page to the forge's base and saves nothing.
	w := h.call(owner, http.MethodPost, giteaPrefix+"/probe", fmt.Sprintf(`{"base_url":%q,"allow_insecure_http":true}`, h.forge.base()+"/team/repo/"))
	probe := decodeGitea(t, w)
	server, _ := probe["server"].(map[string]any)
	if w.Code != http.StatusOK || server["base_url"] != h.forge.base() || server["product"] != "gitea" || server["version"] != "28.3.0" || server["insecure_http"] != true ||
		probe["redirect_uri"] != giteaOrigin+giteaPrefix+"/oauth/callback" || probe["register_url"] != h.forge.base()+"/user/settings/applications" || len(probe) != 3 {
		t.Fatalf("probe = %d %v", w.Code, probe)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "gitea.json")); !os.IsNotExist(err) {
		t.Fatal("probe saved something")
	}

	h.connect()
	status = decodeGitea(t, h.call(owner, http.MethodGet, giteaPrefix, ""))
	account, _ := status["account"].(map[string]any)
	if status["state"] != "connected" || status["access"] != "scoped" || status["repository_count"] != float64(12) || account["login"] != "helm-bot" || status["client_id"] != giteaClientID {
		t.Fatalf("connected status = %v", status)
	}

	h.forge.set(func(f *giteaFake) { f.repos = 60 })
	status = decodeGitea(t, h.call(owner, http.MethodPost, giteaPrefix+"/check", ""))
	if status["repository_count"] != float64(60) || status["check"].(map[string]any)["state"] != "ok" {
		t.Fatalf("check = %v", status)
	}
	// A restart shows the last check, not zero.
	h.open()
	if status := decodeGitea(t, h.call(owner, http.MethodGet, giteaPrefix, "")); status["repository_count"] != float64(60) || status["state"] != "connected" {
		t.Fatalf("after restart = %v", status)
	}

	w = h.call(owner, http.MethodGet, giteaPrefix+"/repositories", "")
	list := decodeGitea(t, w)
	repos, _ := list["repositories"].([]any)
	if w.Code != http.StatusOK || len(repos) != 60 || list["truncated"] != false || len(list) != 2 {
		t.Fatalf("repositories = %d %v", w.Code, list)
	}
	if first := repos[0].(map[string]any); len(first) != 3 || first["full_name"] != "team/repo-00" || first["private"] != true {
		t.Fatalf("repository item = %v", first)
	}

	status = decodeGitea(t, h.call(owner, http.MethodPost, giteaPrefix+"/disconnect", ""))
	if status["state"] != "disconnected" || status["server"] != nil {
		t.Fatalf("disconnect = %v", status)
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, "gitea.json")); !os.IsNotExist(err) {
		t.Fatal("disconnect left the state file")
	}

	// Logs and metrics carry the route template, never the code or state.
	if !strings.Contains(h.logs.String(), `"route":"/api/v1/integrations/gitea/oauth/callback"`) {
		t.Fatalf("callback request not logged by template: %s", h.logs.String())
	}
	metrics := h.call(owner, http.MethodGet, "/metrics", "")
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), `route="/api/v1/integrations/gitea/oauth/callback"`) {
		t.Fatalf("metrics = %d", metrics.Code)
	}
}

func TestGiteaStatusDocumentShape(t *testing.T) {
	h := newGiteaHarness(t)
	status := decodeGitea(t, h.call(h.as(h.owner), http.MethodGet, giteaPrefix, ""))
	if got := sortedKeys(status); !slices.Equal(got, giteaStatusKeys) {
		t.Fatalf("keys = %v", got)
	}
	for _, key := range []string{"server", "account", "connected_at", "checked_at"} {
		if value, ok := status[key]; !ok || value != nil {
			t.Errorf("disconnected %s = %v, want null", key, value)
		}
	}
	if status["access"] != "unknown" || status["register_url"] != "" || status["revoke_url"] != "" || status["client_id"] != "" {
		t.Fatalf("disconnected defaults = %v", status)
	}
	if check := status["check"].(map[string]any); check["state"] != "ok" || check["code"] != "" || len(check) != 2 {
		t.Fatalf("disconnected check = %v", check)
	}
	h.connect()
	status = decodeGitea(t, h.call(h.as(h.owner), http.MethodGet, giteaPrefix, ""))
	if got := sortedKeys(status); !slices.Equal(got, giteaStatusKeys) {
		t.Fatalf("connected keys = %v", got)
	}
	server := status["server"].(map[string]any)
	if got := sortedKeys(server); !slices.Equal(got, []string{"base_url", "insecure_http", "product", "version"}) {
		t.Fatalf("server keys = %v", got)
	}
	if got := sortedKeys(status["account"].(map[string]any)); !slices.Equal(got, []string{"id", "login"}) {
		t.Fatalf("account keys = %v", got)
	}
	if status["connected_at"] == nil || status["checked_at"] == nil || status["revoke_url"] != h.forge.base()+"/user/settings/applications" {
		t.Fatalf("connected = %v", status)
	}
}

type giteaRoute struct{ method, path, body string }

func giteaRoutes(base string) []giteaRoute {
	probe := fmt.Sprintf(`{"base_url":%q,"allow_insecure_http":true}`, base)
	return []giteaRoute{
		{http.MethodGet, giteaPrefix, ""},
		{http.MethodPost, giteaPrefix + "/probe", probe},
		{http.MethodPost, giteaPrefix + "/oauth/start", fmt.Sprintf(`{"base_url":%q,"client_id":%q,"allow_insecure_http":true}`, base, giteaClientID)},
		{http.MethodGet, giteaPrefix + "/oauth/callback?code=" + giteaCodeSentinel + "&state=s", ""},
		{http.MethodPost, giteaPrefix + "/check", ""},
		{http.MethodGet, giteaPrefix + "/repositories", ""},
		{http.MethodPost, giteaPrefix + "/disconnect", ""},
	}
}

func TestGiteaRoutesRefuseNonAdministrators(t *testing.T) {
	h := newGiteaHarness(t)
	h.connect()
	before := h.forge.callCount()
	for _, route := range giteaRoutes(h.forge.base()) {
		callback := strings.Contains(route.path, "/oauth/callback")
		for name, caller := range map[string]giteaCaller{
			"owner bearer": {bearer: h.ownerToken}, "agent bearer": {bearer: h.agent}, "member": h.as(h.member), "anonymous": {},
		} {
			w := h.call(caller, route.method, route.path, route.body)
			switch {
			case callback && (name == "member" || name == "anonymous"):
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?gitea_error=state_invalid" {
					t.Errorf("%s %s as %s = %d %q", route.method, route.path, name, w.Code, w.Header().Get("Location"))
				}
			case name == "anonymous":
				if w.Code != http.StatusUnauthorized {
					t.Errorf("%s %s as %s = %d", route.method, route.path, name, w.Code)
				}
			default:
				if w.Code != http.StatusForbidden {
					t.Errorf("%s %s as %s = %d %s", route.method, route.path, name, w.Code, w.Body.String())
				}
			}
		}
		if route.method == http.MethodPost {
			if w := h.call(giteaCaller{cookie: h.owner, noOrigin: true}, route.method, route.path, route.body); w.Code != http.StatusForbidden || giteaErrorCode(t, w) != "csrf_origin" {
				t.Errorf("%s without Origin = %d", route.path, w.Code)
			}
		}
	}
	if h.forge.callCount() != before {
		t.Fatal("a refused request reached the forge")
	}
	if status := h.manager.Status(); status.State != forgeconnect.StateConnected {
		t.Fatalf("a refused request changed the connection: %s", status.State)
	}
}

func callbackURL(values url.Values) string { return giteaPrefix + "/oauth/callback?" + values.Encode() }

func TestGiteaCallbackWithoutSession(t *testing.T) {
	h := newGiteaHarness(t)
	code, state := h.forge.approve(h.start(h.owner))
	w := h.call(giteaCaller{}, http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?gitea_error=state_invalid" || strings.Contains(w.Body.String(), "{") {
		t.Fatalf("callback without session = %d %q %q", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	// Nothing was consumed: the administrator can still finish.
	if w := h.call(h.as(h.owner), http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), ""); w.Header().Get("Location") != "/settings?gitea=connected" {
		t.Fatalf("owner after anonymous callback = %q", w.Header().Get("Location"))
	}
}

func TestGiteaCallbackOtherAdministrator(t *testing.T) {
	h := newGiteaHarness(t)
	code, state := h.forge.approve(h.start(h.owner))
	w := h.call(h.as(h.admin), http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), "")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/settings?gitea_error=state_invalid" {
		t.Fatalf("other administrator = %d %q", w.Code, w.Header().Get("Location"))
	}
	if h.manager.Status().State != forgeconnect.StateDisconnected {
		t.Fatal("another administrator completed the sign-in")
	}
	if w := h.call(h.as(h.owner), http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), ""); w.Header().Get("Location") != "/settings?gitea=connected" {
		t.Fatalf("starter = %q", w.Header().Get("Location"))
	}
}

func TestGiteaCallbackOutcomes(t *testing.T) {
	h := newGiteaHarness(t)
	owner := h.as(h.owner)
	expect := func(name string, w *httptest.ResponseRecorder, code string) {
		t.Helper()
		want := "/settings?gitea_error=" + code
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != want || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s = %d %q (Referrer-Policy %q), want %q", name, w.Code, w.Header().Get("Location"), w.Header().Get("Referrer-Policy"), want)
		}
	}
	upstream := giteaUpstreamSentinel + " <script>"
	for _, c := range []struct{ name, error, description, code string }{
		{"denied", "access_denied", upstream, "denied"},
		{"scope", "server_error", "a grant exists with different scope " + upstream, "scope_conflict"},
		{"other", "temporarily_unavailable" + giteaUpstreamSentinel, upstream, "authorize_failed"},
	} {
		_, state := h.forge.approve(h.start(h.owner))
		expect(c.name, h.call(owner, http.MethodGet, callbackURL(url.Values{"error": {c.error}, "error_description": {c.description}, "state": {state}}), ""), c.code)
	}

	h.forge.set(func(f *giteaFake) { f.tokenError = [2]string{"invalid_request", upstream} })
	code, state := h.forge.approve(h.start(h.owner))
	expect("exchange", h.call(owner, http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), ""), "exchange_failed")
	h.forge.set(func(f *giteaFake) { f.tokenError = [2]string{"unauthorized_client", "invalid empty client secret"} })
	code, state = h.forge.approve(h.start(h.owner))
	expect("confidential", h.call(owner, http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), ""), "client_confidential")
	expect("replayed state", h.call(owner, http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), ""), "state_invalid")
	expect("no state", h.call(owner, http.MethodGet, callbackURL(url.Values{"code": {code}}), ""), "state_invalid")

	h.forge.set(func(f *giteaFake) { f.tokenError = [2]string{} })
	code, state = h.forge.approve(h.start(h.owner))
	w := h.call(owner, http.MethodGet, callbackURL(url.Values{"code": {code}, "state": {state}}), "")
	if w.Header().Get("Location") != "/settings?gitea=connected" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("success = %q", w.Header().Get("Location"))
	}
}

func TestGiteaMetricRoutes(t *testing.T) {
	for _, path := range []string{"", "/probe", "/oauth/start", "/oauth/callback", "/check", "/repositories", "/disconnect"} {
		if got := metricRoute(giteaPrefix + path + "?code=" + giteaCodeSentinel + "&state=abc"); got != giteaPrefix+path {
			t.Errorf("metricRoute(%s) = %q", path, got)
		}
	}
	if got := metricRoute(giteaPrefix + "/" + giteaCodeSentinel); got != "/api/v1/other" {
		t.Errorf("unknown segment = %q", got)
	}
}
