package config

import (
	"strings"
	"testing"
)

func TestFromEnvLunaControls(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HELM_AUTH_MODE", "disabled")
	t.Setenv("HELM_ADDR", "127.0.0.1:8080")
	t.Setenv("HELM_LUNA_ENABLED", "false")
	t.Setenv("HELM_LUNA_MODEL", "custom-luna")
	t.Setenv("HELM_LUNA_EFFORT", "high")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LunaDisabled || cfg.CodexModel != "custom-luna" || cfg.CodexEffort != "high" {
		t.Fatalf("Luna config = %+v", cfg)
	}

	for name, value := range map[string]string{"HELM_LUNA_ENABLED": "sometimes", "HELM_LUNA_EFFORT": "extreme"} {
		t.Run(name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("HELM_AUTH_MODE", "disabled")
			t.Setenv("HELM_ADDR", "127.0.0.1:8080")
			t.Setenv(name, value)
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid Luna control accepted")
			}
		})
	}
}

func TestFromEnvCanonicalAndLegacySettings(t *testing.T) {
	tests := []struct {
		name      string
		canonical string
		legacy    string
		value     string
		want      string
		field     func(Config) string
	}{
		{
			name: "address", canonical: "HELM_ADDR", legacy: "ROADMAP_ADDR",
			value: "127.0.0.1:9090", want: "127.0.0.1:9090",
			field: func(cfg Config) string { return cfg.Addr },
		},
		{
			name: "database", canonical: "HELM_DB", legacy: "ROADMAP_DB",
			value: "/var/lib/helm/roadmap.db", want: "/var/lib/helm/roadmap.db",
			field: func(cfg Config) string { return cfg.DB },
		},
		{
			name: "auth mode", canonical: "HELM_AUTH_MODE", legacy: "ROADMAP_AUTH_MODE",
			value: "disabled", want: "disabled",
			field: func(cfg Config) string { return cfg.AuthMode },
		},
		{
			name: "public origin", canonical: "HELM_PUBLIC_ORIGIN", legacy: "ROADMAP_PUBLIC_ORIGIN",
			value: " https://helm.example/ ", want: "https://helm.example",
			field: func(cfg Config) string { return cfg.PublicOrigin },
		},
		{
			name: "administrator", canonical: "HELM_ADMIN_EMAIL", legacy: "ROADMAP_ADMIN_EMAIL",
			value: "owner@example.com", want: "owner@example.com",
			field: func(cfg Config) string { return cfg.AdminEmail },
		},
		{
			name: "Codex binary", canonical: "HELM_CODEX_BINARY", legacy: "ROADMAP_CODEX_BINARY",
			value: "/usr/local/bin/codex", want: "/usr/local/bin/codex",
			field: func(cfg Config) string { return cfg.CodexBinary },
		},
		{
			name: "Codex home root", canonical: "HELM_CODEX_HOME_ROOT", legacy: "ROADMAP_CODEX_HOME_ROOT",
			value: "/var/lib/helm/codex-users", want: "/var/lib/helm/codex-users",
			field: func(cfg Config) string { return cfg.CodexHomeRoot },
		},
		{
			name: "release SHA", canonical: "HELM_RELEASE_SHA", legacy: "ROADMAP_RELEASE_SHA",
			value: strings.Repeat("a", 40), want: strings.Repeat("a", 40),
			field: func(cfg Config) string { return cfg.ReleaseSHA },
		},
		{
			name: "secure cookies", canonical: "HELM_SECURE_COOKIES", legacy: "ROADMAP_SECURE_COOKIES",
			value: "false", want: "false",
			field: func(cfg Config) string {
				if cfg.SecureCookies {
					return "true"
				}
				return "false"
			},
		},
		{
			name: "demo seed", canonical: "HELM_DEMO_SEED", legacy: "ROADMAP_DEMO_SEED",
			value: "true", want: "true",
			field: func(cfg Config) string {
				if cfg.DemoSeed {
					return "true"
				}
				return "false"
			},
		},
		{
			name: "Cloudflare issuer", canonical: "HELM_CLOUDFLARE_ISSUER", legacy: "ROADMAP_CLOUDFLARE_ISSUER",
			value: "https://team.cloudflareaccess.com", want: "https://team.cloudflareaccess.com",
			field: func(cfg Config) string { return cfg.CloudflareIssuer },
		},
		{
			name: "Cloudflare audience", canonical: "HELM_CLOUDFLARE_AUDIENCE", legacy: "ROADMAP_CLOUDFLARE_AUDIENCE",
			value: "ui-audience", want: "ui-audience",
			field: func(cfg Config) string { return cfg.CloudflareAudience },
		},
		{
			name: "Cloudflare audiences", canonical: "HELM_CF_ACCESS_AUDIENCES", legacy: "ROADMAP_CF_ACCESS_AUDIENCES",
			value: "ui-audience, api-audience", want: "ui-audience,api-audience",
			field: func(cfg Config) string { return strings.Join(cfg.CloudflareAudiences, ",") },
		},
		{
			name: "Cloudflare JWKS URL", canonical: "HELM_CLOUDFLARE_JWKS_URL", legacy: "ROADMAP_CLOUDFLARE_JWKS_URL",
			value: "https://team.cloudflareaccess.com/certs", want: "https://team.cloudflareaccess.com/certs",
			field: func(cfg Config) string { return cfg.CloudflareJWKSURL },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, variant := range []string{"canonical", "legacy", "equal"} {
				t.Run(variant, func(t *testing.T) {
					clearConfigEnv(t)
					t.Setenv("HELM_AUTH_MODE", "disabled")
					if tt.canonical != "HELM_ADDR" {
						t.Setenv("HELM_ADDR", "127.0.0.1:8080")
					}
					switch variant {
					case "canonical":
						t.Setenv(tt.canonical, tt.value)
					case "legacy":
						t.Setenv(tt.legacy, tt.value)
					case "equal":
						t.Setenv(tt.canonical, tt.value)
						t.Setenv(tt.legacy, tt.value)
					}
					cfg, err := FromEnv()
					if err != nil {
						t.Fatal(err)
					}
					if got := tt.field(cfg); got != tt.want {
						t.Fatalf("%s value = %q, want %q", variant, got, tt.want)
					}
				})
			}
		})
	}
}

func TestFromEnvConflictingSettingsFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		canonical  string
		legacy     string
		leftValue  string
		rightValue string
	}{
		{name: "address", canonical: "HELM_ADDR", legacy: "ROADMAP_ADDR", leftValue: "127.0.0.1:8080", rightValue: "127.0.0.1:9090"},
		{name: "database", canonical: "HELM_DB", legacy: "ROADMAP_DB", leftValue: "/var/lib/helm-a.db", rightValue: "/var/lib/helm-b.db"},
		{name: "auth mode", canonical: "HELM_AUTH_MODE", legacy: "ROADMAP_AUTH_MODE", leftValue: "disabled", rightValue: "local"},
		{name: "public origin", canonical: "HELM_PUBLIC_ORIGIN", legacy: "ROADMAP_PUBLIC_ORIGIN", leftValue: "https://helm-a.example", rightValue: "https://helm-b.example"},
		{name: "administrator", canonical: "HELM_ADMIN_EMAIL", legacy: "ROADMAP_ADMIN_EMAIL", leftValue: "a@example.com", rightValue: "b@example.com"},
		{name: "Codex binary", canonical: "HELM_CODEX_BINARY", legacy: "ROADMAP_CODEX_BINARY", leftValue: "/opt/a/codex", rightValue: "/opt/b/codex"},
		{name: "Codex home root", canonical: "HELM_CODEX_HOME_ROOT", legacy: "ROADMAP_CODEX_HOME_ROOT", leftValue: "/var/lib/helm/a", rightValue: "/var/lib/helm/b"},
		{name: "release SHA", canonical: "HELM_RELEASE_SHA", legacy: "ROADMAP_RELEASE_SHA", leftValue: strings.Repeat("a", 40), rightValue: strings.Repeat("b", 40)},
		{name: "secure cookies", canonical: "HELM_SECURE_COOKIES", legacy: "ROADMAP_SECURE_COOKIES", leftValue: "true", rightValue: "false"},
		{name: "demo seed", canonical: "HELM_DEMO_SEED", legacy: "ROADMAP_DEMO_SEED", leftValue: "true", rightValue: "false"},
		{name: "issuer", canonical: "HELM_CLOUDFLARE_ISSUER", legacy: "ROADMAP_CLOUDFLARE_ISSUER", leftValue: "https://issuer-a.example", rightValue: "https://issuer-b.example"},
		{name: "audience", canonical: "HELM_CLOUDFLARE_AUDIENCE", legacy: "ROADMAP_CLOUDFLARE_AUDIENCE", leftValue: "audience-a", rightValue: "audience-b"},
		{name: "audiences", canonical: "HELM_CF_ACCESS_AUDIENCES", legacy: "ROADMAP_CF_ACCESS_AUDIENCES", leftValue: "ui-a,api-a", rightValue: "ui-b,api-b"},
		{name: "JWKS URL", canonical: "HELM_CLOUDFLARE_JWKS_URL", legacy: "ROADMAP_CLOUDFLARE_JWKS_URL", leftValue: "https://jwks-a.example/certs", rightValue: "https://jwks-b.example/certs"},
		{name: "issuer compatibility aliases", canonical: "ROADMAP_CLOUDFLARE_ISSUER", legacy: "ROADMAP_CF_ACCESS_ISSUER", leftValue: "https://issuer-a.example", rightValue: "https://issuer-b.example"},
		{name: "audience compatibility aliases", canonical: "ROADMAP_CLOUDFLARE_AUDIENCE", legacy: "ROADMAP_CLOUDFLARE_AUD", leftValue: "audience-a", rightValue: "audience-b"},
		{name: "audiences compatibility aliases", canonical: "ROADMAP_CF_ACCESS_AUDIENCES", legacy: "ROADMAP_CLOUDFLARE_AUDIENCES", leftValue: "ui-a,api-a", rightValue: "ui-b,api-b"},
		{name: "JWKS compatibility aliases", canonical: "ROADMAP_CLOUDFLARE_JWKS_URL", legacy: "ROADMAP_CF_ACCESS_JWKS_URL", leftValue: "https://jwks-a.example/certs", rightValue: "https://jwks-b.example/certs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("HELM_AUTH_MODE", "disabled")
			if tt.canonical != "HELM_ADDR" && tt.legacy != "ROADMAP_ADDR" {
				t.Setenv("HELM_ADDR", "127.0.0.1:8080")
			}
			t.Setenv(tt.canonical, tt.leftValue)
			t.Setenv(tt.legacy, tt.rightValue)
			_, err := FromEnv()
			if err == nil {
				t.Fatal("conflicting settings unexpectedly accepted")
			}
			message := err.Error()
			if !strings.Contains(message, tt.canonical) || !strings.Contains(message, tt.legacy) {
				t.Fatalf("conflict error = %q, want both variable names", message)
			}
			if strings.Contains(message, tt.leftValue) || strings.Contains(message, tt.rightValue) {
				t.Fatalf("conflict error leaked a value: %q", message)
			}
		})
	}
}

