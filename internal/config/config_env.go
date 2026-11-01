package config

import (
	"fmt"
	"os"
	"strings"
)

type resolvedEnv struct {
	name  string
	value string
}

// resolveEnv reads one canonical environment variable and its compatibility
// aliases. Empty values are treated as unset, preserving the existing
// fallback behavior. Every non-empty spelling must agree before a value is
// accepted; in particular, a legacy value cannot silently override a Helm
// value (or vice versa).
func resolveEnv(names ...string) (resolvedEnv, error) {
	var resolved resolvedEnv
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		if resolved.name == "" {
			resolved = resolvedEnv{name: name, value: value}
			continue
		}
		if value != resolved.value {
			return resolvedEnv{}, fmt.Errorf("conflicting environment variables %s and %s", resolved.name, name)
		}
	}
	return resolved, nil
}

func valueOr(value resolvedEnv, fallback string) string {
	if value.value != "" {
		return value.value
	}
	return fallback
}

func envOr(name, fallback string) string {
	if value, err := resolveEnv(name); err == nil && value.value != "" {
		return value.value
	}
	return fallback
}

func envOrAny(names ...string) string {
	if value, err := resolveEnv(names...); err == nil {
		return value.value
	}
	return ""
}
