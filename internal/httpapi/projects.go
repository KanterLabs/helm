package httpapi

import (
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
)

func (s *Server) projects(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
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
		favoriteOnly, err := parseOptionalBool(r, "favorite")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		projects, err := s.Store.ListProjectsFilteredWithExtra(r.Context(), limit+1, offset, store.ProjectFilter{IncludeArchived: includeArchived, FavoriteOnly: favoriteOnly, ProjectIDs: scopedProjectIDs(identity)})
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		hasMore := len(projects) > limit
		if hasMore {
			projects = projects[:limit]
		}
		next := ""
		if hasMore {
			next = encodeCursor(offset + len(projects))
		}
		s.writeCollection(w, projects, next)
	case http.MethodPost:
		if !requireScope(w, identity, "projects:write") {
			return
		}
		// A project-scoped token may manage only its existing allow-list. It
		// cannot authorize a brand-new project whose ID is necessarily outside
		// that ceiling; an unscoped projects:write token remains able to create.
		if identity.IsToken && identity.Token.ProjectsScoped {
			s.writeError(w, http.StatusForbidden, "forbidden", "project-scoped tokens cannot create projects", nil)
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		var payload struct {
			Key                       *string `json:"key"`
			Slug                      *string `json:"slug"`
			Name                      *string `json:"name"`
			Description               *string `json:"description"`
			Color                     *string `json:"color"`
			Favorite                  *bool   `json:"favorite"`
			ChecklistCompletionPolicy *string `json:"checklist_completion_policy"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := requireJSONFields(fields, "key", "name"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if err := rejectJSONNull(fields, "key", "slug", "name", "description", "color", "favorite", "checklist_completion_policy"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if _, present := fields["color"]; present {
			if payload.Color == nil || !validHTTPColor(*payload.Color) {
				s.writeStoreError(w, taskInputError("color must be a six-digit hexadecimal value"))
				return
			}
		}
		projectInput := store.ProjectInput{Key: payload.Key, Slug: payload.Slug, Name: payload.Name, Description: payload.Description, Color: payload.Color, Favorite: payload.Favorite, ChecklistCompletionPolicy: payload.ChecklistCompletionPolicy}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			project, err := s.Store.CreateProject(r.Context(), projectInput, identity.Actor.ID)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/projects/"+project.ID)
			body, err := marshalProjectForIdentity(identity, project)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusCreated, body, projectETag(project.Version), nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) project(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method == http.MethodPatch {
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
		w.Header().Set("ETag", projectETag(project.Version))
		s.writeJSON(w, http.StatusOK, project)
	case http.MethodPatch:
		var payload struct {
			Key                       *string `json:"key"`
			Slug                      *string `json:"slug"`
			Name                      *string `json:"name"`
			Description               *string `json:"description"`
			Color                     *string `json:"color"`
			Favorite                  *bool   `json:"favorite"`
			Archived                  *bool   `json:"archived"`
			ChecklistCompletionPolicy *string `json:"checklist_completion_policy"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := rejectJSONNull(fields, "key", "slug", "name", "description", "color", "favorite", "archived", "checklist_completion_policy"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if _, present := fields["color"]; present {
			if payload.Color == nil || !validHTTPColor(*payload.Color) {
				s.writeStoreError(w, taskInputError("color must be a six-digit hexadecimal value"))
				return
			}
		}
		if payload.Key == nil && payload.Slug == nil && payload.Name == nil && payload.Description == nil && payload.Color == nil && payload.Favorite == nil && payload.Archived == nil && payload.ChecklistCompletionPolicy == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "patch must include at least one project field", nil)
			return
		}
		input := store.ProjectInput{Key: payload.Key, Slug: payload.Slug, Name: payload.Name, Description: payload.Description, Color: payload.Color, Favorite: payload.Favorite, Archived: payload.Archived, ChecklistCompletionPolicy: payload.ChecklistCompletionPolicy}
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
			updated, err := s.Store.UpdateProjectWithVersion(r.Context(), project.ID, input, identity.Actor.ID, expected)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := marshalProjectForIdentity(identity, updated)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusOK, body, projectETag(updated.Version), nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}