func TestFromEnvExistingCloudflareAliases(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
		field func(Config) string
		want  string
	}{
		{
			name: "Access issuer", env: "ROADMAP_CF_ACCESS_ISSUER", value: "https://team.cloudflareaccess.com",
			field: func(cfg Config) string { return cfg.CloudflareIssuer }, want: "https://team.cloudflareaccess.com",
		},
		{
			name: "short audience", env: "ROADMAP_CLOUDFLARE_AUD", value: "ui-audience",
			field: func(cfg Config) string { return cfg.CloudflareAudience }, want: "ui-audience",
		},
		{
			name: "Access audiences", env: "ROADMAP_CF_ACCESS_AUDIENCES", value: "ui-audience,api-audience",
			field: func(cfg Config) string { return strings.Join(cfg.CloudflareAudiences, ",") }, want: "ui-audience,api-audience",
		},
		{
			name: "Cloudflare audiences", env: "ROADMAP_CLOUDFLARE_AUDIENCES", value: "ui-audience,api-audience",
			field: func(cfg Config) string { return strings.Join(cfg.CloudflareAudiences, ",") }, want: "ui-audience,api-audience",
		},
		{
			name: "Access JWKS URL", env: "ROADMAP_CF_ACCESS_JWKS_URL", value: "https://team.cloudflareaccess.com/certs",
			field: func(cfg Config) string { return cfg.CloudflareJWKSURL }, want: "https://team.cloudflareaccess.com/certs",
		},
		{
			name: "Cloudflare certs URL", env: "ROADMAP_CLOUDFLARE_CERTS_URL", value: "https://team.cloudflareaccess.com/certs",
			field: func(cfg Config) string { return cfg.CloudflareJWKSURL }, want: "https://team.cloudflareaccess.com/certs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("HELM_AUTH_MODE", "disabled")
			t.Setenv("HELM_ADDR", "127.0.0.1:8080")
			t.Setenv(tt.env, tt.value)
			cfg, err := FromEnv()
			if err != nil {
				t.Fatal(err)
			}
			if got := tt.field(cfg); got != tt.want {
				t.Fatalf("alias value = %q, want %q", got, tt.want)
			}
		})
	}
}
