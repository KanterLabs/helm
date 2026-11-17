package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/KanterLabs/helm/internal/auth"
)

func (s *Server) taskPullRequestLinks(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference, linkID string) {
	read := r.Method == http.MethodGet && linkID == ""
	write := (r.Method == http.MethodPost && linkID == "") || (r.Method == http.MethodDelete && linkID != "")
	if !read && !write {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	scope := "tasks:read"
	if write {
		scope = "tasks:write"
	}
	if !requireScope(w, identity, scope) {
		return
	}
	var version int64
	if write {
		var err error
		version, err = parseVersion(r)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	// Resolve project access before replaying any response containing link data.
	task, err := s.resolveChecklistTask(r, identity, reference)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if read {
		links, e := s.Store.ListPullRequestLinks(r.Context(), task.ID)
		if e != nil {
			s.writeStoreError(w, e)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"items": links})
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	rawURL := ""
	if r.Method == http.MethodPost {
		var input struct {
			URL *string `json:"url"`
		}
		fields, e := decodeJSONObject(r, &input)
		if e != nil || len(fields) != 1 || input.URL == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "request must contain only a pull request URL", nil)
			return
		}
		rawURL = *input.URL
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		override := !identity.IsToken && identity.Actor.Admin
		if r.Method == http.MethodDelete {
			updated, e := s.Store.DetachPullRequest(r.Context(), task.ID, linkID, version, identity.Actor.ID, override)
			if e != nil {
				return 0, nil, "", e
			}
			taskBody, e := marshalTaskForIdentity(identity, updated)
			if e != nil {
				return 0, nil, "", e
			}
			body, e := json.Marshal(map[string]any{"task": json.RawMessage(taskBody)})
			return http.StatusOK, body, taskETag(updated), e
		}
		link, updated, e := s.Store.LinkPullRequest(r.Context(), task.ID, rawURL, version, identity.Actor.ID, override)
		if e != nil {
			return 0, nil, "", e
		}
		taskBody, e := marshalTaskForIdentity(identity, updated)
		if e != nil {
			return 0, nil, "", e
		}
		body, e := json.Marshal(map[string]any{"link": link, "task": json.RawMessage(taskBody)})
		return http.StatusCreated, body, taskETag(updated), e
	})
}
