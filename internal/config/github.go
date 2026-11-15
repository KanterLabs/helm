package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

// Helm's published GitHub App (device flow, no client secret). Empty until
// the App is registered (gate G1); empty means sign-in is unavailable.
const (
	DefaultGitHubAppClientID = ""
	DefaultGitHubAppSlug     = ""
)

var (
	githubClientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	githubSlugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// githubAppFromEnv reads the device-flow settings. There is deliberately no
// setting for GitHub's origins: only github.com and api.github.com are used.
func githubAppFromEnv(c *Config) error {
	c.GitHubAppClientID = envOr("HELM_GITHUB_APP_CLIENT_ID", DefaultGitHubAppClientID)
	if c.GitHubAppClientID != "" && !githubClientIDPattern.MatchString(c.GitHubAppClientID) {
		return fmt.Errorf("HELM_GITHUB_APP_CLIENT_ID is not a valid GitHub App client ID")
	}
	c.GitHubAppSlug = envOr("HELM_GITHUB_APP_SLUG", DefaultGitHubAppSlug)
	if c.GitHubAppSlug != "" && (len(c.GitHubAppSlug) > 100 || !githubSlugPattern.MatchString(c.GitHubAppSlug)) {
		return fmt.Errorf("HELM_GITHUB_APP_SLUG must be a lowercase GitHub App slug")
	}
	c.GitHubStateDir = envOr("HELM_GITHUB_STATE_DIR", filepath.Join(filepath.Dir(c.DB), "github"))
	for _, r := range c.GitHubStateDir {
		if unicode.IsControl(r) {
			return fmt.Errorf("HELM_GITHUB_STATE_DIR must not contain control characters")
		}
	}
	return nil
}

// githubFromEnv only validates configuration. Missing credentials leave the
// optional provider unconfigured, never preventing ordinary Helm readiness.
// The provider alone reads the credential file.
func githubFromEnv(c *Config) error {
	enabled := strings.TrimSpace(os.Getenv("HELM_GITHUB_ENABLED"))
	if enabled != "" {
		value, err := strconv.ParseBool(enabled)
		if err != nil {
			return fmt.Errorf("HELM_GITHUB_ENABLED must be true or false")
		}
		c.GitHubEnabled = value
	}
	if err := githubAppFromEnv(c); err != nil {
		return err
	}
	c.GitHubTokenFile = strings.TrimSpace(os.Getenv("HELM_GITHUB_TOKEN_FILE"))
	if c.GitHubTokenFile != "" && (!filepath.IsAbs(c.GitHubTokenFile) || strings.ContainsAny(c.GitHubTokenFile, "\r\n\x00")) {
		return fmt.Errorf("HELM_GITHUB_TOKEN_FILE must be an absolute protected file path")
	}
	c.GitHubRepositories = []string{}
	seen := map[string]bool{}
	raw := strings.TrimSpace(os.Getenv("HELM_GITHUB_REPOSITORIES"))
	if raw == "" {
		return nil
	}
	for _, entry := range strings.Split(raw, ",") {
		repository, err := githubprovider.NormalizeRepository(strings.TrimSpace(entry))
		if err != nil {
			return fmt.Errorf("HELM_GITHUB_REPOSITORIES must contain owner/name pairs")
		}
		if !seen[repository] {
			c.GitHubRepositories = append(c.GitHubRepositories, repository)
			seen[repository] = true
		}
	}
	if len(c.GitHubRepositories) > 50 {
		return fmt.Errorf("HELM_GITHUB_REPOSITORIES supports at most 50 repositories")
	}
	return nil
}
