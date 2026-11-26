package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

const (
	agentAdmissionSessionHeader = "X-Helm-Agent-Session"
	agentAdmissionFenceHeader   = "X-Helm-Agent-Fence"
)

func agentAdmissionSession(r *http.Request, identity auth.Identity) []store.AgentAdmissionSession {
	if !identity.IsToken {
		return nil
	}
	return []store.AgentAdmissionSession{{
		TokenID:   identity.Token.Token.ID,
		SessionID: r.Header.Get(agentAdmissionSessionHeader),
		Fence:     r.Header.Get(agentAdmissionFenceHeader),
	}}
}

func (s *Server) agentAdmission(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectRef, action string) {
	if !identity.IsToken || identity.Actor.Kind != "agent" {
		s.writeError(w, http.StatusForbidden, "agent_token_required", "an agent bearer token is required", nil)
		return
	}
	if !requireScope(w, identity, "tasks:write") || !requireScope(w, identity, "tasks:claim") {
		return
	}
	project, err := s.Store.GetProject(r.Context(), projectRef)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if !identity.CanProject(project.ID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "project access is required", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	tokenID := identity.Token.Token.ID
	switch {
	case action == "admit" && r.Method == http.MethodPost:
		s.admitAgentWork(w, r, identity, project.ID, tokenID)
	case action == "admit" && r.Method == http.MethodGet:
		value, err := s.Store.CurrentAgentAdmission(r.Context(), project.ID, identity.Actor.ID, tokenID, r.URL.Query().Get("session_id"))
		s.writeAgentAdmission(w, identity, value, err)
	case action == "eligibility" && r.Method == http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
		value, err := s.Store.AgentAdmissionEligibility(r.Context(), project.ID, identity.Actor.ID, tokenID)
		if err != nil {
			s.writeStoreErrorForIdentity(w, identity, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"data": value})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) admitAgentWork(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectID, tokenID string) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		s.writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required", nil)
		return
	}
	if len(key) > 255 {
		s.writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long", nil)
		return
	}
	var input store.AgentAdmissionInput
	fields, err := decodeJSONObject(r, &input)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "session_id", "next_action", "finish_condition"); err != nil {
		writeTaskInputError(w, err)
		return
	}
	if err := rejectJSONNull(fields, "task_key", "title", "description", "priority", "lease_seconds"); err != nil {
		writeTaskInputError(w, err)
		return
	}
	if _, hasTaskKey := fields["task_key"]; hasTaskKey && strings.TrimSpace(input.TaskKey) == "" {
		writeTaskInputError(w, taskInputError("task_key must not be empty"))
		return
	}
	// Coordinate with generic mutation receipts in this process. The database
	// transaction also serializes admission attempts across processes.
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	receipt := store.AgentAdmissionReceipt{
		Key: idempotencyStoreKey(identity, key), RequestHash: idempotencyRequestHash(r, identity), Path: r.URL.Path,
	}
	_, found, err := s.Store.GetIdempotency(r.Context(), identity.Actor.ID, receipt.Key, r.Method, receipt.Path, receipt.RequestHash)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	s.metricsValue().recordAgentMutation("attempted")
	if !found && !s.admitMutationRate(w, r, identity) {
		return
	}
	value, err := s.Store.AdmitAgentWork(r.Context(), projectID, identity.Actor.ID, tokenID, input, receipt)
	if err != nil {
		s.metricsValue().recordAgentMutation("failed")
	} else {
		s.metricsValue().recordAgentMutation("succeeded")
	}
	s.writeAgentAdmission(w, identity, value, err)
}

func (s *Server) writeAgentAdmission(w http.ResponseWriter, identity auth.Identity, value store.AgentAdmission, err error) {
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	w.Header().Set("ETag", `"v`+strconv.FormatInt(value.Task.Version, 10)+`"`)
	s.writeJSON(w, http.StatusOK, map[string]any{"data": value})
}
