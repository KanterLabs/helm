package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KanterLabs/helm/internal/config"
)

func TestCloudflareHostAudienceBinding(t *testing.T) {
	bindings := map[string][]string{
		"old.example": {"old-ui", "old-api"},
		"new.example": {"new-ui", "new-api"},
	}
	for _, tt := range []struct {
		name, host string
		audiences  []string
		want       bool
	}{
		{"old UI", "old.example", []string{"old-ui"}, true},
		{"new API", "new.example", []string{"new-api"}, true},
		{"cross host", "new.example", []string{"old-ui"}, false},
		{"unknown host", "evil.example", []string{"new-ui"}, false},
		{"explicit port", "new.example:443", []string{"new-ui"}, false},
		{"missing verified audience", "new.example", nil, false},
		{"unrelated audience", "new.example", []string{"beta-ui"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := cloudflareHostAudienceAllowed(bindings, tt.host, tt.audiences); got != tt.want {
				t.Fatalf("allowed = %v, want %v", got, tt.want)
			}
		})
	}
	if !cloudflareHostAudienceAllowed(nil, "existing.example", nil) {
		t.Fatal("absent opt-in changed existing behavior")
	}
}

func TestManagerRejectsCrossHostIdentityBeforeCreatingActor(t *testing.T) {
	data := testStore(t)
	bindings := map[string][]string{"new.example": {"new-ui"}, "old.example": {"old-ui"}}
	manager := NewManagerWithVerifier(data, config.Config{
		AuthMode: "cloudflare", CloudflareHostAudiences: bindings,
	}, staticVerifier{claims: CloudflareClaims{Email: "owner@example.com", Audiences: []string{"old-ui"}}})
	// Mutating the caller's configuration cannot widen the manager policy.
	bindings["new.example"][0] = "old-ui"
	request := httptest.NewRequest(http.MethodGet, "https://new.example/api/v1/auth/me", nil)
	request.Header.Set("Cf-Access-Jwt-Assertion", "verified-by-fixture")
	request.Header.Set("X-Forwarded-Host", "old.example")
	request.Header.Set("Forwarded", "host=old.example;proto=https")
	if _, err := manager.Authenticate(context.Background(), request); err == nil {
		t.Fatal("cross-host assertion was accepted")
	}
	count, err := data.CountHumanActors(context.Background())
	if err != nil || count != 0 {
		t.Fatalf("rejected identity created actor: count=%d err=%v", count, err)
	}
	request.Host = "old.example"
	if _, err := manager.Authenticate(context.Background(), request); err != nil {
		t.Fatalf("correct host rejected: %v", err)
	}
}
