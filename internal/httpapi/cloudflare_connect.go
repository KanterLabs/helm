package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/publicendpoint"
)

// Guided setup ("Connect Cloudflare") routes, all for human administrators:
//
//	GET    /api/v1/cloudflare                  status, token link, sign-in availability
//	POST   /api/v1/cloudflare/session          {api_token}: connect with a pasted token
//	DELETE /api/v1/cloudflare/session          forget (and revoke) the connection
//	POST   /api/v1/cloudflare/oauth/start      begin "Sign in with Cloudflare"
//	GET    /api/v1/cloudflare/oauth/callback   browser return from the relay
//	GET    /api/v1/cloudflare/zones            domains the connection can set up
//	POST   /api/v1/cloudflare/setup            start the checklist
//	GET    /api/v1/cloudflare/setup/{run}      checklist progress
func (s *Server) cloudflareConnect(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) {
	w.Header().Set("Cache-Control", "no-store")
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	manager, actor := s.PublicEndpoints, identity.Actor.ID
	route := strings.Join(parts[1:], "/")
	switch {
	case route == "" && r.Method == http.MethodGet:
		s.writeJSON(w, http.StatusOK, manager.ConnectStatus(actor))
	case route == "session" && r.Method == http.MethodPost:
		var payload struct {
			APIToken string `json:"api_token"`
		}
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {api_token}", nil)
			return
		}
		if err := manager.ConnectToken(r.Context(), actor, payload.APIToken); err != nil {
			s.writeConnectError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, manager.ConnectStatus(actor))
	case route == "session" && r.Method == http.MethodDelete:
		manager.Disconnect(r.Context(), actor)
		s.writeJSON(w, http.StatusOK, manager.ConnectStatus(actor))
	case route == "oauth/start" && r.Method == http.MethodPost:
		authorize, err := manager.StartOAuth(actor)
		if err != nil {
			s.writeConnectError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]string{"authorize_url": authorize})
	case route == "oauth/callback" && r.Method == http.MethodGet:
		s.cloudflareOAuthCallback(w, r, manager, actor)
	case route == "zones" && r.Method == http.MethodGet:
		zones, err := manager.CloudflareZones(r.Context(), actor)
		if err != nil {
			s.writeConnectError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"data": zones})
	case route == "setup" && r.Method == http.MethodPost:
		var payload struct {
			Zone     string `json:"zone"`
			Hostname string `json:"hostname"`
			Email    *struct {
				LocalPart           string `json:"local_part"`
				FallbackAddress     string `json:"fallback_address"`
				EnableSubaddressing bool   `json:"enable_subaddressing"`
				Project             string `json:"project"` // ignored: tickets land in the ticket queue
				InboxName           string `json:"inbox_name"`
				AssignToMe          bool   `json:"assign_to_me"`
			} `json:"email"`
		}
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {zone, hostname?, email?}", nil)
			return
		}
		request := publicendpoint.SetupRequest{ZoneID: payload.Zone, Hostname: payload.Hostname}
		if payload.Email != nil {
			request.Email = &publicendpoint.SetupEmail{LocalPart: payload.Email.LocalPart, FallbackAddress: payload.Email.FallbackAddress, EnableSubaddressing: payload.Email.EnableSubaddressing, InboxName: payload.Email.InboxName}
			if payload.Email.AssignToMe {
				request.Email.AssigneeID = actor
			}
		}
		run, err := manager.StartSetup(r.Context(), actor, request)
		if err != nil {
			s.writeConnectError(w, err)
			return
		}
		s.writeJSON(w, http.StatusAccepted, run)
	case len(parts) == 3 && parts[1] == "setup" && r.Method == http.MethodGet:
		run, ok := manager.SetupRun(actor, parts[2])
		if !ok {
			s.writeError(w, http.StatusNotFound, "not_found", "setup run not found", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, run)
	default:
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

func (s *Server) writeConnectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publicendpoint.ErrNotConnected):
		s.writeError(w, http.StatusConflict, "cloudflare_not_connected", "Connect Cloudflare first: sign in or paste a token.", nil)
	case errors.Is(err, publicendpoint.ErrOAuthUnavailable):
		s.writeError(w, http.StatusNotFound, "cloudflare_oauth_unavailable", err.Error(), nil)
	case errors.Is(err, publicendpoint.ErrSetupRunning):
		s.writeError(w, http.StatusConflict, "setup_running", "A guided setup is already running; wait for it to finish.", nil)
	case errors.Is(err, publicendpoint.ErrInvalidHostname):
		s.writeError(w, http.StatusBadRequest, "invalid_hostname", err.Error(), map[string]any{"field": "hostname"})
	case errors.Is(err, publicendpoint.ErrCloudflare):
		s.writeError(w, http.StatusBadRequest, "cloudflare_error", strings.TrimPrefix(err.Error(), publicendpoint.ErrCloudflare.Error()+": "), nil)
	default:
		s.writePublicEndpointError(w, err)
	}
}

// cloudflareOAuthCallback finishes sign-in, then returns the browser to
// Connect apps with the outcome in the query string (never the token).
func (s *Server) cloudflareOAuthCallback(w http.ResponseWriter, r *http.Request, manager *publicendpoint.Manager, actor string) {
	query := r.URL.Query()
	target := url.Values{"connect": {"1"}}
	if denied := query.Get("error"); denied != "" {
		message := query.Get("error_description")
		if message == "" {
			message = denied
		}
		target.Set("cloudflare_error", "Cloudflare sign-in was not completed: "+message)
	} else if err := manager.FinishOAuth(r.Context(), actor, query.Get("state"), query.Get("code")); err != nil {
		target.Set("cloudflare_error", strings.TrimPrefix(err.Error(), publicendpoint.ErrCloudflare.Error()+": "))
	} else {
		target.Set("cloudflare", "connected")
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/tickets?"+target.Encode(), http.StatusSeeOther)
}
