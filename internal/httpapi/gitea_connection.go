package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/forgeconnect"
)

// Gitea / Forgejo connection routes (contract C5 in
// docs/GITEA_OAUTH_PLAN.md), all for signed-in human administrators:
//
//	GET  /api/v1/integrations/gitea                  status document
//	POST /api/v1/integrations/gitea/probe            find the forge; saves nothing
//	POST /api/v1/integrations/gitea/oauth/start      begin sign-in: {authorize_url}
//	GET  /api/v1/integrations/gitea/oauth/callback   browser return from the forge: 303 to Settings
//	POST /api/v1/integrations/gitea/check            test the connection: status document
//	GET  /api/v1/integrations/gitea/repositories     what the account can read
//	POST /api/v1/integrations/gitea/disconnect       delete the local state file
//
// None is wrapped in s.mutation: nothing is stored for idempotent replay, and
// every answer is no-store. Errors carry only a forgeconnect code and a fixed
// message of ours; upstream text never reaches a response, a redirect or a log.
const (
	giteaCallbackRoute       = "/api/v1/integrations/gitea/oauth/callback"
	giteaProbeTimeout        = 15 * time.Second
	giteaRepositoriesTimeout = 30 * time.Second
	giteaCallbackTimeout     = 30 * time.Second
)

func (s *Server) dispatchGitea(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) bool {
	if len(parts) < 2 || parts[0] != "integrations" || parts[1] != "gitea" {
		return false
	}
	s.giteaConnection(w, r, identity, strings.Join(parts[2:], "/"))
	return true
}

func (s *Server) giteaConnection(w http.ResponseWriter, r *http.Request, identity auth.Identity, route string) {
	w.Header().Set("Cache-Control", "no-store")
	callback := route == "oauth/callback" && r.Method == http.MethodGet
	if identity.IsToken || identity.Actor.Kind != "human" {
		s.writeError(w, http.StatusForbidden, "human_account_required", "The Gitea connection can only be managed by a signed-in human", nil)
		return
	}
	if !identity.IsAdmin() {
		if callback {
			giteaRedirect(w, r, forgeconnect.CodeStateInvalid)
			return
		}
		s.writeError(w, http.StatusForbidden, "forbidden", "administrator access is required", nil)
		return
	}
	m, actor := s.Gitea, identity.Actor.ID
	switch {
	case route == "" && r.Method == http.MethodGet:
		s.writeJSON(w, http.StatusOK, giteaStatusDocument(m))
	case route == "probe" && r.Method == http.MethodPost:
		s.giteaProbe(w, r, m)
	case route == "oauth/start" && r.Method == http.MethodPost:
		s.giteaStart(w, r, m, actor)
	case callback:
		s.giteaCallback(w, r, m, actor)
	case route == "check" && r.Method == http.MethodPost:
		if m == nil {
			s.writeGiteaError(w, errGiteaUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), giteaProbeTimeout)
		defer cancel()
		s.writeJSON(w, http.StatusOK, newGiteaStatusBody(m.Check(ctx)))
	case route == "repositories" && r.Method == http.MethodGet:
		s.giteaRepositories(w, r, m)
	case route == "disconnect" && r.Method == http.MethodPost:
		if m == nil {
			s.writeGiteaError(w, errGiteaUnavailable)
			return
		}
		if err := m.Disconnect(actor); err != nil {
			s.writeGiteaError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, giteaStatusDocument(m))
	default:
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

var errGiteaUnavailable = &forgeconnect.Error{Code: forgeconnect.CodeUnavailable}

// signInManager returns the manager only when sign-in can be offered: it is
// enabled and Helm has a public origin for the forge to return to.
func (s *Server) signInManager(w http.ResponseWriter, m *forgeconnect.Manager) *forgeconnect.Manager {
	if m == nil || !m.Status().Available {
		s.writeGiteaError(w, errGiteaUnavailable)
		return nil
	}
	return m
}

func (s *Server) giteaProbe(w http.ResponseWriter, r *http.Request, m *forgeconnect.Manager) {
	var in struct {
		BaseURL           *string `json:"base_url"`
		AllowInsecureHTTP *bool   `json:"allow_insecure_http"`
	}
	if _, err := decodeJSONObject(r, &in); err != nil || in.BaseURL == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be {base_url, allow_insecure_http}", nil)
		return
	}
	if m = s.signInManager(w, m); m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), giteaProbeTimeout)
	defer cancel()
	server, err := m.Probe(ctx, *in.BaseURL, in.AllowInsecureHTTP != nil && *in.AllowInsecureHTTP)
	if err != nil {
		s.writeGiteaError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"server": server, "redirect_uri": m.Status().RedirectURI, "register_url": forgeconnect.RegisterURL(server.BaseURL),
	})
}

func (s *Server) giteaStart(w http.ResponseWriter, r *http.Request, m *forgeconnect.Manager, actor string) {
	var in struct {
		BaseURL           *string `json:"base_url"`
		ClientID          *string `json:"client_id"`
		AllowInsecureHTTP *bool   `json:"allow_insecure_http"`
	}
	if _, err := decodeJSONObject(r, &in); err != nil || in.BaseURL == nil || in.ClientID == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be {base_url, client_id, allow_insecure_http}", nil)
		return
	}
	if m = s.signInManager(w, m); m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), giteaProbeTimeout)
	defer cancel()
	authorize, err := m.Begin(ctx, actor, forgeconnect.BeginInput{
		BaseURL: *in.BaseURL, ClientID: *in.ClientID, AllowInsecureHTTP: in.AllowInsecureHTTP != nil && *in.AllowInsecureHTTP,
	})
	if err != nil {
		s.writeGiteaError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"authorize_url": authorize})
}

