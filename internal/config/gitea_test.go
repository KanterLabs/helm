package config

import (
	"path/filepath"
	"testing"
)

func TestGiteaEnvironment(t *testing.T) {
	t.Setenv("HELM_GITEA_OAUTH", "")
	t.Setenv("HELM_FORGE_STATE_DIR", "")
	c := Config{DB: "/srv/helm/data/roadmap.db"}
	if err := giteaFromEnv(&c); err != nil {
		t.Fatal(err)
	}
	if !c.GiteaOAuth || c.ForgeStateDir != "/srv/helm/data/forge" {
		t.Fatalf("defaults = %v %q", c.GiteaOAuth, c.ForgeStateDir)
	}
	c = Config{DB: "data/roadmap.db"}
	if err := giteaFromEnv(&c); err != nil || c.ForgeStateDir != filepath.Join("data", "forge") {
		t.Fatalf("relative database: %q %v", c.ForgeStateDir, err)
	}

	t.Setenv("HELM_GITEA_OAUTH", "off")
	t.Setenv("HELM_FORGE_STATE_DIR", "/var/lib/helm/forge/")
	c = Config{DB: "data/roadmap.db"}
	if err := giteaFromEnv(&c); err != nil || c.GiteaOAuth || c.ForgeStateDir != "/var/lib/helm/forge" {
		t.Fatalf("explicit = %v %q %v", c.GiteaOAuth, c.ForgeStateDir, err)
	}
	t.Setenv("HELM_GITEA_OAUTH", "on")
	if err := giteaFromEnv(&c); err != nil || !c.GiteaOAuth {
		t.Fatalf("on = %v %v", c.GiteaOAuth, err)
	}

	for _, bad := range []string{"true", "1", "yes", "OFF", "disabled"} {
		t.Setenv("HELM_GITEA_OAUTH", bad)
		if err := giteaFromEnv(&Config{DB: "data/roadmap.db"}); err == nil {
			t.Errorf("HELM_GITEA_OAUTH=%q accepted", bad)
		}
	}
	t.Setenv("HELM_GITEA_OAUTH", "on")
	for _, bad := range []string{"/var/lib/helm\nforge", "/var/lib/helm\x7fforge", "/var/lib/\rforge", "/var/lib/\tforge"} {
		t.Setenv("HELM_FORGE_STATE_DIR", bad)
		if err := giteaFromEnv(&Config{DB: "data/roadmap.db"}); err == nil {
			t.Errorf("HELM_FORGE_STATE_DIR=%q accepted", bad)
		}
	}
}

func TestGiteaEnvironmentFromEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HELM_PUBLIC_ORIGIN", "http://localhost:8080")
	t.Setenv("HELM_DB", "/data/helm.db")
	t.Setenv("HELM_GITEA_OAUTH", "")
	t.Setenv("HELM_FORGE_STATE_DIR", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.GiteaOAuth || cfg.ForgeStateDir != "/data/forge" {
		t.Fatalf("FromEnv = %v %q", cfg.GiteaOAuth, cfg.ForgeStateDir)
	}
	t.Setenv("HELM_GITEA_OAUTH", "maybe")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv accepted a bad HELM_GITEA_OAUTH")
	}
}
