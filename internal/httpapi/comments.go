package httpapi

import (
	"encoding/json"
	"github.com/KanterLabs/helm/internal/auth"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) comments(w http.ResponseWriter, r *http.Request, identity auth.Identity, taskID string) {
	if r.Method == http.MethodPost {
		if !requireScope(w, identity, "tasks:write") {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), taskID)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	taskID = task.ID
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
		limit, offset, paginationErr := parsePagination(r, 50)
		if paginationErr != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", paginationErr.Error(), nil)
			return
		}
		comments, more, err := s.Store.ListCommentsPage(r.Context(), taskID, limit, offset)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more {
			next = encodeCursor(offset + len(comments))
		}
		s.writeCollection(w, comments, next)
	case http.MethodPost:
		body, err := decodeCommentBody(r)
		if err != nil {
			writeTaskInputError(w, err)
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			comment, err := s.Store.CreateComment(r.Context(), taskID, identity.Actor.ID, body)
			if err != nil {
				return 0, nil, "", err
			}
			body, _ := json.Marshal(comment)
			return http.StatusCreated, body, `"v` + strconv.FormatInt(comment.Version, 10) + `"`, nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// commentMutation handles the nested comment resource. Comment writes are
// guarded by the comment's own version and author ownership, while the task
// reference is resolved first so project scope remains fail-closed.
func (s *Server) commentMutation(w http.ResponseWriter, r *http.Request, identity auth.Identity, taskReference, commentID string) {
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
		comment, err := s.Store.GetComment(r.Context(), commentID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if comment.TaskID != task.ID {
			s.writeError(w, http.StatusNotFound, "not_found", "comment not found", nil)
			return
		}
		w.Header().Set("ETag", `"v`+strconv.FormatInt(comment.Version, 10)+`"`)
		s.writeJSON(w, http.StatusOK, comment)
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
			if err := s.Store.DeleteComment(r.Context(), task.ID, commentID, identity.Actor.ID, version, allowAdmin); err != nil {
				return 0, nil, "", err
			}
			return http.StatusNoContent, nil, `"v` + strconv.FormatInt(version+1, 10) + `"`, nil
		})
		return
	}
	body, err := decodeCommentBody(r)
	if err != nil {
		writeTaskInputError(w, err)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		comment, err := s.Store.UpdateComment(r.Context(), task.ID, commentID, identity.Actor.ID, body, version, allowAdmin)
		if err != nil {
			return 0, nil, "", err
		}
		encoded, err := json.Marshal(comment)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusOK, encoded, `"v` + strconv.FormatInt(comment.Version, 10) + `"`, nil
	})
}

func decodeCommentBody(r *http.Request) (string, error) {
	var payload struct {
		Body *string `json:"body"`
		Text *string `json:"text"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		return "", err
	}
	if !jsonFieldPresent(fields, "body") && !jsonFieldPresent(fields, "text") {
		return "", taskInputError("comment body is required")
	}
	body := ""
	if payload.Body != nil {
		body = strings.TrimSpace(*payload.Body)
	}
	if body == "" && payload.Text != nil {
		body = strings.TrimSpace(*payload.Text)
	}
	if body == "" {
		return "", taskInputError("comment body is required")
	}
	if len(body) > 20000 {
		return "", taskInputError("comment is too long")
	}
	return body, nil
}