// giteaCallback finishes sign-in for the administrator who started it and
// returns the browser to Settings with only a forgeconnect code.
func (s *Server) giteaCallback(w http.ResponseWriter, r *http.Request, m *forgeconnect.Manager, actor string) {
	code := forgeconnect.CodeUnavailable
	if m != nil {
		query := r.URL.Query()
		ctx, cancel := context.WithTimeout(r.Context(), giteaCallbackTimeout)
		defer cancel()
		code = forgeconnect.Code(m.Finish(ctx, actor, query.Get("state"), query.Get("code"), query.Get("error"), query.Get("error_description")))
	}
	giteaRedirect(w, r, code)
}

// giteaRedirect sends the browser to Settings. An empty code means connected;
// anything that is not a known forgeconnect code becomes exchange_failed.
func giteaRedirect(w http.ResponseWriter, r *http.Request, code string) {
	target := "/settings?gitea=connected"
	if code != "" {
		if _, known := giteaErrors[code]; !known {
			code = forgeconnect.CodeExchangeFailed
		}
		target = "/settings?" + url.Values{"gitea_error": {code}}.Encode()
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) giteaRepositories(w http.ResponseWriter, r *http.Request, m *forgeconnect.Manager) {
	if m == nil || m.Status().UnavailableReason == "disabled" {
		s.writeGiteaError(w, errGiteaUnavailable)
		return
	}
	if m.Status().State == forgeconnect.StateDisconnected {
		s.writeError(w, http.StatusConflict, forgeconnect.CodeUnavailable, "Helm is not connected to a Gitea or Forgejo server.", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), giteaRepositoriesTimeout)
	defer cancel()
	list, truncated, err := m.Repositories(ctx)
	if err != nil {
		s.writeGiteaError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"repositories": list, "truncated": truncated})
}

// isGiteaCallback reports the browser return from the forge. Without a Helm
// session it is answered with a redirect, never raw JSON (see prepareRequest).
func isGiteaCallback(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.TrimSuffix(r.URL.Path, "/") == giteaCallbackRoute
}

type giteaError struct {
	status  int
	message string
}

// giteaErrors maps every forgeconnect code to a status and a fixed message.
var giteaErrors = map[string]giteaError{
	forgeconnect.CodeUnavailable:        {http.StatusServiceUnavailable, "The Gitea connection is not available on this Helm."},
	forgeconnect.CodeInvalidServerURL:   {http.StatusBadRequest, "That is not a valid server address."},
	forgeconnect.CodeInsecureHTTP:       {http.StatusBadRequest, "The address uses http; allow an unencrypted connection to use it."},
	forgeconnect.CodeServerUnreachable:  {http.StatusBadGateway, "Helm could not reach the server."},
	forgeconnect.CodeTLSError:           {http.StatusBadGateway, "The server's certificate is not trusted."},
	forgeconnect.CodeNotAForge:          {http.StatusBadRequest, "No Gitea or Forgejo sign-in was found at that address."},
	forgeconnect.CodeIssuerMismatch:     {http.StatusBadRequest, "The server says it lives at another address."},
	forgeconnect.CodeAlreadyConnected:   {http.StatusConflict, "Helm is already connected; disconnect first."},
	forgeconnect.CodeStateInvalid:       {http.StatusBadRequest, "The sign-in expired or was started elsewhere; start again."},
	forgeconnect.CodeDenied:             {http.StatusForbidden, "Sign-in was cancelled on the server."},
	forgeconnect.CodeScopeConflict:      {http.StatusConflict, "An earlier authorization with other permissions must be removed first."},
	forgeconnect.CodeAuthorizeFailed:    {http.StatusBadGateway, "The server did not complete the sign-in."},
	forgeconnect.CodeClientConfidential: {http.StatusBadRequest, "The application must be a public client (Confidential Client unticked)."},
	forgeconnect.CodeClientUnknown:      {http.StatusBadRequest, "The server does not recognise that client ID."},
	forgeconnect.CodeExchangeFailed:     {http.StatusBadGateway, "The server did not accept the sign-in."},
	forgeconnect.CodeStorageFailed:      {http.StatusInternalServerError, "Helm could not save or remove the connection."},
	forgeconnect.CodeReconnectRequired:  {http.StatusConflict, "Sign in again to keep using this connection."},
}

func giteaErrorStatus(code string) (int, string) {
	if known, ok := giteaErrors[code]; ok {
		return known.status, known.message
	}
	return http.StatusInternalServerError, "The Gitea connection failed unexpectedly."
}

func (s *Server) writeGiteaError(w http.ResponseWriter, err error) {
	code := forgeconnect.Code(err)
	status, message := giteaErrorStatus(code)
	details := map[string]any{}
	var failure *forgeconnect.Error
	if errors.As(err, &failure) && failure.Code == forgeconnect.CodeIssuerMismatch && failure.Issuer != "" {
		details["issuer"] = failure.Issuer
	}
	if _, known := giteaErrors[code]; !known {
		code = "internal_error"
	}
	s.writeError(w, status, code, message, details)
}
