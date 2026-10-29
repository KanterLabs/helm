package config

import (
	"os"
	"path/filepath"
	"testing"
)

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HELM_ADDR", "HELM_DB", "HELM_AUTH_MODE", "HELM_PUBLIC_ORIGIN",
		"HELM_ADMIN_EMAIL", "HELM_RELEASE_SHA", "HELM_CLOUDFLARE_ISSUER", "HELM_CF_ACCESS_ISSUER",
		"HELM_BETA_SWITCH_ENABLED", "HELM_BETA_SWITCH_SOCKET", "HELM_BETA_SWITCH_SOCKET_PATH",
		"HELM_CODEX_BINARY", "HELM_CODEX_HOME_ROOT",
		"HELM_LUNA_ENABLED", "HELM_LUNA_MODEL", "HELM_LUNA_EFFORT",
		"HELM_AUDIT_MODEL", "HELM_AUDIT_EFFORT",
		"HELM_CLOUDFLARE_AUDIENCE", "HELM_CLOUDFLARE_AUD", "HELM_CF_ACCESS_AUDIENCES", "HELM_CLOUDFLARE_AUDIENCES",
		"HELM_CLOUDFLARE_JWKS_URL", "HELM_CF_ACCESS_JWKS_URL", "HELM_CLOUDFLARE_CERTS_URL", "HELM_SECURE_COOKIES",
		"HELM_DEMO_SEED", "HELM_TAILNET_OWNER_LOGIN", "HELM_TAILNET_ADMIN_EMAIL", "HELM_TAILNET_AUDIENCE",
		"HELM_TAILNET_ASSERTION_KEY_FILE", "HELM_TAILNET_AUTH_KEY_FILE", "HELM_TAILNET_KEY_FILE",
		"HELM_TAILNET_TLS_ADDR", "HELM_TAILNET_TLS_CERT_FILE", "HELM_TAILNET_TLS_KEY_FILE", "HELM_TAILNET_ALLOWED_PEER_IPS",
		"ROADMAP_ADDR", "ROADMAP_DB", "ROADMAP_AUTH_MODE", "ROADMAP_PUBLIC_ORIGIN",
		"ROADMAP_ADMIN_EMAIL", "ROADMAP_RELEASE_SHA", "ROADMAP_CLOUDFLARE_ISSUER", "ROADMAP_CF_ACCESS_ISSUER",
		"ROADMAP_BETA_SWITCH_ENABLED", "ROADMAP_BETA_SWITCH_SOCKET", "ROADMAP_BETA_SWITCH_SOCKET_PATH",
		"ROADMAP_CODEX_BINARY", "ROADMAP_CODEX_HOME_ROOT",
		"ROADMAP_LUNA_ENABLED", "ROADMAP_LUNA_MODEL", "ROADMAP_LUNA_EFFORT",
		"ROADMAP_AUDIT_MODEL", "ROADMAP_AUDIT_EFFORT",
		"ROADMAP_CLOUDFLARE_AUDIENCE", "ROADMAP_CLOUDFLARE_AUD", "ROADMAP_CF_ACCESS_AUDIENCES", "ROADMAP_CLOUDFLARE_AUDIENCES",
		"ROADMAP_CLOUDFLARE_JWKS_URL", "ROADMAP_CF_ACCESS_JWKS_URL", "ROADMAP_CLOUDFLARE_CERTS_URL", "ROADMAP_SECURE_COOKIES",
		"ROADMAP_DEMO_SEED", "ROADMAP_TAILNET_OWNER_LOGIN", "ROADMAP_TAILNET_ADMIN_EMAIL", "ROADMAP_TAILNET_AUDIENCE",
		"ROADMAP_TAILNET_ASSERTION_KEY_FILE", "ROADMAP_TAILNET_AUTH_KEY_FILE", "ROADMAP_TAILNET_KEY_FILE",
		"ROADMAP_TAILNET_TLS_ADDR", "ROADMAP_TAILNET_TLS_CERT_FILE", "ROADMAP_TAILNET_TLS_KEY_FILE", "ROADMAP_TAILNET_ALLOWED_PEER_IPS",
	} {
		t.Setenv(name, "")
	}
}

func TestFromEnvDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HELM_PUBLIC_ORIGIN", "http://localhost:8080/")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("default address = %q, want :8080", cfg.Addr)
	}
	if cfg.DB != "data/roadmap.db" {
		t.Fatalf("default database = %q, want data/roadmap.db", cfg.DB)
	}
	if cfg.AuthMode != "local" {
		t.Fatalf("default auth mode = %q, want local", cfg.AuthMode)
	}
	if cfg.CodexBinary != "codex" || cfg.CodexHomeRoot != "data/codex-users" {
		t.Fatalf("default Codex config = binary %q root %q", cfg.CodexBinary, cfg.CodexHomeRoot)
	}
	if cfg.LunaDisabled || cfg.CodexModel != "gpt-5.6-luna" || cfg.CodexEffort != "medium" {
		t.Fatalf("default Luna config = disabled %v model %q effort %q", cfg.LunaDisabled, cfg.CodexModel, cfg.CodexEffort)
	}
	if cfg.PublicOrigin != "http://localhost:8080" {
		t.Fatalf("normalized default origin = %q", cfg.PublicOrigin)
	}
	if !cfg.SecureCookies {
		t.Fatal("secure cookies defaulted to false")
	}
	if cfg.DemoSeed {
		t.Fatal("demo seed defaulted to true")
	}
	if cfg.BetaSwitchEnabled {
		t.Fatal("beta switcher defaulted to enabled")
	}
	if cfg.BetaSwitchSocket != "/run/helm-beta-switcher/helm-beta-switchd.sock" || cfg.BetaSwitchSocketPath != cfg.BetaSwitchSocket {
		t.Fatalf("beta switcher socket = %q/%q", cfg.BetaSwitchSocket, cfg.BetaSwitchSocketPath)
	}
}

func TestFromEnvBetaSwitchBoundary(t *testing.T) {
	t.Run("invalid enabled value", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("HELM_AUTH_MODE", "disabled")
		t.Setenv("HELM_ADDR", "127.0.0.1:8080")
		t.Setenv("HELM_BETA_SWITCH_ENABLED", "sometimes")
		if _, err := FromEnv(); err == nil {
			t.Fatal("invalid beta switch enabled value accepted")
		}
	})

	t.Run("relative socket rejected", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("HELM_AUTH_MODE", "disabled")
		t.Setenv("HELM_ADDR", "127.0.0.1:8080")
		t.Setenv("HELM_BETA_SWITCH_SOCKET", "run/helm-beta-switcher/helm-beta-switchd.sock")
		if _, err := FromEnv(); err == nil {
			t.Fatal("relative beta switch socket accepted")
		}
	})

	t.Run("enabled requires tailnet", func(t *testing.T) {
		clearConfigEnv(t)
		t.Setenv("HELM_AUTH_MODE", "disabled")
		t.Setenv("HELM_ADDR", "127.0.0.1:8080")
		t.Setenv("HELM_BETA_SWITCH_ENABLED", "true")
		if _, err := FromEnv(); err == nil {
			t.Fatal("beta switcher enabled outside Tailnet mode")
		}
	})

	t.Run("enabled requires exact beta origin", func(t *testing.T) {
		setMinimalTailnetEnv(t, "https://not-beta.example")
		t.Setenv("HELM_BETA_SWITCH_ENABLED", "true")
		if _, err := FromEnv(); err == nil {
			t.Fatal("beta switcher accepted a non-beta origin")
		}
	})

	t.Run("enabled with exact beta origin", func(t *testing.T) {
		setMinimalTailnetEnv(t, "https://beta-helm.home.shanekanterman.dev/")
		t.Setenv("HELM_BETA_SWITCH_ENABLED", "true")
		t.Setenv("HELM_BETA_SWITCH_SOCKET_PATH", "/run/custom-beta-switchd.sock")
		cfg, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.BetaSwitchEnabled || cfg.BetaSwitchSocket != "/run/custom-beta-switchd.sock" || cfg.BetaSwitchSocketPath != cfg.BetaSwitchSocket {
			t.Fatalf("beta switcher config = %+v", cfg)
		}
	})
}

func setMinimalTailnetEnv(t *testing.T, origin string) {
	t.Helper()
	clearConfigEnv(t)
	dir := t.TempDir()
	assertionPath := filepath.Join(dir, "tailnet.key")
	tlsKeyPath := filepath.Join(dir, "tls.key")
	certPath := filepath.Join(dir, "tls.crt")
	key := []byte("01234567890123456789012345678901")
	for _, path := range []string{assertionPath, tlsKeyPath} {
		if err := os.WriteFile(path, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(certPath, []byte("certificate"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"HELM_AUTH_MODE":                  "tailnet",
		"HELM_ADDR":                       "127.0.0.1:8080",
		"HELM_PUBLIC_ORIGIN":              origin,
		"HELM_ADMIN_EMAIL":                "owner@example.com",
		"HELM_TAILNET_OWNER_LOGIN":        "ShaneKanterman04@github",
		"HELM_TAILNET_ASSERTION_KEY_FILE": assertionPath,
		"HELM_TAILNET_TLS_ADDR":           "10.0.0.39:8443",
		"HELM_TAILNET_TLS_CERT_FILE":      certPath,
		"HELM_TAILNET_TLS_KEY_FILE":       tlsKeyPath,
		"HELM_TAILNET_ALLOWED_PEER_IPS":   "10.0.0.101",
	} {
		t.Setenv(name, value)
	}
}
