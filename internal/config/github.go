package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

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
