package httpapi

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" || r.URL.Path == "/health" {
		s.health(w, r, false)
		return
	}
	if r.URL.Path == "/readyz" || r.URL.Path == "/ready" {
		s.health(w, r, true)
		return
	}
	if r.URL.Path == "/metrics" {
		s.metricsEndpoint(w, r)
		return
	}
	if r.URL.Path == "/openapi.json" {
		s.openAPI(w, r)
		return
	}
	if !isAPIPath(r.URL.Path) {
		s.staticFile(w, r)
		return
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
			return
		}
		response := map[string]any{"name": "helm", "version": "v1"}
		if s.Cfg.ReleaseSHA != "" {
			response["revision"] = s.Cfg.ReleaseSHA
		}
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	if isCoolifyIntakePath(parts) {
		s.coolifyIntake(w, r, parts[2])
		return
	}
	if isTicketHookPath(parts) {
		s.ticketHook(w, r, parts[2])
		return
	}
	if isEmailHookPath(parts) {
		s.emailHook(w, r, parts[3])
		return
	}
	// Auth endpoints status/setup/login are intentionally public. Logout can
	// also be called after a session has expired.
	if len(parts) >= 2 && parts[0] == "auth" {
		switch parts[1] {
		case "status":
			if r.Method == http.MethodGet {
				s.authStatus(w, r)
				return
			}
		case "setup":
			if r.Method == http.MethodPost {
				s.authSetup(w, r)
				return
			}
		case "login":
			if r.Method == http.MethodPost {
				s.authLogin(w, r)
				return
			}
		case "logout":
			if r.Method == http.MethodPost {
				s.authLogout(w, r)
				return
			}
		}
	}
	identity, ok := requestIdentity(r)
	if !ok {
		var err error
		var admitted bool
		identity, err, admitted = s.authenticateRequest(w, r)
		if !admitted {
			return
		}
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		// ServeHTTP authenticates and admits protected requests before buffering;
		// retain this fallback for direct route callers.
		if !s.admitBearerRequest(w, r, identity) {
			return
		}
	}
	if len(parts) >= 2 && parts[0] == "auth" && parts[1] == "me" && r.Method == http.MethodGet {
		s.writeJSON(w, http.StatusOK, identity.Actor)
		return
	}
	if !identity.IsToken && !s.validMutationOrigin(r) {
		s.writeError(w, http.StatusForbidden, "csrf_origin", "request origin is not allowed", nil)
		return
	}
	s.dispatchAuthed(w, r, identity, parts)
}

func isBetaSwitchRoute(r *http.Request) bool {
	if r == nil || !isAPIPath(r.URL.Path) {
		return false
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	return len(parts) >= 2 && parts[0] == "admin" && parts[1] == "beta"
}

func (s *Server) betaSwitchEnabled() bool {
	return s != nil && s.Cfg.BetaSwitchEnabled && s.Cfg.AuthMode == "tailnet" && s.Cfg.PublicOrigin == "https://beta-helm.home.shanekanterman.dev"
}

func (s *Server) validMutationOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Browser mutations always carry Origin. Requiring it prevents a
		// cross-site form or legacy client from riding an ambient session cookie.
		// App bearer tokens are checked before this guard and remain suitable for
		// non-browser automation.
		return false
	}
	return s.Cfg.PublicOrigin != "" && origin == s.Cfg.PublicOrigin
}

func splitPath(value string) []string {
	value = strings.Trim(value, "/")
	if value == "" {
		return nil
	}
	raw := strings.Split(value, "/")
	result := make([]string, 0, len(raw))
	for _, part := range raw {
		if decoded, err := strconv.Unquote(`"` + part + `"`); err == nil {
			part = decoded
		}
		result = append(result, part)
	}
	return result
}
