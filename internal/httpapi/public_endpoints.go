package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/publicendpoint"
)

// NewPublicHooksHandler serves the loopback listener cloudflared forwards
// to. It admits only webhook POSTs and a health probe, so even a
// misconfigured tunnel cannot reach the dashboard or the rest of the API.
func NewPublicHooksHandler(api *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		// Self-test nonces are answered only here, never on the main origin,
		// so a successful probe proves the request came through the tunnel.
		if nonce, ok := strings.CutPrefix(r.URL.Path, publicendpoint.ProbePath); ok && r.Method == http.MethodGet && api.PublicEndpoints.AnswerProbe(nonce) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"probe":"` + nonce + `"}`))
			return
		}
		if isAPIPath(r.URL.Path) {
			parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
			if isTicketHookPath(parts) || isCoolifyIntakePath(parts) || isEmailHookPath(parts) {
				api.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		api.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
	})
}

type publicEndpointRequest struct {
	Provider string `json:"provider"`
	Hostname string `json:"hostname"`
	APIToken string `json:"api_token"`
}

func (s *Server) publicEndpointsUnavailable(w http.ResponseWriter) bool {
	if s.PublicEndpoints != nil {
		return false
	}
	s.writeError(w, http.StatusNotFound, "public_endpoints_disabled", "public endpoints are disabled on this server (HELM_PUBLIC_HOOKS_ADDR=off)", nil)
	return true
}

func (s *Server) writePublicEndpointError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publicendpoint.ErrInvalidHostname):
		s.writeError(w, http.StatusBadRequest, "invalid_hostname", err.Error(), map[string]any{"field": "hostname"})
	case errors.Is(err, publicendpoint.ErrEmailPrerequisite):
		s.writeError(w, http.StatusConflict, "email_prerequisite", strings.TrimPrefix(err.Error(), publicendpoint.ErrEmailPrerequisite.Error()+": "), nil)
	case errors.Is(err, publicendpoint.ErrCloudflare):
		s.writeError(w, http.StatusBadRequest, "cloudflare_error", err.Error(), nil)
	default:
		s.writeStoreError(w, err)
	}
}

// publicEndpoints serves GET/POST /api/v1/public-endpoints (human admins).
func (s *Server) publicEndpoints(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := s.PublicEndpoints.View(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, view)
	case http.MethodPost:
		// The request carries a Cloudflare API token; never replay or cache.
		if !s.secretResponseAllowed(w, r) {
			return
		}
		var payload publicEndpointRequest
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {provider, hostname, api_token}", nil)
			return
		}
		if payload.Provider != "" && payload.Provider != "cloudflare" {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "provider must be cloudflare", map[string]any{"field": "provider"})
			return
		}
		if _, err := s.PublicEndpoints.Provision(r.Context(), payload.APIToken, payload.Hostname, identity.Actor.ID); err != nil {
			s.writePublicEndpointError(w, err)
			return
		}
		view, err := s.PublicEndpoints.View(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, view)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// testPublicEndpoint serves POST /api/v1/public-endpoints/{id}/test: one
// round trip from Helm through its public hostname back to the hooks listener.
func (s *Server) testPublicEndpoint(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	result, err := s.PublicEndpoints.Test(r.Context(), id)
	if errors.Is(err, publicendpoint.ErrNoActiveEndpoint) {
		s.writeError(w, http.StatusConflict, "public_endpoint_inactive", "only the active public URL can be tested", nil)
		return
	}
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// publicEndpoint serves DELETE /api/v1/public-endpoints/{id}. An optional
// {api_token} also deletes the Cloudflare DNS record and tunnel.
func (s *Server) publicEndpoint(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !s.secretResponseAllowed(w, r) {
		return
	}
	var payload publicEndpointRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be empty or {api_token}", nil)
			return
		}
	}
	endpoint, err := s.PublicEndpoints.Disable(r.Context(), id, payload.APIToken, identity.Actor.ID)
	if err != nil && endpoint.ID == "" {
		s.writePublicEndpointError(w, err)
		return
	}
	response := map[string]any{"endpoint": endpoint}
	if err != nil {
		response["warning"] = "Stopped locally, but Cloudflare cleanup failed: " + err.Error()
	} else if endpoint.CleanupPending {
		response["warning"] = "Stopped locally. Delete DNS record " + endpoint.DNSRecordID + " and tunnel " + endpoint.TunnelID + " in Cloudflare, or disable again with an API token."
	}
	s.writeJSON(w, http.StatusOK, response)
}
