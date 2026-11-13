package forgeconnect

import (
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const maxURLLength = 255

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func validClientID(id string) bool { return clientIDPattern.MatchString(id) }

// serverURL is a validated forge address: lower-case scheme and host, an
// escaped path prefix without a trailing slash.
type serverURL struct{ scheme, host, hostname, port, path string }

func (s serverURL) String() string { return s.scheme + "://" + s.host + s.path }
func (s serverURL) origin() string { return s.scheme + "://" + s.host }

// hostKey compares hosts with default ports made explicit.
func (s serverURL) hostKey() string {
	port := s.port
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[s.scheme]
	}
	return s.hostname + ":" + port
}

// parseServerURL applies the C4 address rules. A missing scheme means https.
func parseServerURL(raw string) (serverURL, error) {
	bad := serverURL{}
	invalid := fail(CodeInvalidServerURL)
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLength {
		return bad, invalid
	}
	for _, r := range raw {
		if r <= ' ' || r >= 0x7f || r == '\\' {
			return bad, invalid
		}
	}
	if strings.ContainsAny(raw, "?#") {
		return bad, invalid
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return bad, invalid
	}
	s := serverURL{scheme: strings.ToLower(u.Scheme), host: strings.ToLower(u.Host), hostname: strings.ToLower(u.Hostname()), port: u.Port()}
	if s.scheme != "http" && s.scheme != "https" || s.hostname == "" || strings.HasSuffix(u.Host, ":") {
		return bad, invalid
	}
	if s.port != "" {
		if n, err := strconv.Atoi(s.port); err != nil || n < 1 || n > 65535 {
			return bad, invalid
		}
	}
	if ip, err := netip.ParseAddr(s.hostname); err == nil {
		if ip.Zone() != "" || blockedAddr(ip) {
			return bad, invalid
		}
	} else {
		for _, r := range s.hostname {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
				return bad, invalid
			}
		}
	}
	s.path = strings.TrimRight(u.EscapedPath(), "/")
	if s.path != "" {
		for _, segment := range strings.Split(s.path[1:], "/") {
			if segment == "" || segment == "." || segment == ".." {
				return bad, invalid
			}
		}
	}
	if len(s.String()) > maxURLLength {
		return bad, invalid
	}
	return s, nil
}

func normalizeBaseURL(raw string, allowHTTP bool) (string, error) {
	s, err := parseServerURL(raw)
	if err != nil {
		return "", err
	}
	if s.scheme == "http" && !allowHTTP {
		return "", fail(CodeInsecureHTTP)
	}
	return s.String(), nil
}

// probeCandidates lists where to look for the discovery document: the given
// path, its parents, then the origin; at most four.
func probeCandidates(raw string, allowHTTP bool) ([]string, error) {
	s, err := parseServerURL(raw)
	if err != nil {
		return nil, err
	}
	if s.scheme == "http" && !allowHTTP {
		return nil, fail(CodeInsecureHTTP)
	}
	list := []string{s.String()}
	for p := s.path; len(list) < 3; {
		p = p[:strings.LastIndex(p, "/")+1]
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			break
		}
		list = append(list, s.origin()+p)
	}
	if s.path != "" {
		list = append(list, s.origin())
	}
	return list, nil
}

// normalizeOrigin validates Helm's public origin: scheme and host only.
func normalizeOrigin(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return "", fail(CodeUnavailable)
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

// RedirectURI is the callback the forge sends the browser back to; it is
// identical at authorize and exchange. Empty without a public origin.
func RedirectURI(publicOrigin string) string {
	origin := strings.TrimRight(strings.TrimSpace(publicOrigin), "/")
	if origin == "" {
		return ""
	}
	return origin + gitea.CallbackPath
}

// RegisterURL is the forge page where the OAuth2 application is created.
func RegisterURL(base string) string { return settingsURL(base) }

// RevokeURL is the forge page where the user removes Helm's authorization.
func RevokeURL(base string) string { return settingsURL(base) }

func settingsURL(base string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return ""
	}
	return base + gitea.SettingsPath
}
