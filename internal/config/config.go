// Package config contains environment-backed server configuration.
package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Addr         string
	DB           string
	AuthMode     string
	PublicOrigin string
	// LegacyOrigin is an optional, exact-origin compatibility host used during
	// a domain migration. An empty value keeps the single-origin behavior.
	LegacyOrigin string
	AdminEmail   string
	// CodexBinary is the executable used to launch the local Codex App Server.
	// CodexHomeRoot contains one isolated CODEX_HOME directory per Helm actor.
	CodexBinary   string
	CodexHomeRoot string
	// LunaDisabled is the emergency operator kill switch for model turns. Account
	// management remains available so disabling assistance never strands auth.
	LunaDisabled bool
	CodexModel   string
	CodexEffort  string
	// ReleaseSHA is an optional immutable deployment revision exposed by
	// health/discovery responses. It is accepted only in canonical git SHA
	// form so an env-file typo cannot masquerade as a release identifier.
	ReleaseSHA string
	// CloudflareIssuer and CloudflareAudience identify the Access team and
	// applications which are allowed to authenticate human requests. The JWKS
	// URL is optional; when omitted it is derived from the issuer's
	// /cdn-cgi/access/certs endpoint.
	CloudflareIssuer    string
	CloudflareAudience  string
	CloudflareAudiences []string
	// CloudflareHostAudiences optionally narrows the accepted Access
	// application audiences to exact Host authorities. The map is populated
	// from HELM_CF_ACCESS_HOST_AUDIENCES using host=aud1,aud2;host2=aud3.
	CloudflareHostAudiences map[string][]string
	CloudflareJWKSURL       string
	SecureCookies           bool
	DemoSeed                bool
}

