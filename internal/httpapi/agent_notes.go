package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
)

type agentNoteInput struct {
	Category string   `json:"category"`
	Body     string   `json:"body"`
	Evidence []string `json:"evidence"`
}

func decodeAgentNote(r *http.Request) (agentNoteInput, error) {
	var input agentNoteInput
	fields, err := decodeJSONObject(r, &input)
	if err != nil {
		return input, err
	}
	if !jsonFieldPresent(fields, "category") || !jsonFieldPresent(fields, "body") {
		return input, taskInputError("agent note category and body are required")
	}
	if input.Evidence == nil {
		input.Evidence = []string{}
	}
	return input, nil
}

func (s *Server) agentNotes(w http.ResponseWriter, r *http.Request, identity auth.Identity, taskReference string) {
	if r.Method == http.MethodPost {
		if !requireScope(w, identity, "tasks:write") {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
	} else if r.Method == http.MethodGet {
		if !requireScope(w, identity, "tasks:read") {
			return
		}
	} else {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), taskReference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	if r.Method == http.MethodGet {
		includeResolved := strings.EqualFold(r.URL.Query().Get("include_resolved"), "true")
		notes, err := s.Store.ListAgentNotes(r.Context(), task.ID, includeResolved)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		s.writeCollection(w, notes, "")
		return
	}
	input, err := decodeAgentNote(r)
	if err != nil {
		writeTaskInputError(w, err)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		note, err := s.Store.CreateAgentNote(r.Context(), task.ID, identity.Actor.ID, input.Category, input.Body, input.Evidence)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(note)
		return http.StatusCreated, body, `"v` + strconv.FormatInt(note.Version, 10) + `"`, err
	})
}

func (s *Server) agentNoteMutation(w http.ResponseWriter, r *http.Request, identity auth.Identity, taskReference, noteID string) {
	if r.Method == http.MethodGet {
		if !requireScope(w, identity, "tasks:read") {
			return
		}
		task, err := s.Store.ResolveTaskReference(r.Context(), taskReference)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !identity.CanProject(task.ProjectID) {
			s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
			return
		}
		note, err := s.Store.GetAgentNote(r.Context(), noteID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if note.TaskID != task.ID {
			s.writeError(w, http.StatusNotFound, "not_found", "agent note not found", nil)
			return
		}
		w.Header().Set("ETag", `"v`+strconv.FormatInt(note.Version, 10)+`"`)
		s.writeJSON(w, http.StatusOK, note)
		return
	}
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:write") {
		return
	}
	version, err := parseVersion(r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), taskReference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	allowAdmin := !identity.IsToken && identity.Actor.Admin
	if r.Method == http.MethodDelete {
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			if err := s.Store.ResolveAgentNote(r.Context(), task.ID, noteID, identity.Actor.ID, version, allowAdmin); err != nil {
				return 0, nil, "", err
			}
			return http.StatusNoContent, nil, `"v` + strconv.FormatInt(version+1, 10) + `"`, nil
		})
		return
	}
	input, err := decodeAgentNote(r)
	if err != nil {
		writeTaskInputError(w, err)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		note, err := s.Store.UpdateAgentNote(r.Context(), task.ID, noteID, identity.Actor.ID, input.Category, input.Body, input.Evidence, version, allowAdmin)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(note)
		return http.StatusOK, body, `"v` + strconv.FormatInt(note.Version, 10) + `"`, err
	})
}
