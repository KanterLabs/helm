package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// giteaFromEnv reads the Gitea / Forgejo OAuth connection settings
// (docs/GITEA_OAUTH_PLAN.md). The connection itself is optional: these
// settings only decide whether it is offered and where its 0600 state file
// lives. By default that is a forge directory beside the database file, so it
// follows the data volume with no configuration.
func giteaFromEnv(c *Config) error {
	mode, err := resolveEnv("HELM_GITEA_OAUTH")
	if err != nil {
		return err
	}
	switch mode.value {
	case "", "on":
		c.GiteaOAuth = true
	case "off":
		c.GiteaOAuth = false
	default:
		return fmt.Errorf("HELM_GITEA_OAUTH must be on or off")
	}
	dir, err := resolveEnv("HELM_FORGE_STATE_DIR")
	if err != nil {
		return err
	}
	if strings.ContainsFunc(dir.value, unicode.IsControl) {
		return fmt.Errorf("HELM_FORGE_STATE_DIR must not contain control characters")
	}
	if dir.value == "" {
		c.ForgeStateDir = filepath.Join(filepath.Dir(c.DB), "forge")
		return nil
	}
	c.ForgeStateDir = filepath.Clean(dir.value)
	return nil
}
