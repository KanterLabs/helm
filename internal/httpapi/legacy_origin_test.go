package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// Exercise the populated store and real router: compatibility must not bypass
// token identity/scopes or turn a stale browser into an authenticated caller.
func TestLegacyOriginTransition(t *testing.T) {
	s, data := testServer(t, "disabled")
	if _, err := data.EnsureDisabledActor(context.Background()); err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("MOVE"), Name: stringPtr("Move")}, "actor-disabled-mode")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := data.CreateAgent(context.Background(), store.Actor{Kind: "agent", Name: "Migration agent", ProjectIDs: []string{project.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateTokenBy(context.Background(), agent.ID, "actor-disabled-mode", "migration", []string{"tasks:read"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Cfg.PublicOrigin = "https://helm.home.test"
	s.Cfg.LegacyOrigin = "https://tc.test"
	cases := []struct {
		name, method, path, authorization string
		status                            int
		redirect                          bool
	}{
		{"browser navigation", http.MethodGet, "/projects?view=board", "", http.StatusTemporaryRedirect, true},
		{"stale browser write", http.MethodPost, "/api/v1/projects", "", http.StatusConflict, false},
		{"browser API read", http.MethodGet, "/api/v1/projects", "", http.StatusConflict, false},
		{"invalid token", http.MethodGet, "/api/v1/auth/me", "Bearer invalid", http.StatusUnauthorized, false},
		{"existing identity", http.MethodGet, "/api/v1/auth/me", "Bearer " + token, http.StatusOK, false},
		{"scope preserved", http.MethodPost, "/api/v1/projects", "Bearer " + token, http.StatusForbidden, false},
		{"no legacy login", http.MethodPost, "/api/v1/auth/login", "Bearer " + token, http.StatusConflict, false},
		{"no credential redirect", http.MethodGet, "/projects", "Bearer " + token, http.StatusConflict, false},
		{"legacy health", http.MethodGet, "/readyz", "", http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{"Authorization": tc.authorization, "Cookie": "helm_session=forged", "X-Helm-Tailnet-Assertion": "forged", "Cf-Access-Jwt-Assertion": "forged"}
			r := request(t, s, tc.method, "https://tc.test"+tc.path, nil, headers)
			if r.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", r.Code, tc.status, r.Body.String())
			}
			location := r.Header().Get("Location")
			if tc.redirect {
				if location != "https://helm.home.test"+tc.path {
					t.Fatalf("location=%q", location)
				}
			} else if location != "" {
				t.Fatalf("unexpected redirect=%q", location)
			}
			if r.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("legacy result must not be cached")
			}
			if tc.name == "existing identity" && !strings.Contains(r.Body.String(), agent.ID) {
				t.Fatal("actor identity changed")
			}
		})
	}
	r := request(t, s, http.MethodGet, "https://helm.home.test/healthz", nil, nil)
	if r.Code != http.StatusOK || r.Header().Get("X-Helm-Legacy") != "" {
		t.Fatalf("canonical health changed: %d", r.Code)
	}
}
