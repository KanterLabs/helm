package httpapi

import (
	"net/http"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

var ticketQueues = func() map[string]struct{} {
	values := make(map[string]struct{}, len(store.TicketQueues))
	for _, queue := range store.TicketQueues {
		values[queue] = struct{}{}
	}
	return values
}()

type ticketCollection struct {
	Data       []store.Task       `json:"data"`
	NextCursor string             `json:"next_cursor"`
	Counts     store.TicketCounts `json:"counts"`
}

// tickets serves GET /api/v1/tickets: one queue page plus server counts for
// every queue over the same project/search scope.
func (s *Server) tickets(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	var projectIDs []string
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
		projectIDs = []string{project.ID}
	} else if identity.IsToken && identity.Token.ProjectsScoped {
		// Matches /my-work: a global aggregate would reveal ticket metadata
		// across every project a scoped token may read.
		s.writeError(w, http.StatusForbidden, "forbidden", "a project is required for a scoped token", nil)
		return
	}
	queue, err := parseOptionalEnum(r, "queue", ticketQueues)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	query, err := parseOptionalSearch(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	limit, offset, err := parsePagination(r, 50)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	filter := store.TicketFilter{ProjectIDs: projectIDs, Queue: queue, ActorID: identity.Actor.ID, Query: query, Offset: offset, Limit: limit}
	tickets, more, err := s.Store.ListTickets(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	counts, err := s.Store.CountTickets(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	next := ""
	if more && len(tickets) > 0 {
		next = encodeCursor(offset + len(tickets))
	}
	s.writeJSON(w, http.StatusOK, ticketCollection{Data: tickets, NextCursor: next, Counts: counts})
}

// projectTickets serves POST /api/v1/projects/{project}/tickets.
func (s *Server) projectTickets(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:write") {
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	project, err := s.Store.GetProject(r.Context(), reference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(project.ID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	input, err := decodeTaskInput(r, true)
	if err != nil {
		writeTaskInputError(w, err)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		task, err := s.Store.CreateTicket(r.Context(), project.ID, input, identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		w.Header().Set("Location", "/api/v1/tasks/"+task.ID)
		body, err := marshalTaskForIdentity(identity, task)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusCreated, body, taskETag(task), nil
	})
}
