package httpapi

import (
	"net/http"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

type intakeCollection struct {
	Data       []store.Task       `json:"data"`
	NextCursor string             `json:"next_cursor"`
	Counts     store.IntakeCounts `json:"counts"`
	Queue      *ticketQueueRef    `json:"queue,omitempty"`
}

var intakeScopes = map[string]struct{}{"needs_triage": {}, "all": {}}
var intakeSeverityFilters = map[string]struct{}{"s1": {}, "s2": {}, "s3": {}, "s4": {}, "untriaged": {}, "none": {}}

// intake is an additive reader: legacy ticket queues and bug lifecycle routes
// keep their contracts. scopedProjectIDs contains the live actor/token ceiling.
func (s *Server) intake(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	limit, err := parseLimit(r, 50)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	filter := store.IntakeFilter{ProjectIDs: scopedProjectIDs(identity), Limit: limit, Reader: identity.Actor.ID}
	if identity.IsToken {
		filter.Reader += "/" + identity.Token.Token.ID
	}
	value, present, cursorErr := queryValue(r, "cursor")
	if cursorErr != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", cursorErr.Error(), nil)
		return
	}
	if present && (value == "" || len(value) > 4096) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "cursor must contain 1 to 4096 bytes", nil)
		return
	}
	filter.Cursor = value
	if filter.Scope, err = parseOptionalEnum(r, "scope", intakeScopes); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if filter.Kind, err = parseOptionalEnum(r, "kind", taskKinds); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if filter.Severity, err = parseOptionalEnum(r, "severity", intakeSeverityFilters); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if filter.Query, err = parseOptionalSearch(r); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if reference, err := parseOptionalIdentifier(r, "project"); err != nil {
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
		filter.ProjectIDs = []string{project.ID}
	}
	page, err := s.Store.ReadIntake(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	result := intakeCollection{Data: page.Data, Counts: page.Counts, NextCursor: page.NextCursor}
	queue, found, err := s.Store.LookupTicketQueue(r.Context())
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if found && identity.CanProject(queue.ID) {
		result.Queue = &ticketQueueRef{ProjectID: queue.ID, Key: queue.Key}
	}
	s.writeJSON(w, http.StatusOK, result)
}
