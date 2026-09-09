// Package config contains environment-backed server configuration.
package config

import (
	"bytes"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr         string
	DB           string
	AuthMode     string
	PublicOrigin string
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
	CloudflareJWKSURL   string
	// Tailnet mode accepts only assertions minted by the loopback forward-auth
	// helper. The key is loaded once from an owner-protected regular file and
	// is never included in configuration responses or logs.
	TailnetOwnerLogin       string
	TailnetAudience         string
	TailnetAssertionKeyFile string
	TailnetAssertionKey     []byte
	TailnetTLSAddr          string
	TailnetTLSCertFile      string
	TailnetTLSKeyFile       string
	TailnetAllowedPeerIPs   []string
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
	cloudflareJWKSURL, err := resolveEnv(
		"HELM_CLOUDFLARE_JWKS_URL", "HELM_CF_ACCESS_JWKS_URL", "HELM_CLOUDFLARE_CERTS_URL",
		"ROADMAP_CLOUDFLARE_JWKS_URL", "ROADMAP_CF_ACCESS_JWKS_URL", "ROADMAP_CLOUDFLARE_CERTS_URL",
	)
	if err != nil {
		return Config{}, err
	}
	tailnetOwnerLogin, err := resolveEnv(
		"HELM_TAILNET_OWNER_LOGIN", "HELM_TAILNET_ADMIN_EMAIL",
		"ROADMAP_TAILNET_OWNER_LOGIN", "ROADMAP_TAILNET_ADMIN_EMAIL",
	)
	if err != nil {
		return Config{}, err
	}
	tailnetAudience, err := resolveEnv("HELM_TAILNET_AUDIENCE", "ROADMAP_TAILNET_AUDIENCE")
	if err != nil {
		return Config{}, err
	}
	tailnetKeyFile, err := resolveEnv(
		"HELM_TAILNET_ASSERTION_KEY_FILE", "HELM_TAILNET_AUTH_KEY_FILE", "HELM_TAILNET_KEY_FILE",
		"ROADMAP_TAILNET_ASSERTION_KEY_FILE", "ROADMAP_TAILNET_AUTH_KEY_FILE", "ROADMAP_TAILNET_KEY_FILE",
	)
	if err != nil {
		return Config{}, err
	}
	tailnetTLSAddr, err := resolveEnv("HELM_TAILNET_TLS_ADDR", "ROADMAP_TAILNET_TLS_ADDR")
	if err != nil {
		return Config{}, err
	}
	tailnetTLSCertFile, err := resolveEnv("HELM_TAILNET_TLS_CERT_FILE", "ROADMAP_TAILNET_TLS_CERT_FILE")
	if err != nil {
		return Config{}, err
	}
	tailnetTLSKeyFile, err := resolveEnv("HELM_TAILNET_TLS_KEY_FILE", "ROADMAP_TAILNET_TLS_KEY_FILE")
	if err != nil {
		return Config{}, err
	}
	tailnetAllowedPeers, err := resolveEnv("HELM_TAILNET_ALLOWED_PEER_IPS", "ROADMAP_TAILNET_ALLOWED_PEER_IPS")
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
		Addr:                    valueOr(addr, ":8080"),
		DB:                      valueOr(db, "data/roadmap.db"),
		AuthMode:                strings.ToLower(valueOr(authMode, "local")),
		PublicOrigin:            strings.TrimRight(publicOrigin.value, "/"),
		AdminEmail:              adminEmail.value,
		CodexBinary:             valueOr(codexBinary, "codex"),
		CodexHomeRoot:           valueOr(codexHomeRoot, "data/codex-users"),
		CodexModel:              valueOr(codexModel, "gpt-5.6-luna"),
		CodexEffort:             strings.ToLower(valueOr(codexEffort, "medium")),
		ReleaseSHA:              releaseSHA.value,
		CloudflareIssuer:        cloudflareIssuer.value,
		CloudflareAudience:      cloudflareAudience.value,
		CloudflareJWKSURL:       cloudflareJWKSURL.value,
		TailnetOwnerLogin:       strings.TrimSpace(tailnetOwnerLogin.value),
		TailnetAudience:         strings.TrimSpace(tailnetAudience.value),
		TailnetAssertionKeyFile: strings.TrimSpace(tailnetKeyFile.value),
		TailnetTLSAddr:          strings.TrimSpace(tailnetTLSAddr.value),
		TailnetTLSCertFile:      strings.TrimSpace(tailnetTLSCertFile.value),
		TailnetTLSKeyFile:       strings.TrimSpace(tailnetTLSKeyFile.value),
		SecureCookies:           true,
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
	if c.AuthMode != "local" && c.AuthMode != "cloudflare" && c.AuthMode != "tailnet" && c.AuthMode != "disabled" {
		return Config{}, fmt.Errorf("HELM_AUTH_MODE must be local, cloudflare, tailnet, or disabled")
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
	if c.AuthMode == "local" || c.AuthMode == "cloudflare" || c.AuthMode == "tailnet" {
		origin, err := normalizeOrigin(c.PublicOrigin, c.AuthMode == "cloudflare" || c.AuthMode == "tailnet")
		if err != nil {
			return Config{}, err
		}
		c.PublicOrigin = origin
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
	if c.AuthMode == "tailnet" {
		if !loopbackAddr(c.Addr) {
			return Config{}, fmt.Errorf("HELM_AUTH_MODE=tailnet requires HELM_ADDR to bind to loopback")
		}
		if !c.SecureCookies {
			return Config{}, fmt.Errorf("HELM_SECURE_COOKIES must be true in tailnet mode")
		}
		if c.DemoSeed {
			return Config{}, fmt.Errorf("HELM_DEMO_SEED must be false in tailnet mode")
		}
		if !validEmail(c.AdminEmail) {
			return Config{}, fmt.Errorf("HELM_ADMIN_EMAIL must be a valid email when HELM_AUTH_MODE=tailnet")
		}
		if !validTailnetLogin(c.TailnetOwnerLogin) {
			return Config{}, fmt.Errorf("HELM_TAILNET_OWNER_LOGIN must be a non-empty login without controls when HELM_AUTH_MODE=tailnet")
		}
		if c.TailnetAudience == "" {
			c.TailnetAudience = c.PublicOrigin
		}
		if c.TailnetAudience != c.PublicOrigin {
			return Config{}, fmt.Errorf("HELM_TAILNET_AUDIENCE must exactly match HELM_PUBLIC_ORIGIN")
		}
		if c.TailnetAssertionKeyFile == "" {
			return Config{}, fmt.Errorf("HELM_TAILNET_ASSERTION_KEY_FILE is required when HELM_AUTH_MODE=tailnet")
		}
		key, err := LoadPrivateKeyFile(c.TailnetAssertionKeyFile)
		if err != nil {
			return Config{}, fmt.Errorf("invalid HELM_TAILNET_ASSERTION_KEY_FILE: %w", err)
		}
		c.TailnetAssertionKey = key
		if err := validatePrivateTLSAddr(c.TailnetTLSAddr); err != nil {
			return Config{}, fmt.Errorf("invalid HELM_TAILNET_TLS_ADDR: %w", err)
		}
		if c.TailnetTLSCertFile == "" || c.TailnetTLSKeyFile == "" {
			return Config{}, fmt.Errorf("HELM_TAILNET_TLS_CERT_FILE and HELM_TAILNET_TLS_KEY_FILE are required when HELM_AUTH_MODE=tailnet")
		}
		if err := ValidateRegularFile(c.TailnetTLSCertFile, 1<<20, false); err != nil {
			return Config{}, fmt.Errorf("invalid HELM_TAILNET_TLS_CERT_FILE: %w", err)
		}
		if _, err := LoadPrivateKeyFile(c.TailnetTLSKeyFile); err != nil {
			return Config{}, fmt.Errorf("invalid HELM_TAILNET_TLS_KEY_FILE: %w", err)
		}
		if strings.TrimSpace(tailnetAllowedPeers.value) == "" {
			return Config{}, fmt.Errorf("HELM_TAILNET_ALLOWED_PEER_IPS is required when HELM_AUTH_MODE=tailnet")
		}
		seenPeers := make(map[string]struct{})
		for _, raw := range strings.Split(tailnetAllowedPeers.value, ",") {
			raw = strings.TrimSpace(raw)
			ip := net.ParseIP(raw)
			if ip == nil || ip.IsUnspecified() {
				return Config{}, fmt.Errorf("HELM_TAILNET_ALLOWED_PEER_IPS contains an invalid IP")
			}
			canonical := ip.String()
			if _, exists := seenPeers[canonical]; exists {
				return Config{}, fmt.Errorf("HELM_TAILNET_ALLOWED_PEER_IPS must contain distinct IPs")
			}
			seenPeers[canonical] = struct{}{}
			c.TailnetAllowedPeerIPs = append(c.TailnetAllowedPeerIPs, canonical)
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
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("HELM_PUBLIC_ORIGIN is required when HELM_AUTH_MODE is local or cloudflare")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") || (parsed.Path != "" && parsed.Path != "/") || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("HELM_PUBLIC_ORIGIN must be a normalized http(s) origin")
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return "", fmt.Errorf("HELM_PUBLIC_ORIGIN must use https in cloudflare mode")
	}
	normalized := strings.TrimRight(value, "/")
	if normalized == "" {
		return "", fmt.Errorf("HELM_PUBLIC_ORIGIN must be a normalized http(s) origin")
	}
	return normalized, nil
}

func validEmail(value string) bool {
	if value == "" || len(value) > 320 || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value
}

func validTailnetLogin(value string) bool {
	return value != "" && len(value) <= 320 && value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\r\n\x00")
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

// IsLoopbackAddr exposes the same strict bind-address predicate used by the
// application configuration to the standalone helper command.
func IsLoopbackAddr(value string) bool { return loopbackAddr(value) }

// ValidateRegularFile checks a file path without following a symlink. It is
// used for the secondary TLS listener certificate and key boundary.
func ValidateRegularFile(path string, maxSize int64, ownerProtected bool) error {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\r\n\x00") {
		return fmt.Errorf("file path is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file must be a regular file")
	}
	if ownerProtected && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("file permissions must not grant group or other access")
	}
	if maxSize > 0 && info.Size() > maxSize {
		return fmt.Errorf("file is too large")
	}
	return nil
}

func validatePrivateTLSAddr(value string) error {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("must be an explicit private IP:port")
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || !ip.IsPrivate() {
		return fmt.Errorf("must bind to a non-loopback private IP")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return fmt.Errorf("port is invalid")
	}
	return nil
}

// LoadPrivateKeyFile reads a regular, owner-protected assertion key file. A
// symlink is rejected deliberately: deployments should point the service at
// an explicitly provisioned secret, not at a mutable indirection.
func LoadPrivateKeyFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\r\n\x00") {
		return nil, fmt.Errorf("key file path is invalid")
	}
	if err := ValidateRegularFile(path, 4096, true); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() < 32 {
		return nil, fmt.Errorf("key file must contain at least 32 bytes")
	}
	// Keep path handling explicit without ever including secret bytes in an
	// error or log. filepath.Clean also avoids accidental empty-path reads.
	path = filepath.Clean(path)
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) < 32 || bytes.Equal(key, make([]byte, len(key))) {
		return nil, fmt.Errorf("key file must contain a non-zero key of at least 32 bytes")
	}
	return append([]byte(nil), key...), nil
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
