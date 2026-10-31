package httpapi

import (
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
)

func (s *Server) columns(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
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
		if !requireScope(w, identity, "projects:read") {
			return
		}
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		includeArchived, err := parseOptionalBool(r, "archived")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		columns, more, err := s.Store.ListColumnsPageFiltered(r.Context(), project.ID, limit, offset, includeArchived)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more && len(columns) > 0 {
			next = encodeCursor(offset + len(columns))
		}
		s.writeCollection(w, columns, next)
	case http.MethodPost:
		var payload struct {
			Name          *string `json:"name"`
			SemanticState *string `json:"semantic_state"`
			Position      *int    `json:"position"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := requireJSONFields(fields, "name", "semantic_state"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if err := rejectJSONNull(fields, "name", "semantic_state", "position"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		input := store.ColumnInput{Name: payload.Name, SemanticState: payload.SemanticState, Position: payload.Position}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			column, err := s.Store.CreateColumn(r.Context(), project.ID, input, identity.Actor.ID)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/columns/"+column.ID)
			body, err := marshalColumnForIdentity(identity, column)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusCreated, body, columnETag(column.Version), nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) column(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if r.Method == http.MethodPatch {
		if !requireScope(w, identity, "projects:write") {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
	}
	column, err := s.Store.GetColumn(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(column.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	if r.Method != http.MethodPatch {
		if r.Method == http.MethodGet {
			if !requireScope(w, identity, "projects:read") {
				return
			}
			w.Header().Set("ETag", columnETag(column.Version))
			s.writeJSON(w, http.StatusOK, column)
			return
		}
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var payload struct {
		Name          *string `json:"name"`
		SemanticState *string `json:"semantic_state"`
		Position      *int    `json:"position"`
		Archived      *bool   `json:"archived"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := rejectJSONNull(fields, "name", "semantic_state", "position", "archived"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Name == nil && payload.SemanticState == nil && payload.Position == nil && payload.Archived == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "patch must include at least one column field", nil)
		return
	}
	input := store.ColumnInput{Name: payload.Name, SemanticState: payload.SemanticState, Position: payload.Position, Archived: payload.Archived}
	var expected *int64
	if strings.TrimSpace(r.Header.Get("If-Match")) != "" {
		version, versionErr := parseVersion(r)
		if versionErr != nil {
			s.writeStoreError(w, versionErr)
			return
		}
		expected = &version
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		updated, err := s.Store.UpdateColumnWithVersion(r.Context(), id, input, identity.Actor.ID, !identity.IsToken && identity.Actor.Admin, expected)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := marshalColumnForIdentity(identity, updated)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusOK, body, columnETag(updated.Version), nil
	})
}
