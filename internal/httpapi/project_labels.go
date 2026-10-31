package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strconv"
)

func (s *Server) labels(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method == http.MethodPost {
		if !requireScope(w, identity, "projects:write") {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
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
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		labels, more, err := s.Store.ListLabelsPage(r.Context(), project.ID, limit, offset)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more && len(labels) > 0 {
			next = encodeCursor(offset + len(labels))
		}
		s.writeCollection(w, labels, next)
	case http.MethodPost:
		var payload store.LabelInput
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := requireJSONFields(fields, "name"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if err := rejectJSONNull(fields, "name"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if raw, present := fields["color"]; present && !isJSONNull(raw) {
			if !validHTTPColor(payload.Color) {
				s.writeStoreError(w, taskInputError("color must be a six-digit hexadecimal value"))
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			label, err := s.Store.CreateLabel(r.Context(), project.ID, payload, identity.Actor.ID)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/labels/"+label.ID)
			body, _ := json.Marshal(label)
			return http.StatusCreated, body, "", nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) deleteLabel(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if !requireScope(w, identity, "projects:write") {
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	labelProject, err := s.labelProject(r, id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(labelProject) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		err := s.Store.DeleteLabel(r.Context(), id, identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusNoContent, nil, "", nil
	})
}

func (s *Server) labelProject(r *http.Request, id string) (string, error) {
	var projectID string
	err := s.Store.DB.QueryRowContext(r.Context(), `SELECT project_id FROM labels WHERE id=?`, id).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return projectID, err
}

func validHTTPColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func projectETag(version int64) string { return `"v` + strconv.FormatInt(version, 10) + `"` }

func columnETag(version int64) string { return `"v` + strconv.FormatInt(version, 10) + `"` }

// marshalProjectForIdentity keeps successful project mutations least-privilege.
// A bearer credential with projects:write but without projects:read may still
// update a project, but must not receive its private fields in the mutation
// response. Humans and read-capable bearer credentials retain the full object.
func marshalProjectForIdentity(identity auth.Identity, project store.Project) ([]byte, error) {
	if identity.IsToken && !identity.HasScope("projects:read") {
		return json.Marshal(map[string]any{"id": project.ID})
	}
	return json.Marshal(project)
}

// marshalColumnForIdentity applies the same response boundary to column
// mutations. project_id is retained so a scoped writer can correlate the
// newly changed column without exposing its name, state, or timestamps.
func marshalColumnForIdentity(identity auth.Identity, column store.Column) ([]byte, error) {
	if identity.IsToken && !identity.HasScope("projects:read") {
		return json.Marshal(map[string]any{"id": column.ID, "project_id": column.ProjectID})
	}
	return json.Marshal(column)
}
