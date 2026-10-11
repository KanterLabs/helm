package config

import (
	"fmt"
	"net/url"
	"strings"
)

// CoolifyIntake pins the routing for Coolify webhook alerts. Every field comes
// from operator configuration; webhook content can never choose the
// assignee or secret. Tickets land in the ticket queue. A zero value means
// the intake route is disabled.
type CoolifyIntake struct {
	// Secret is the URL path credential Coolify presents. Coolify webhooks
	// carry no signature or custom headers, so the secret must be part of the
	// configured URL. It is never logged or echoed. It comes from exactly one
	// of HELM_COOLIFY_WEBHOOK_SECRET_FILE or, for deployments whose only
	// private channel is a root-only environment file,
	// HELM_COOLIFY_WEBHOOK_SECRET.
	Secret     []byte
	SecretFile string
	// Project is the deprecated HELM_COOLIFY_PROJECT. It is accepted so
	// existing deployments keep starting, but ignored: Coolify tickets land
	// in the ticket queue like every other intake (docs/TICKET_QUEUE_PLAN.md).
	Project string
	// Assignee is a human actor ID or email resolved on every delivery.
	Assignee string
}

func (c CoolifyIntake) Enabled() bool {
	return len(c.Secret) > 0
}

func coolifyIntakeFromEnv() (CoolifyIntake, error) {
	secretFile, err := resolveEnv("HELM_COOLIFY_WEBHOOK_SECRET_FILE")
	if err != nil {
		return CoolifyIntake{}, err
	}
	secretValue, err := resolveEnv("HELM_COOLIFY_WEBHOOK_SECRET")
	if err != nil {
		return CoolifyIntake{}, err
	}
	project, err := resolveEnv("HELM_COOLIFY_PROJECT")
	if err != nil {
		return CoolifyIntake{}, err
	}
	assignee, err := resolveEnv("HELM_COOLIFY_ASSIGNEE")
	if err != nil {
		return CoolifyIntake{}, err
	}
	intake := CoolifyIntake{
		SecretFile: strings.TrimSpace(secretFile.value),
		Project:    strings.TrimSpace(project.value),
		Assignee:   strings.TrimSpace(assignee.value),
	}
	hasSecret := intake.SecretFile != "" || secretValue.value != ""
	if !hasSecret && intake.Project == "" && intake.Assignee == "" {
		return CoolifyIntake{}, nil
	}
	if intake.SecretFile != "" && secretValue.value != "" {
		return CoolifyIntake{}, fmt.Errorf("set only one of HELM_COOLIFY_WEBHOOK_SECRET_FILE and HELM_COOLIFY_WEBHOOK_SECRET")
	}
	if !hasSecret || intake.Assignee == "" {
		return CoolifyIntake{}, fmt.Errorf("a Coolify webhook secret and HELM_COOLIFY_ASSIGNEE must be set together")
	}
	if strings.ContainsAny(intake.Project+intake.Assignee, "\r\n\x00") {
		return CoolifyIntake{}, fmt.Errorf("HELM_COOLIFY_PROJECT and HELM_COOLIFY_ASSIGNEE must not contain control characters")
	}
	secret := secretValue.value
	if intake.SecretFile != "" {
		raw, err := LoadPrivateKeyFile(intake.SecretFile)
		if err != nil {
			return CoolifyIntake{}, fmt.Errorf("invalid HELM_COOLIFY_WEBHOOK_SECRET_FILE: %w", err)
		}
		secret = strings.TrimSpace(string(raw))
	}
	if !validURLSecret(secret) {
		return CoolifyIntake{}, fmt.Errorf("the Coolify webhook secret must be 32-256 URL-safe characters (A-Z, a-z, 0-9, '-', '_')")
	}
	intake.Secret = []byte(secret)
	return intake, nil
}

func validURLSecret(value string) bool {
	if len(value) < 32 || len(value) > 256 {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-', char == '_':
		default:
			return false
		}
	}
	return true
}

// PublicEndpoints configures the Cloudflare-tunnel public webhook endpoint.
type PublicEndpoints struct {
	// HooksAddr is the loopback listener that serves only webhook routes to
	// cloudflared. Empty disables public endpoints.
	HooksAddr string
	// CloudflareAPIBase overrides the Cloudflare API (tests use a loopback
	// fixture); production uses https://api.cloudflare.com/client/v4.
	CloudflareAPIBase string
	// CloudflaredBinary runs the tunnel connector.
	CloudflaredBinary string
	// ProbeOrigin sends the public URL self-test to a loopback origin with
	// the public Host header instead of resolving the hostname (tests only).
	ProbeOrigin string
	// OAuth configures "Sign in with Cloudflare" for guided setup.
	OAuth CloudflareOAuth
}

// Helm's published Cloudflare OAuth client (public PKCE client, no secret)
// and the KanterLabs relay registered as its only redirect URL. See
// docs/CLOUDFLARE_CONNECT_PLAN.md and deploy/cloudflare-connect-relay.
const (
	DefaultCloudflareOAuthClientID = "f05395c32033f7b61981048f5e523cec"
	DefaultCloudflareOAuthRelayURL = "https://helm-connect.shanekanterman04.workers.dev/cloudflare/callback"
	DefaultCloudflareDashboardURL  = "https://dash.cloudflare.com"
)

