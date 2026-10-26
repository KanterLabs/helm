// Package config contains environment-backed server configuration.
package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Addr         string
	DB           string
	AuthMode     string
	PublicOrigin string
	// LegacyOrigin retains bearer-only API compatibility during a private hostname move.
	LegacyOrigin string
	AdminEmail   string
	// BetaSwitchEnabled exposes the beta release switcher only on the private
	// Tailnet deployment. It is deliberately disabled by default and is
	// validated against the exact beta origin below.
	BetaSwitchEnabled bool
	// BetaSwitchSocket is the absolute Unix socket used to reach the root-owned
	// beta switch broker. The application never invokes deployment commands
	// directly. BetaSwitchSocketPath is retained as an explicit alias for
	// callers that describe the setting as a path; both fields are populated
	// from the same environment value.
	BetaSwitchSocket     string
	BetaSwitchSocketPath string
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
	// CoolifyIntake enables the Coolify webhook alert route when configured.
	CoolifyIntake CoolifyIntake
	// PublicEndpoints configures Cloudflare-tunnel public webhook endpoints.
	PublicEndpoints PublicEndpoints
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
	betaSwitchEnabled, err := resolveEnv("HELM_BETA_SWITCH_ENABLED", "ROADMAP_BETA_SWITCH_ENABLED")
	if err != nil {
		return Config{}, err
	}
	betaSwitchSocket, err := resolveEnv(
		"HELM_BETA_SWITCH_SOCKET", "HELM_BETA_SWITCH_SOCKET_PATH",
		"ROADMAP_BETA_SWITCH_SOCKET", "ROADMAP_BETA_SWITCH_SOCKET_PATH",
	)
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
		LegacyOrigin:            legacyOrigin.value,
		AdminEmail:              adminEmail.value,
		BetaSwitchSocket:        valueOr(betaSwitchSocket, "/run/helm-beta-switcher/helm-beta-switchd.sock"),
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
	if value := betaSwitchEnabled.value; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HELM_BETA_SWITCH_ENABLED must be true or false: %w", err)
		}
		c.BetaSwitchEnabled = parsed
	}
	c.BetaSwitchSocket = strings.TrimSpace(c.BetaSwitchSocket)
	c.BetaSwitchSocketPath = c.BetaSwitchSocket
	if c.BetaSwitchSocket == "" || !filepath.IsAbs(c.BetaSwitchSocket) || strings.ContainsAny(c.BetaSwitchSocket, "\r\n\x00") {
		return Config{}, fmt.Errorf("HELM_BETA_SWITCH_SOCKET must be an absolute Unix socket path")
	}
	if value := lunaEnabled.value; value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("HELM_LUNA_ENABLED must be true or false: %w", err)
		}
		c.LunaDisabled = !parsed
	}
	return validateEnvironment(c, tailnetAllowedPeers)
}
