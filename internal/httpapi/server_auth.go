package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.Auth.Status(r.Context())
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	status["authenticated"] = false
	status["actor"] = nil
	status["user"] = nil
	identity, authenticated := requestIdentity(r)
	if !authenticated && strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		if authenticatedIdentity, authErr, admitted := s.authenticateRequest(w, r); !admitted {
			return
		} else if authErr == nil {
			identity, authenticated = authenticatedIdentity, true
		}
	}
	if authenticated {
		status["authenticated"] = true
		status["actor"] = identity.Actor
		status["user"] = identity.Actor
		// The UI and API use separate Cloudflare Access applications. A normal
		// browser navigation to this endpoint lets Access issue its API-path
		// authorization cookie, then returns the user to the SPA. API clients and
		// fetch requests explicitly ask for JSON and retain the status response.
		if s.Cfg.AuthMode == "cloudflare" && !identity.IsToken && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	s.writeJSON(w, http.StatusOK, status)
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if !s.validMutationOrigin(r) {
		s.writeError(w, http.StatusForbidden, "csrf_origin", "request origin is not allowed", nil)
		return
	}
	if !s.rejectAuthIdempotencyKey(w, r) {
		return
	}
	var input struct {
		Email    *string `json:"email"`
		Name     *string `json:"name"`
		Password *string `json:"password"`
	}
	fields, err := decodeJSONObject(r, &input)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "email", "password"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if input.Email == nil || input.Password == nil {
		// requireJSONFields handles the explicit-null case; keep this guard
		// defensive if a future decoder changes pointer behavior.
		s.writeError(w, http.StatusBadRequest, "invalid_request", "email and password are required", nil)
		return
	}
	if !auth.ValidEmail(*input.Email) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "a valid email is required", nil)
		return
	}
	if err := validateStringLength(json.RawMessage(strconv.Quote(*input.Password)), "password", 12, 0, false); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if raw, present := fields["name"]; present {
		if err := validateStringLength(raw, "name", 1, 200, true); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	name := ""
	if input.Name != nil {
		name = *input.Name
	}
	actor, err := s.Auth.Setup(r.Context(), *input.Email, name, *input.Password)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, actor)
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if !s.validMutationOrigin(r) {
		s.writeError(w, http.StatusForbidden, "csrf_origin", "request origin is not allowed", nil)
		return
	}
	if !s.rejectAuthIdempotencyKey(w, r) {
		return
	}
	var input struct {
		Email    *string `json:"email"`
		Password *string `json:"password"`
	}
	fields, err := decodeJSONObject(r, &input)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "email", "password"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if input.Email == nil || input.Password == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "email and password are required", nil)
		return
	}
	if !auth.ValidEmail(*input.Email) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "a valid email is required", nil)
		return
	}
	if err := validateStringLength(json.RawMessage(strconv.Quote(*input.Password)), "password", 1, 0, false); err != nil {
		s.writeStoreError(w, err)
		return
	}
	actor, err := s.Auth.LoginRequest(r.Context(), *input.Email, *input.Password, r, w)
	if err != nil {
		if errors.Is(err, store.ErrForbidden) {
			s.writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password", nil)
			return
		}
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, actor)
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if !s.validMutationOrigin(r) {
		s.writeError(w, http.StatusForbidden, "csrf_origin", "request origin is not allowed", nil)
		return
	}
	if !s.rejectAuthIdempotencyKey(w, r) {
		return
	}
	if err := s.Auth.Logout(r.Context(), r, w); err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
