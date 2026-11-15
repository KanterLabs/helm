package config

import "testing"

func TestGitHubEnvironment(t *testing.T) {
	t.Setenv("HELM_GITHUB_ENABLED", "true")
	t.Setenv("HELM_GITHUB_TOKEN_FILE", "/missing/protected-token")
	t.Setenv("HELM_GITHUB_REPOSITORIES", "KanterLabs/helm, example/repo,KanterLabs/helm")
	c := Config{}
	if err := githubFromEnv(&c); err != nil {
		t.Fatal(err)
	}
	if !c.GitHubEnabled || c.GitHubTokenFile != "/missing/protected-token" || len(c.GitHubRepositories) != 2 {
		t.Fatalf("unexpected settings: %+v", c)
	}
	t.Setenv("HELM_GITHUB_REPOSITORIES", "https://attacker.invalid/repo")
	if err := githubFromEnv(&c); err == nil {
		t.Fatal("accepted URL as repository")
	}
}

func TestGitHubEnvironmentMissingTokenDoesNotFail(t *testing.T) {
	t.Setenv("HELM_GITHUB_ENABLED", "true")
	t.Setenv("HELM_GITHUB_TOKEN_FILE", "")
	t.Setenv("HELM_GITHUB_REPOSITORIES", "example/repo")
	if err := githubFromEnv(&Config{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HELM_GITHUB_ENABLED", "not-bool")
	if err := githubFromEnv(&Config{}); err == nil {
		t.Fatal("accepted bad boolean")
	}
}

func TestGitHubDeviceFlowEnvironment(t *testing.T) {
	for _, name := range []string{"HELM_GITHUB_APP_CLIENT_ID", "HELM_GITHUB_APP_SLUG", "HELM_GITHUB_STATE_DIR", "HELM_GITHUB_REPOSITORIES"} {
		t.Setenv(name, "")
	}
	c := Config{DB: "/srv/helm/data/roadmap.db"}
	if err := githubFromEnv(&c); err != nil {
		t.Fatal(err)
	}
	if c.GitHubAppClientID != DefaultGitHubAppClientID || c.GitHubAppSlug != DefaultGitHubAppSlug || c.GitHubStateDir != "/srv/helm/data/github" {
		t.Fatalf("defaults: %+v", c)
	}
	relative := Config{DB: "data/roadmap.db"}
	if err := githubFromEnv(&relative); err != nil || relative.GitHubStateDir != "data/github" {
		t.Fatalf("relative default %q: %v", relative.GitHubStateDir, err)
	}
	t.Setenv("HELM_GITHUB_APP_CLIENT_ID", "Iv23liABCdef.123")
	t.Setenv("HELM_GITHUB_APP_SLUG", "kanterlabs-helm")
	t.Setenv("HELM_GITHUB_STATE_DIR", "/var/lib/helm/github")
	if err := githubFromEnv(&c); err != nil {
		t.Fatal(err)
	}
	if c.GitHubAppClientID != "Iv23liABCdef.123" || c.GitHubAppSlug != "kanterlabs-helm" || c.GitHubStateDir != "/var/lib/helm/github" {
		t.Fatalf("explicit: %+v", c)
	}
	bad := map[string][]string{
		"HELM_GITHUB_APP_CLIENT_ID": {"has space", "a/b", "id?x=1", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"},
		"HELM_GITHUB_APP_SLUG":      {"Bad_Slug", "-helm", "helm-", "a/b", "UPPER"},
		"HELM_GITHUB_STATE_DIR":     {"/var/lib/\x01helm", "/var/lib/helm\x7f", "/var/lib/\u0085helm"},
	}
	for name, values := range bad {
		for _, value := range values {
			t.Run(name, func(t *testing.T) {
				t.Setenv(name, value)
				if err := githubFromEnv(&Config{DB: "/srv/helm/data/roadmap.db"}); err == nil {
					t.Fatalf("accepted %s=%q", name, value)
				}
			})
		}
	}
}
