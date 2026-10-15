package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

// serveLegacyOrigin allows the old protected tunnel to keep scoped agents
// working against the same store. Browser credentials never authenticate on
// this route; only safe browser navigation is redirected to the fixed origin.
func (s *Server) serveLegacyOrigin(w http.ResponseWriter, r *http.Request) bool {
	if s.Cfg.LegacyOrigin == "" {
		return false
	}
	legacy, err := url.Parse(s.Cfg.LegacyOrigin)
	if err != nil || !strings.EqualFold(r.Host, legacy.Host) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Helm-Legacy", "true")
	w.Header().Set("Deprecation", "true")
	s.logJSON(map[string]any{"level": "info", "msg": "legacy_hostname_request", "method": r.Method, "route": metricRoute(r.URL.Path)})
	// Strip ambient owner credentials even when a valid agent token is present.
	r.Header.Del("Cookie")
	r.Header.Del("X-Helm-Tailnet-Assertion")
	r.Header.Del("Cf-Access-Jwt-Assertion")
	r.Header.Del("Cf-Access-Authenticated-User-Email")
	path := r.URL.Path
	if path == "/healthz" || path == "/readyz" || path == "/health" || path == "/ready" {
		return false
	}
	isAPI := strings.HasPrefix(path, "/api/") || path == "/api" || path == "/openapi.json"
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.Fields(authorization)
	bearer := len(r.Header.Values("Authorization")) == 1 && len(parts) == 2 && strings.EqualFold(parts[0], "Bearer")
	isBrowserAuth := strings.HasPrefix(path, "/api/v1/auth/") && path != "/api/v1/auth/me"
	if strings.HasPrefix(path, "/api/v1/") && bearer && !isBrowserAuth {
		return false
	}
	if !isAPI && authorization == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		// Build the URL rather than resolving a user-controlled path against it;
		// double-slash paths can never choose a different host.
		target, parseErr := url.Parse(s.Cfg.PublicOrigin)
		if parseErr == nil && target.Scheme == "https" && target.Host != "" {
			target.Path = r.URL.Path
			target.RawPath = r.URL.RawPath
			target.RawQuery = r.URL.RawQuery
			http.Redirect(w, r, target.String(), http.StatusTemporaryRedirect)
			return true
		}
	}
	s.writeError(w, http.StatusConflict, "origin_moved", "Helm moved; use the configured private origin. Update the agent endpoint or reopen Helm before retrying.", map[string]any{"origin": s.Cfg.PublicOrigin})
	return true
}
