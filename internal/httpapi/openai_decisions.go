package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/decisions"
)

func (s *Server) requireDecisionsUser(w http.ResponseWriter, identity auth.Identity) bool {
	w.Header().Set("Cache-Control", "no-store")
	if identity.IsToken || identity.DevelopmentBypass || identity.Actor.Kind != "human" || !identity.IsWorkspaceMember() {
		s.writeError(w, http.StatusForbidden, "human_account_required", "Sign in to use a personal OpenAI key.", nil)
		return false
	}
	return true
}

func (s *Server) openAIConnection(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !s.requireDecisionsUser(w, identity) {
		return
	}

	actor := identity.Actor.ID
	var connected bool
	var err error
	switch r.Method {
	case http.MethodGet:
		connected, err = s.openAIDecisions.Connected(actor)
	case http.MethodPut:
		var input struct {
			APIKey *string `json:"api_key"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		fields, decodeErr := decodeJSONObject(r, &input)
		if decodeErr != nil || len(fields) != 1 || input.APIKey == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "Enter an OpenAI API key.", nil)
			return
		}
		err = s.openAIDecisions.Connect(r.Context(), actor, *input.APIKey)
		connected = err == nil
	case http.MethodDelete:
		err = s.openAIDecisions.Disconnect(actor)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if err != nil {
		s.writeDecisionsError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"connected": connected})
}

func (s *Server) taskTriageSuggestion(w http.ResponseWriter, r *http.Request, identity auth.Identity, ref string) {
	if !s.requireDecisionsUser(w, identity) || !requireScope(w, identity, "tasks:write") {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if r.ContentLength != 0 {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "Generate a suggestion from the saved task.", nil)
		return
	}
	version, err := parseVersion(r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), ref)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusNotFound, "not_found", "task not found", nil)
		return
	}
	if version != task.Version {
		s.writeError(w, http.StatusConflict, "stale_version", "The task changed. Refresh and suggest triage again.", nil)
		return
	}
	if !identity.Actor.Admin && task.ClaimedBy != nil && *task.ClaimedBy != identity.Actor.ID && task.ClaimExpiresAt != nil {
		expires, err := time.Parse(time.RFC3339Nano, *task.ClaimExpiresAt)
		if err != nil || time.Now().Before(expires) {
			s.writeError(w, http.StatusConflict, "claim_conflict", "This task is currently claimed by another actor.", nil)
			return
		}
	}

	suggestion, err := s.openAIDecisions.Suggest(r.Context(), identity.Actor.ID, task.Title, task.Description)
	if err != nil {
		s.writeDecisionsError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, struct {
		decisions.Suggestion
		TaskVersion int64 `json:"task_version"`
	}{Suggestion: suggestion, TaskVersion: task.Version})
}

func (s *Server) writeDecisionsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, decisions.ErrInvalidKey):
		s.writeError(w, http.StatusBadRequest, "openai_invalid_key", "Enter a valid OpenAI API key.", nil)
	case errors.Is(err, decisions.ErrKeyRejected):
		s.writeError(w, http.StatusUnprocessableEntity, "openai_key_rejected", "OpenAI rejected the API key. Check or replace it in Settings.", nil)
	case errors.Is(err, decisions.ErrAccessDenied):
		s.writeError(w, http.StatusUnprocessableEntity, "openai_access_denied", "This key cannot use OpenAI Decisions. Check its project and permissions.", nil)
	case errors.Is(err, decisions.ErrUsageLimit):
		s.writeError(w, http.StatusTooManyRequests, "openai_usage_limit", "OpenAI reached a usage limit. Check your project quota or try again later.", nil)
	case errors.Is(err, decisions.ErrBusy):
		s.writeError(w, http.StatusTooManyRequests, "openai_busy", "An OpenAI request is already in progress. Try again shortly.", nil)
	case errors.Is(err, decisions.ErrNotConnected):
		s.writeError(w, http.StatusConflict, "openai_not_connected", "Connect an OpenAI key in Settings to get a suggestion.", nil)
	case errors.Is(err, decisions.ErrInvalidAnswer):
		s.writeError(w, http.StatusBadGateway, "openai_invalid_answer", "OpenAI returned an unexpected answer. Try again.", nil)
	default:
		s.writeError(w, http.StatusServiceUnavailable, "openai_unavailable", "The OpenAI connection is unavailable. Try again shortly.", nil)
	}
}