func FromEnv() (Config, error) {
	addr, err := resolveEnv("HELM_ADDR", "ROADMAP_ADDR")
	if err != nil {
		return Config{}, err
	}
	db, err := resolveEnv("HELM_DB", "ROADMAP_DB")
	if err != nil {
		return Config{}, err
	}
	authMode, err := resolveEnv("HELM_AUTH_MODE", "ROADMAP_AUTH_MODE")
	if err != nil {
		return Config{}, err
	}
	publicOrigin, err := resolveEnv("HELM_PUBLIC_ORIGIN", "ROADMAP_PUBLIC_ORIGIN")
	if err != nil {
		return Config{}, err
	}
	legacyOrigin, err := resolveEnv("HELM_LEGACY_ORIGIN", "ROADMAP_LEGACY_ORIGIN")
	if err != nil {
		return Config{}, err
	}
	adminEmail, err := resolveEnv("HELM_ADMIN_EMAIL", "ROADMAP_ADMIN_EMAIL")
	if err != nil {
		return Config{}, err
	}
	codexBinary, err := resolveEnv("HELM_CODEX_BINARY", "ROADMAP_CODEX_BINARY")
	if err != nil {
		return Config{}, err
	}
	codexHomeRoot, err := resolveEnv("HELM_CODEX_HOME_ROOT", "ROADMAP_CODEX_HOME_ROOT")
	if err != nil {
		return Config{}, err
	}
	lunaEnabled, err := resolveEnv("HELM_LUNA_ENABLED", "ROADMAP_LUNA_ENABLED")
	if err != nil {
		return Config{}, err
	}
	codexModel, err := resolveEnv("HELM_LUNA_MODEL", "ROADMAP_LUNA_MODEL")
	if err != nil {
		return Config{}, err
	}
	codexEffort, err := resolveEnv("HELM_LUNA_EFFORT", "ROADMAP_LUNA_EFFORT")
	if err != nil {
		return Config{}, err
	}
	releaseSHA, err := resolveEnv("HELM_RELEASE_SHA", "ROADMAP_RELEASE_SHA")
	if err != nil {
		return Config{}, err
	}
	cloudflareIssuer, err := resolveEnv(
		"HELM_CLOUDFLARE_ISSUER", "HELM_CF_ACCESS_ISSUER",
		"ROADMAP_CLOUDFLARE_ISSUER", "ROADMAP_CF_ACCESS_ISSUER",
	)
	if err != nil {
		return Config{}, err
	}
	cloudflareAudience, err := resolveEnv(
		"HELM_CLOUDFLARE_AUDIENCE", "HELM_CLOUDFLARE_AUD",
		"ROADMAP_CLOUDFLARE_AUDIENCE", "ROADMAP_CLOUDFLARE_AUD",
	)
	if err != nil {
		return Config{}, err
	}
	cloudflareAudiences, err := resolveEnv(
		"HELM_CF_ACCESS_AUDIENCES", "HELM_CLOUDFLARE_AUDIENCES",
		"ROADMAP_CF_ACCESS_AUDIENCES", "ROADMAP_CLOUDFLARE_AUDIENCES",
	)
	if err != nil {
		return Config{}, err
	}
	cloudflareHostAudiences, err := resolveEnv("HELM_CF_ACCESS_HOST_AUDIENCES", "ROADMAP_CF_ACCESS_HOST_AUDIENCES")
	if err != nil {
		return Config{}, err
	}
	cloudflareJWKSURL, err := resolveEnv(
		"HELM_CLOUDFLARE_JWKS_URL", "HELM_CF_ACCESS_JWKS_URL", "HELM_CLOUDFLARE_CERTS_URL",
		"ROADMAP_CLOUDFLARE_JWKS_URL", "ROADMAP_CF_ACCESS_JWKS_URL", "ROADMAP_CLOUDFLARE_CERTS_URL",
	)
	if err != nil {
		return Config{}, err
	}
	secureCookies, err := resolveEnv("HELM_SECURE_COOKIES", "ROADMAP_SECURE_COOKIES")
	if err != nil {
		return Config{}, err
	}
	demoSeed, err := resolveEnv("HELM_DEMO_SEED", "ROADMAP_DEMO_SEED")
	if err != nil {
		return Config{}, err
	}

	c := Config{
		Addr:               valueOr(addr, ":8080"),
		DB:                 valueOr(db, "data/roadmap.db"),
		AuthMode:           strings.ToLower(valueOr(authMode, "local")),
		PublicOrigin:       strings.TrimRight(publicOrigin.value, "/"),
		LegacyOrigin:       strings.TrimRight(legacyOrigin.value, "/"),
		AdminEmail:         adminEmail.value,
		CodexBinary:        valueOr(codexBinary, "codex"),
		CodexHomeRoot:      valueOr(codexHomeRoot, "data/codex-users"),
		CodexModel:         valueOr(codexModel, "gpt-5.6-luna"),
		CodexEffort:        strings.ToLower(valueOr(codexEffort, "medium")),
		ReleaseSHA:         releaseSHA.value,
		CloudflareIssuer:   cloudflareIssuer.value,
		CloudflareAudience: cloudflareAudience.value,
		CloudflareJWKSURL:  cloudflareJWKSURL.value,
		SecureCookies:      true,
	}
	if value := cloudflareAudiences.value; value != "" {
		for _, audience := range strings.Split(value, ",") {
			if audience = strings.TrimSpace(audience); audience != "" {
				c.CloudflareAudiences = append(c.CloudflareAudiences, audience)
			}
		}
	}
	if len(c.CloudflareAudiences) == 0 && c.CloudflareAudience != "" {
		c.CloudflareAudiences = []string{c.CloudflareAudience}
	}
	if len(c.CloudflareAudiences) > 0 {
		c.CloudflareAudience = c.CloudflareAudiences[0]
	}
	if value := secureCookies.value; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HELM_SECURE_COOKIES must be true or false: %w", err)
		}
		c.SecureCookies = parsed
	}
	if value := demoSeed.value; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HELM_DEMO_SEED must be true or false: %w", err)
		}
		c.DemoSeed = parsed
	}
	if value := lunaEnabled.value; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HELM_LUNA_ENABLED must be true or false: %w", err)
		}
		c.LunaDisabled = !parsed
	}
	if c.AuthMode != "local" && c.AuthMode != "cloudflare" && c.AuthMode != "disabled" {
		return Config{}, fmt.Errorf("HELM_AUTH_MODE must be local, cloudflare, or disabled")
	}
	if c.ReleaseSHA != "" && !validReleaseSHA(c.ReleaseSHA) {
		return Config{}, fmt.Errorf("HELM_RELEASE_SHA must be 40 lowercase hexadecimal characters")
	}
	if strings.ContainsAny(c.CodexBinary, "\r\n\x00") {
		return Config{}, fmt.Errorf("HELM_CODEX_BINARY contains invalid characters")
	}
	if strings.ContainsAny(c.CodexHomeRoot, "\r\n\x00") {
		return Config{}, fmt.Errorf("HELM_CODEX_HOME_ROOT contains invalid characters")
	}
	if c.CodexModel == "" || len(c.CodexModel) > 128 || strings.ContainsAny(c.CodexModel, "\r\n\x00") {
		return Config{}, fmt.Errorf("HELM_LUNA_MODEL is invalid")
	}
	if _, ok := map[string]struct{}{"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {}}[c.CodexEffort]; !ok {
		return Config{}, fmt.Errorf("HELM_LUNA_EFFORT must be low, medium, high, xhigh, max, or ultra")
	}
	if c.AuthMode == "local" || c.AuthMode == "cloudflare" {
		origin, err := normalizeOrigin(c.PublicOrigin, c.AuthMode == "cloudflare" || c.LegacyOrigin != "")
		if err != nil {
			return Config{}, err
		}
		c.PublicOrigin = origin
	}
	if c.LegacyOrigin != "" {
		if c.AuthMode == "disabled" {
			return Config{}, fmt.Errorf("HELM_LEGACY_ORIGIN requires HELM_AUTH_MODE local or cloudflare")
		}
		origin, err := normalizeNamedOrigin("HELM_LEGACY_ORIGIN", c.LegacyOrigin, true)
		if err != nil {
			return Config{}, err
		}
		c.PublicOrigin = canonicalizeOriginAuthority(c.PublicOrigin)
		origin = canonicalizeOriginAuthority(origin)
		if sameOrigin(c.PublicOrigin, origin) {
			return Config{}, fmt.Errorf("HELM_LEGACY_ORIGIN must differ from HELM_PUBLIC_ORIGIN")
		}
		c.LegacyOrigin = origin
	}
	if cloudflareHostAudiences.value != "" && c.LegacyOrigin == "" {
		c.PublicOrigin = canonicalizeOriginAuthority(c.PublicOrigin)
	}
	if cloudflareHostAudiences.value != "" {
		if c.AuthMode != "cloudflare" {
			return Config{}, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES requires HELM_AUTH_MODE=cloudflare")
		}
		parsed, err := parseCloudflareHostAudiences(cloudflareHostAudiences.value, c.CloudflareAudiences, c.PublicOrigin, c.LegacyOrigin)
		if err != nil {
			return Config{}, err
		}
		c.CloudflareHostAudiences = parsed
	}
	if c.AuthMode == "disabled" && !loopbackAddr(c.Addr) {
		return Config{}, fmt.Errorf("HELM_AUTH_MODE=disabled requires HELM_ADDR to bind to loopback")
	}
	if c.AuthMode == "cloudflare" {
		if !loopbackAddr(c.Addr) {
			return Config{}, fmt.Errorf("HELM_AUTH_MODE=cloudflare requires HELM_ADDR to bind to loopback")
		}
		if !c.SecureCookies {
			return Config{}, fmt.Errorf("HELM_SECURE_COOKIES must be true in cloudflare mode")
		}
		if c.DemoSeed {
			return Config{}, fmt.Errorf("HELM_DEMO_SEED must be false in cloudflare mode")
		}
		if !validEmail(c.AdminEmail) {
			return Config{}, fmt.Errorf("HELM_ADMIN_EMAIL must be a valid email when HELM_AUTH_MODE=cloudflare")
		}
		if c.CloudflareIssuer == "" {
			return Config{}, fmt.Errorf("HELM_CLOUDFLARE_ISSUER is required when HELM_AUTH_MODE=cloudflare")
		}
		if err := validateURL("HELM_CLOUDFLARE_ISSUER", c.CloudflareIssuer, true); err != nil {
			return Config{}, err
		}
		if len(c.CloudflareAudiences) < 2 {
			return Config{}, fmt.Errorf("HELM_CF_ACCESS_AUDIENCES must include the UI and API application audiences")
		}
		seenAudiences := make(map[string]struct{}, len(c.CloudflareAudiences))
		for _, audience := range c.CloudflareAudiences {
			if strings.TrimSpace(audience) == "" || strings.ContainsAny(audience, "\r\n,") {
				return Config{}, fmt.Errorf("HELM_CF_ACCESS_AUDIENCES contains an invalid audience")
			}
			if _, exists := seenAudiences[audience]; exists {
				return Config{}, fmt.Errorf("HELM_CF_ACCESS_AUDIENCES must contain distinct application audiences")
			}
			seenAudiences[audience] = struct{}{}
		}
		if c.CloudflareJWKSURL != "" {
			if err := validateURL("HELM_CLOUDFLARE_JWKS_URL", c.CloudflareJWKSURL, true); err != nil {
				return Config{}, err
			}
		}
	}
	return c, nil
}

