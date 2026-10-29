package config

import (
	"strings"
	"testing"
)

func TestAuditProfileIndependentDefaultsAndOverrides(t *testing.T) {
	clearConfigEnv(t)
	for _, name := range []string{"HELM_AUDIT_MODEL", "HELM_AUDIT_EFFORT", "ROADMAP_AUDIT_MODEL", "ROADMAP_AUDIT_EFFORT"} {
		t.Setenv(name, "")
	}
	t.Setenv("HELM_AUTH_MODE", "disabled")
	t.Setenv("HELM_ADDR", "127.0.0.1:8080")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	model, effort := cfg.AuditProfile()
	if model != "gpt-5.6-sol" || effort != "medium" || cfg.CodexModel != "gpt-5.6-luna" || cfg.CodexEffort != "medium" {
		t.Fatalf("profiles are not independent: audit=%s/%s ordinary=%s/%s", model, effort, cfg.CodexModel, cfg.CodexEffort)
	}
	if model, effort = (Config{}).AuditProfile(); model != "gpt-5.6-sol" || effort != "medium" {
		t.Fatalf("programmatic defaults = %s/%s", model, effort)
	}
	t.Setenv("HELM_AUDIT_MODEL", "gpt-6-sol")
	t.Setenv("HELM_AUDIT_EFFORT", "HIGH")
	t.Setenv("HELM_LUNA_MODEL", "everyday-model")
	t.Setenv("HELM_LUNA_EFFORT", "low")
	cfg, err = FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	model, effort = cfg.AuditProfile()
	if model != "gpt-6-sol" || effort != "high" || cfg.CodexModel != "everyday-model" || cfg.CodexEffort != "low" {
		t.Fatal("audit override changed ordinary assistance or was ignored")
	}
	t.Setenv("HELM_AUDIT_MODEL", "")
	t.Setenv("ROADMAP_AUDIT_MODEL", "compat-model")
	cfg, err = FromEnv()
	if err != nil || cfg.AuditModel != "compat-model" {
		t.Fatalf("audit alias not honored: %v", err)
	}
	t.Setenv("HELM_AUDIT_MODEL", "conflicting-model")
	if _, err = FromEnv(); err == nil {
		t.Fatal("conflicting audit aliases accepted")
	}
}

func TestAuditProfileRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"HELM_AUDIT_MODEL", "model\ninvalid"},
		{"HELM_AUDIT_MODEL", strings.Repeat("x", 129)},
		{"HELM_AUDIT_EFFORT", "invalid"},
	} {
		t.Run(tc.name+tc.value[:1], func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("HELM_AUTH_MODE", "disabled")
			t.Setenv("HELM_ADDR", "127.0.0.1:8080")
			t.Setenv(tc.name, tc.value)
			if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("invalid %s accepted or misclassified: %v", tc.name, err)
			}
		})
	}
}
