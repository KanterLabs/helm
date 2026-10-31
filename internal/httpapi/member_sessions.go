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

func (s *Server) memberPassword(w http.ResponseWriter, r *http.Request, identity auth.Identity, targetID string) {
	if !s.rejectAuthIdempotencyKey(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if identity.IsToken || identity.Actor.Kind != "human" {
		s.writeError(w, http.StatusForbidden, "human_account_required", "local passwords can only be managed by a signed-in human", nil)
		return
	}
	if targetID != "" && !requireAdmin(w, identity) {
		return
	}
	if targetID == "" {
		targetID = identity.Actor.ID
	}
	var payload struct {
		CurrentPassword *string `json:"current_password"`
		Password        *string `json:"password"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	verifiedHash := ""
	if targetID == identity.Actor.ID {
		if err := requireJSONFields(fields, "current_password", "password"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if payload.CurrentPassword == nil || payload.Password == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "current_password and password are required", nil)
			return
		}
		_, hash, hashErr := s.Store.GetPasswordHash(r.Context(), identity.Actor.EmailValue())
		if hashErr != nil || hash == "" || !auth.CheckPassword(*payload.CurrentPassword, hash) {
			// Password failures deliberately do not disclose whether a local
			// password is configured.
			s.writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is invalid", nil)
			return
		}
		verifiedHash = hash
	} else if err := requireJSONFields(fields, "password"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Password == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "password is required", nil)
		return
	}
	if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Password)), "password", 12, 0, false); err != nil {
		s.writeStoreError(w, err)
		return
	}
	hash, err := auth.HashPassword(*payload.Password)
	if err != nil {
		s.writeStoreError(w, &store.Error{Kind: store.ErrInvalid, Message: err.Error()})
		return
	}
	keepSessionID := ""
	if targetID == identity.Actor.ID {
		keepSessionID = identity.SessionID
	}
	if err := s.Store.SetMemberPasswordExcept(r.Context(), targetID, hash, identity.Actor.ID, keepSessionID, verifiedHash); err != nil {
		if targetID == identity.Actor.ID && errors.Is(err, store.ErrForbidden) {
			s.writeError(w, 401, "invalid_credentials", "current password is invalid", nil)
			return
		}
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request, identity auth.Identity, sessionID string) {
	if identity.IsToken || identity.Actor.Kind != "human" {
		s.writeError(w, http.StatusForbidden, "human_account_required", "local sessions can only be managed by a signed-in human", nil)
		return
	}
	if r.Method == http.MethodGet && sessionID == "" {
		actorID := identity.Actor.ID
		if requested := strings.TrimSpace(r.URL.Query().Get("actor_id")); requested != "" {
			if !identity.IsAdmin() {
				s.writeError(w, http.StatusForbidden, "forbidden", "administrator access is required", nil)
				return
			}
			actorID = requested
			if !s.requireAdmittedHumanSessionTarget(w, r, actorID) {
				return
			}
		}
		items, err := s.Store.ListSessions(r.Context(), actorID, true)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		s.writeCollection(w, items, "")
		return
	}
	if r.Method != http.MethodDelete || sessionID == "" {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	actorID := identity.Actor.ID
	if requested := strings.TrimSpace(r.URL.Query().Get("actor_id")); requested != "" {
		if !identity.IsAdmin() {
			s.writeError(w, http.StatusForbidden, "forbidden", "administrator access is required", nil)
			return
		}
		actorID = requested
		if !s.requireAdmittedHumanSessionTarget(w, r, actorID) {
			return
		}
	}
	if err := s.Store.RevokeSession(r.Context(), actorID, sessionID, identity.Actor.ID); err != nil {
		s.writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireAdmittedHumanSessionTarget keeps the administrator session surface
// scoped to explicit human memberships. Agent actors and Cloudflare identities
// that have not yet been reviewed may have rows in the shared sessions table,
// but they are not local human sessions and must not be exposed here.
func (s *Server) requireAdmittedHumanSessionTarget(w http.ResponseWriter, r *http.Request, actorID string) bool {
	actor, err := s.Store.GetActor(r.Context(), actorID)
	if err != nil {
		s.writeStoreError(w, err)
		return false
	}
	if actor.Kind != "human" || actor.WorkspaceRole == "" {
		s.writeError(w, http.StatusNotFound, "not_found", "member not found", nil)
		return false
	}
	return true
}
