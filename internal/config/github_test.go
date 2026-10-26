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