// CloudflareOAuth configures the OAuth client used by guided setup. Empty
// ClientID disables the button; the token link always remains.
type CloudflareOAuth struct {
	ClientID string
	// RelayURL is the client's registered redirect URL, which bounces the
	// browser back to this Helm's callback.
	RelayURL string
	// DashboardURL hosts /oauth2/auth, /oauth2/token and /oauth2/revoke
	// (tests point it at a loopback fixture).
	DashboardURL string
}

func cloudflareOAuthFromEnv() (CloudflareOAuth, error) {
	mode, err := resolveEnv("HELM_CLOUDFLARE_OAUTH")
	if err != nil {
		return CloudflareOAuth{}, err
	}
	switch mode.value {
	case "", "on":
	case "off":
		return CloudflareOAuth{}, nil
	default:
		return CloudflareOAuth{}, fmt.Errorf("HELM_CLOUDFLARE_OAUTH must be on or off")
	}
	clientID, err := resolveEnv("HELM_CLOUDFLARE_OAUTH_CLIENT_ID")
	if err != nil {
		return CloudflareOAuth{}, err
	}
	relay, err := resolveEnv("HELM_CLOUDFLARE_OAUTH_RELAY_URL")
	if err != nil {
		return CloudflareOAuth{}, err
	}
	dashboard, err := resolveEnv("HELM_CLOUDFLARE_DASHBOARD_URL")
	if err != nil {
		return CloudflareOAuth{}, err
	}
	settings := CloudflareOAuth{
		ClientID:     valueOr(clientID, DefaultCloudflareOAuthClientID),
		RelayURL:     valueOr(relay, DefaultCloudflareOAuthRelayURL),
		DashboardURL: strings.TrimRight(valueOr(dashboard, DefaultCloudflareDashboardURL), "/"),
	}
	for name, value := range map[string]string{"HELM_CLOUDFLARE_OAUTH_RELAY_URL": settings.RelayURL, "HELM_CLOUDFLARE_DASHBOARD_URL": settings.DashboardURL} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackAddr(parsed.Host))) {
			return CloudflareOAuth{}, fmt.Errorf("%s must be an https URL (or loopback http for tests)", name)
		}
	}
	if strings.ContainsAny(settings.ClientID, " /?#&") {
		return CloudflareOAuth{}, fmt.Errorf("HELM_CLOUDFLARE_OAUTH_CLIENT_ID is not a valid client ID")
	}
	return settings, nil
}

func publicEndpointsFromEnv() (PublicEndpoints, error) {
	addr, err := resolveEnv("HELM_PUBLIC_HOOKS_ADDR")
	if err != nil {
		return PublicEndpoints{}, err
	}
	base, err := resolveEnv("HELM_CLOUDFLARE_API_BASE")
	if err != nil {
		return PublicEndpoints{}, err
	}
	binary, err := resolveEnv("HELM_CLOUDFLARED_BINARY")
	if err != nil {
		return PublicEndpoints{}, err
	}
	probe, err := resolveEnv("HELM_PUBLIC_PROBE_ORIGIN")
	if err != nil {
		return PublicEndpoints{}, err
	}
	oauth, err := cloudflareOAuthFromEnv()
	if err != nil {
		return PublicEndpoints{}, err
	}
	settings := PublicEndpoints{HooksAddr: valueOr(addr, "127.0.0.1:8091"), CloudflareAPIBase: strings.TrimRight(valueOr(base, "https://api.cloudflare.com/client/v4"), "/"), CloudflaredBinary: valueOr(binary, "cloudflared"), ProbeOrigin: strings.TrimRight(probe.value, "/"), OAuth: oauth}
	if settings.HooksAddr == "off" {
		settings.HooksAddr = ""
	} else if !loopbackAddr(settings.HooksAddr) {
		return PublicEndpoints{}, fmt.Errorf("HELM_PUBLIC_HOOKS_ADDR must be a loopback host:port or off")
	}
	parsed, err := url.Parse(settings.CloudflareAPIBase)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackAddr(parsed.Host))) {
		return PublicEndpoints{}, fmt.Errorf("HELM_CLOUDFLARE_API_BASE must be an https URL (or loopback http for tests)")
	}
	if settings.ProbeOrigin != "" {
		if parsed, err := url.Parse(settings.ProbeOrigin); err != nil || parsed.Scheme != "http" || !loopbackAddr(parsed.Host) || parsed.Path != "" {
			return PublicEndpoints{}, fmt.Errorf("HELM_PUBLIC_PROBE_ORIGIN must be a loopback http origin (tests only)")
		}
	}
	if strings.ContainsAny(settings.CloudflaredBinary, "\r\n\x00") {
		return PublicEndpoints{}, fmt.Errorf("HELM_CLOUDFLARED_BINARY contains invalid characters")
	}
	return settings, nil
}
