package config

import (
	"reflect"
	"testing"
)

// Failure modes: retired environment values may break startup, reactivate a
// provider, or leak into the effective configuration. Invalid legacy values
// must be ignored and produce exactly the ordinary configuration.
func TestRetiredGithubEnvironmentIgnored(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("HELM_PUBLIC_ORIGIN", "http://localhost:8080")
	names := []string{"HELM_GITHUB_ENABLED", "HELM_GITHUB_TOKEN_FILE", "HELM_GITHUB_REPOSITORIES", "HELM_GITHUB_APP_CLIENT_ID", "HELM_GITHUB_APP_SLUG", "HELM_GITHUB_STATE_DIR"}
	for _, name := range names {
		t.Setenv(name, "")
	}
	baseline, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		t.Setenv(name, "invalid legacy value\n")
	}
	got, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, baseline) {
		t.Fatal("legacy GitHub environment changed configuration")
	}
}
