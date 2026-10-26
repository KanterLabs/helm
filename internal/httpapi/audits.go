package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func validHTTPAuditTerminalStatus(value string) bool {
	switch value {
	case "complete", "partial", "failed":
		return true
	default:
		return false
	}
}

func requireAuditReadScope(w http.ResponseWriter, identity auth.Identity) bool {
	// Findings contain task-derived reasons and evidence. Reuse the existing
	// task scope boundary so tokens cannot gain task context through audits.
	if identity.HasScope("tasks:read") {
		return true
	}
	sDummyWriteError(w, http.StatusForbidden, "insufficient_scope", "token lacks required scope", map[string]any{"required_scope": "tasks:read"})
	return false
}

func requireAuditWriteScope(w http.ResponseWriter, identity auth.Identity) bool {
	if identity.HasScope("tasks:write") {
		return true
	}
	sDummyWriteError(w, http.StatusForbidden, "insufficient_scope", "token lacks required scope", map[string]any{"required_scope": "tasks:write"})
	return false
}

// audits handles the collection surface. projectRoute is true for
// /projects/{project}/audits and false for the optional global /audits
// collection. The parent router can call this method directly.
func (s *Server) audits(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectReference string, projectRoute bool) {
	if r.Method == http.MethodPost {
		if !projectRoute {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "a project is required to create an audit run", nil)
			return
		}
		if !requireAuditWriteScope(w, identity) {
			return
		}
	} else if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	} else if !requireAuditReadScope(w, identity) {
		return
	}

	projectID := ""
	if projectRoute {
		project, err := s.Store.GetProject(r.Context(), projectReference)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !identity.CanProject(project.ID) {
			s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
			return
		}
		projectID = project.ID
	} else if reference, err := parseOptionalIdentifier(r, "project"); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	} else if reference != "" {
		project, err := s.Store.GetProject(r.Context(), reference)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !identity.CanProject(project.ID) {
			s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
			return
		}
		projectID = project.ID
	} else if identity.IsToken && identity.Token.ProjectsScoped {
		// A global aggregate is safe only when the store receives the token's
		// complete allow-list. This also permits a useful union for scoped agents
		// without exposing runs from another project.
		projectIDs := scopedProjectIDs(identity)
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		runs, more, err := s.Store.ListAuditRunsForProjects(r.Context(), projectIDs, limit, offset)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more {
			next = encodeCursor(offset + len(runs))
		}
		s.writeCollection(w, runs, next)
		return
	}
	if r.Method == http.MethodPost && s.idempotencyReplay(w, r, identity) {
		return
	}

	if r.Method == http.MethodGet {
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		var projectIDs []string
		if projectID != "" {
			projectIDs = []string{projectID}
		}
		runs, more, err := s.Store.ListAuditRunsForProjects(r.Context(), projectIDs, limit, offset)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more {
			next = encodeCursor(offset + len(runs))
		}
		s.writeCollection(w, runs, next)
		return
	}

	// projectID is resolved and authorized above before body validation, so a
	// malformed create cannot disclose whether an inaccessible project exists.
	var payload auditRunRequest
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "scope"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := rejectJSONNull(fields, "scope", "status"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Scope == nil {
		s.writeStoreError(w, &store.Error{Kind: store.ErrInvalid, Message: "scope is required"})
		return
	}
	input := store.AuditRunInput{Scope: *payload.Scope}
	if payload.Status != nil {
		input.Status = *payload.Status
	}
	s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
		run, err := s.Store.CreateAuditRun(r.Context(), projectID, identity.Actor.ID, input)
		if err != nil {
			return 0, nil, "", err
		}
		location := "/api/v1/audits/" + run.ID
		w.Header().Set("Location", location)
		body, err := json.Marshal(run)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusCreated, body, "", nil
	})
}

type auditRunRequest struct {
	Scope  *string `json:"scope"`
	Status *string `json:"status"`
}

// audit handles GET /audits/{audit}. Finalization is kept in auditFinalize so
// the parent router can map the explicit /finalize action without ambiguity.
func (s *Server) audit(w http.ResponseWriter, r *http.Request, identity auth.Identity, auditID string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireAuditReadScope(w, identity) {
		return
	}
	run, err := s.Store.GetAuditRun(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(run.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	s.writeJSON(w, http.StatusOK, run)
}

// auditFinalize handles POST /audits/{audit}/finalize. The only caller-owned
// value is the terminal outcome; all timestamps and lifecycle transitions are
// server-owned.
func (s *Server) auditFinalize(w http.ResponseWriter, r *http.Request, identity auth.Identity, auditID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireAuditWriteScope(w, identity) {
		return
	}
	run, err := s.Store.GetAuditRun(r.Context(), auditID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(run.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	var payload auditFinalizeRequest
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "status"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := rejectJSONNull(fields, "status"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Status == nil || !validHTTPAuditTerminalStatus(strings.TrimSpace(*payload.Status)) {
		s.writeStoreError(w, &store.Error{Kind: store.ErrInvalid, Message: "status must be complete, partial, or failed"})
		return
	}
	s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
		finalized, err := s.Store.FinalizeAuditRun(r.Context(), auditID, strings.TrimSpace(*payload.Status), identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(finalized)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusOK, body, "", nil
	})
}

type auditFinalizeRequest struct {
	Status *string `json:"status"`
}