type resolvedEnv struct {
	name  string
	value string
}

// resolveEnv reads one canonical environment variable and its compatibility
// aliases. Empty values are treated as unset, preserving the existing
// fallback behavior. Every non-empty spelling must agree before a value is
// accepted; in particular, a legacy value cannot silently override a Helm
// value (or vice versa).
func resolveEnv(names ...string) (resolvedEnv, error) {
	var resolved resolvedEnv
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		if resolved.name == "" {
			resolved = resolvedEnv{name: name, value: value}
			continue
		}
		if value != resolved.value {
			return resolvedEnv{}, fmt.Errorf("conflicting environment variables %s and %s", resolved.name, name)
		}
	}
	return resolved, nil
}

func valueOr(value resolvedEnv, fallback string) string {
	if value.value != "" {
		return value.value
	}
	return fallback
}

func envOr(name, fallback string) string {
	if value, err := resolveEnv(name); err == nil && value.value != "" {
		return value.value
	}
	return fallback
}

func envOrAny(names ...string) string {
	if value, err := resolveEnv(names...); err == nil {
		return value.value
	}
	return ""
}

func validateURL(name, value string, requireHTTPS bool) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.ForceQuery || parsed.Fragment != "" || requireHTTPS && parsed.Scheme != "https" {
		if requireHTTPS {
			return fmt.Errorf("%s must be an https URL", name)
		}
		return fmt.Errorf("%s must be an http(s) URL", name)
	}
	return nil
}

