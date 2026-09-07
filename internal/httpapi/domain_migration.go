package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
)

// migrationHost identifies which configured origin received a request. Host
// matching deliberately uses Request.Host only; forwarded-host headers are
// transport metadata and are not an authority assertion from the client.
type migrationHost uint8

const (
	migrationHostUnknown migrationHost = iota
	migrationHostCanonical
	migrationHostLegacy
	migrationHostBound
)

func (s *Server) migrationEnabled() bool {
	return strings.TrimSpace(s.Cfg.LegacyOrigin) != ""
}

func (s *Server) hostAllowlistEnabled() bool {
	return s.migrationEnabled() || len(s.Cfg.CloudflareHostAudiences) > 0
}

func originAuthority(origin string) string {
	parsed, err := url.ParseRequestURI(origin)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (s *Server) migrationRequestHost(r *http.Request) migrationHost {
	if !s.hostAllowlistEnabled() {
		return migrationHostCanonical
	}
	if r.Host == originAuthority(s.Cfg.PublicOrigin) {
		return migrationHostCanonical
	}
	if s.migrationEnabled() && r.Host == originAuthority(s.Cfg.LegacyOrigin) {
		return migrationHostLegacy
	}
	if len(s.Cfg.CloudflareHostAudiences) > 0 {
		if _, ok := s.Cfg.CloudflareHostAudiences[r.Host]; ok {
			return migrationHostBound
		}
	}
	return migrationHostUnknown
}

func isLoopbackRemoteAddr(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	} else {
		host = strings.TrimPrefix(host, "[")
		host = strings.TrimSuffix(host, "]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isHealthPath(value string) bool {
	switch value {
	case "/healthz", "/health", "/readyz", "/ready":
		return true
	default:
		return false
	}
}

// allowMigrationHost enforces the exact dual-host allowlist. The loopback
// health/ready exception is intentionally narrow for local installers and is
// checked before routing; no other unknown-host request can reach the app.
func (s *Server) allowMigrationHost(w http.ResponseWriter, r *http.Request) bool {
	if !s.hostAllowlistEnabled() || s.migrationRequestHost(r) != migrationHostUnknown {
		return true
	}
	if isLoopbackRemoteAddr(r.RemoteAddr) && (r.Method == http.MethodGet || r.Method == http.MethodHead) && isHealthPath(r.URL.Path) {
		return true
	}
	s.writeError(w, http.StatusMisdirectedRequest, "host_not_allowed", "request host is not configured", nil)
	return false
}

func isLegacyCompatibilityPath(value string) bool {
	switch value {
	case "/index.html", "/sw.js", "/manifest.webmanifest", "/favicon.svg", "/favicon.ico", "/helm-mark.svg", "/assets", "/icons":
		return true
	default:
		return strings.HasPrefix(value, "/assets/") || strings.HasPrefix(value, "/icons/") || strings.HasPrefix(value, "/cdn-cgi/")
	}
}

func safeLegacyDocumentPath(value, escaped string) bool {
	if value != "/" && !strings.HasPrefix(value, "/p/") {
		return false
	}
	if value == "/p/" || path.Clean(value) != value || strings.Contains(value, "//") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	escaped = strings.ToLower(escaped)
	for _, encoded := range []string{"%2e", "%2f", "%5c", "%25"} {
		if strings.Contains(escaped, encoded) {
			return false
		}
	}
	return true
}

func isDocumentNavigation(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return false
	}
	evidence := false
	if accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept"))); accept != "" {
		if !strings.Contains(accept, "text/html") {
			return false
		}
		evidence = true
	}
	if mode := strings.TrimSpace(r.Header.Get("Sec-Fetch-Mode")); mode != "" {
		if !strings.EqualFold(mode, "navigate") {
			return false
		}
		evidence = true
	}
	if destination := strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")); destination != "" {
		if !strings.EqualFold(destination, "document") {
			return false
		}
		evidence = true
	}
	return evidence
}

// redirectLegacyDocument moves only real browser document navigations. The
// destination is constructed from the configured canonical origin, never from
// a Host or query supplied by the request. Query parameters are discarded so
// credentials accidentally pasted into an old URL cannot be forwarded.
func (s *Server) redirectLegacyDocument(w http.ResponseWriter, r *http.Request) bool {
	if !s.migrationEnabled() || s.migrationRequestHost(r) != migrationHostLegacy || (r.Method != http.MethodGet && r.Method != http.MethodHead) || isLegacyCompatibilityPath(r.URL.Path) || !isDocumentNavigation(r) || !safeLegacyDocumentPath(r.URL.Path, r.URL.EscapedPath()) {
		return false
	}
	destination, err := url.ParseRequestURI(s.Cfg.PublicOrigin)
	if err != nil || destination.Host == "" {
		return false
	}
	destination.Path = r.URL.Path
	destination.RawPath = ""
	destination.RawQuery = ""
	destination.ForceQuery = false
	destination.Fragment = ""
	w.Header().Set("Location", destination.String())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusTemporaryRedirect)
	return true
}

func isLegacyMutationMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func (s *Server) instanceMoved(w http.ResponseWriter) {
	s.writeError(w, http.StatusConflict, "instance_moved", "this Helm instance moved; open the canonical origin and review before submitting again", map[string]any{
		"canonical_origin": s.Cfg.PublicOrigin,
	})
}

func (s *Server) legacyPublicAuthMutationMoved(w http.ResponseWriter, r *http.Request) bool {
	if !s.migrationEnabled() || s.migrationRequestHost(r) != migrationHostLegacy || r.Method != http.MethodPost {
		return false
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	if len(parts) >= 2 && parts[0] == "auth" && (parts[1] == "setup" || parts[1] == "login") {
		s.instanceMoved(w)
		return true
	}
	return false
}

func (s *Server) legacyHumanMutationMoved(w http.ResponseWriter, r *http.Request, identity auth.Identity) bool {
	if !s.migrationEnabled() || s.migrationRequestHost(r) != migrationHostLegacy || !isAPIPath(r.URL.Path) || !isLegacyMutationMethod(r.Method) || identity.IsToken {
		return false
	}
	s.instanceMoved(w)
	return true
}

func (s *Server) legacyLogoutAllowed(r *http.Request) bool {
	if !s.migrationEnabled() || s.migrationRequestHost(r) != migrationHostLegacy || r.Method != http.MethodPost {
		return false
	}
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	return len(parts) >= 2 && parts[0] == "auth" && parts[1] == "logout"
}