func normalizeOrigin(value string, requireHTTPS bool) (string, error) {
	return normalizeNamedOrigin("HELM_PUBLIC_ORIGIN", value, requireHTTPS)
}

func normalizeNamedOrigin(name, value string, requireHTTPS bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required when HELM_AUTH_MODE is local or cloudflare", name)
	}
	// A root origin may be written with one or more trailing slashes. Strip
	// those before parsing so the resulting value is stable across env files.
	value = strings.TrimRight(value, "/")
	if value == "" {
		return "", fmt.Errorf("%s must be a normalized http(s) origin", name)
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" || strings.Contains(value, "#") || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%s must be a normalized http(s) origin", name)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s must use https", name)
	}
	normalized := strings.TrimRight(value, "/")
	if normalized == "" {
		return "", fmt.Errorf("%s must be a normalized http(s) origin", name)
	}
	return normalized, nil
}

func sameOrigin(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	l, leftErr := url.ParseRequestURI(left)
	r, rightErr := url.ParseRequestURI(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if !strings.EqualFold(l.Scheme, r.Scheme) || !strings.EqualFold(l.Hostname(), r.Hostname()) {
		return false
	}
	return effectiveOriginPort(l) == effectiveOriginPort(r)
}

func originAuthority(origin string) string {
	parsed, err := url.ParseRequestURI(origin)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func effectiveOriginPort(origin *url.URL) string {
	if port := origin.Port(); port != "" {
		return port
	}
	if strings.EqualFold(origin.Scheme, "https") {
		return "443"
	}
	return "80"
}

func canonicalizeOriginAuthority(value string) string {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return value
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := parsed.Port()
	if (strings.EqualFold(parsed.Scheme, "https") && port == "443") || (strings.EqualFold(parsed.Scheme, "http") && port == "80") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = host
	parsed.Path = ""
	parsed.RawPath = ""
	return parsed.String()
}

func parseCloudflareHostAudiences(value string, configuredAudiences []string, publicOrigin, legacyOrigin string) (map[string][]string, error) {
	result := make(map[string][]string)
	assignedAudiences := make(map[string]string)
	configured := make(map[string]struct{}, len(configuredAudiences))
	for _, audience := range configuredAudiences {
		configured[audience] = struct{}{}
	}
	for _, entry := range strings.Split(value, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES contains an empty host mapping")
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES entries must use host=audience[,audience]")
		}
		host := strings.TrimSpace(parts[0])
		if !validHostAuthority(host) {
			return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES contains an invalid host")
		}
		host = strings.ToLower(host)
		for existing := range result {
			if strings.EqualFold(existing, host) {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES contains a duplicate host")
			}
		}
		audienceValues := strings.Split(parts[1], ",")
		if len(audienceValues) == 0 {
			return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES requires an audience for each host")
		}
		seen := make(map[string]struct{}, len(audienceValues))
		clean := make([]string, 0, len(audienceValues))
		for _, audience := range audienceValues {
			audience = strings.TrimSpace(audience)
			if audience == "" || strings.ContainsAny(audience, "\r\n;=") {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES contains an invalid audience")
			}
			if _, ok := configured[audience]; !ok {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES audience is not in HELM_CF_ACCESS_AUDIENCES")
			}
			if previousHost, ok := assignedAudiences[audience]; ok && previousHost != host {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES cannot reuse an audience across hosts")
			}
			if _, ok := seen[audience]; ok {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES contains a duplicate audience")
			}
			seen[audience] = struct{}{}
			assignedAudiences[audience] = host
			clean = append(clean, audience)
		}
		result[host] = clean
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES must not be empty")
	}
	if !hostAudienceMappingIncludes(result, originAuthority(publicOrigin)) {
		return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES must include the public origin host")
	}
	if legacyOrigin != "" && !hostAudienceMappingIncludes(result, originAuthority(legacyOrigin)) {
		return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES must include the legacy origin host")
	}
	if legacyOrigin != "" {
		publicHost := originAuthority(publicOrigin)
		legacyHost := originAuthority(legacyOrigin)
		for mapped := range result {
			if !strings.EqualFold(mapped, publicHost) && !strings.EqualFold(mapped, legacyHost) {
				return nil, fmt.Errorf("HELM_CF_ACCESS_HOST_AUDIENCES may contain only the public and legacy origin hosts")
			}
		}
	}
	return result, nil
}

func hostAudienceMappingIncludes(mapping map[string][]string, host string) bool {
	for mapped := range mapping {
		if strings.EqualFold(mapped, host) {
			return true
		}
	}
	return false
}

func validHostAuthority(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\t /?#@=*%;") || strings.Contains(value, "://") {
		return false
	}
	parsed, err := url.ParseRequestURI("https://" + value)
	if err != nil || parsed.Host != value || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	if strings.HasSuffix(value, ":") && !strings.HasSuffix(value, "]") {
		return false
	}
	if port := parsed.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return false
		}
	}
	return true
}

func validEmail(value string) bool {
	if value == "" || len(value) > 320 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value
}

func loopbackAddr(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	} else if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = strings.Trim(value, "[]")
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validReleaseSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
