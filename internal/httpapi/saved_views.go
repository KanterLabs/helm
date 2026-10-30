package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"sort"
	"strings"
)

func (s *Server) listVisibleSavedViews(r *http.Request, identity auth.Identity) ([]store.SavedView, error) {
	result, _, _, err := s.listVisibleSavedViewsPage(r, identity, 200, 0)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

const (
	// Saved-view visibility depends on JSON filters, so it cannot be pushed
	// into the base SQL query without duplicating the HTTP authorization rules.
	// Scan bounded datastore pages instead. The source offset is retained in
	// the returned cursor so hidden rows never consume an authorized page slot
	// or cause a visible row to be skipped.
	savedViewScanPageSize = 200
	savedViewScanBudget   = 2000
)

// listVisibleSavedViewsPage returns one page of the authorized saved-view
// stream. The datastore page is intentionally larger than the public page so
// a run of hidden shared views does not require one query per hidden row. If
// the bounded scan reaches its budget before filling the public page, the
// returned source offset remains resumable through next_cursor.
func (s *Server) listVisibleSavedViewsPage(r *http.Request, identity auth.Identity, limit, offset int) ([]store.SavedView, int, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	visible := make([]store.SavedView, 0, limit)
	sourceOffset := offset
	scanned := 0
	for scanned < savedViewScanBudget {
		batchLimit := savedViewScanPageSize
		if remaining := savedViewScanBudget - scanned; remaining < batchLimit {
			batchLimit = remaining
		}
		views, sourceMore, err := s.Store.ListSavedViews(r.Context(), identity.Actor.ID, batchLimit, sourceOffset)
		if err != nil {
			return nil, sourceOffset, false, err
		}
		if len(views) == 0 {
			return visible, sourceOffset, false, nil
		}
		for index, view := range views {
			scanned++
			if !s.savedViewVisible(r.Context(), identity, view) {
				continue
			}
			visible = append(visible, view)
			if len(visible) < limit {
				continue
			}
			// Rows after the last returned visible view remain part of the
			// authorized stream, even when the datastore page has no sentinel.
			nextOffset := sourceOffset + index + 1
			return visible, nextOffset, index+1 < len(views) || sourceMore, nil
		}
		sourceOffset += len(views)
		if !sourceMore {
			return visible, sourceOffset, false, nil
		}
	}
	// A cursor is still returned when every scanned row was hidden. The caller
	// can safely resume the authorized stream without learning any row data.
	return visible, sourceOffset, true, nil
}

func (s *Server) savedViewVisible(ctx context.Context, identity auth.Identity, view store.SavedView) bool {
	if view.ActorID != identity.Actor.ID && !view.Shared {
		return false
	}
	if scopedProjectIDs(identity) == nil {
		return true
	}
	if ids := scopedProjectIDs(identity); len(ids) == 0 && view.Shared {
		return false
	}
	// Saved filters are JSON and therefore can contain harmless casing or
	// whitespace differences in keys. Normalize aliases here as the execution
	// path does, otherwise a scoped actor could receive the metadata for a
	// shared view whose project ceiling was hidden behind `Project_ID`.
	for key, raw := range view.Filters {
		canonicalKey := strings.ToLower(strings.TrimSpace(key))
		switch canonicalKey {
		case "project", "project_id":
			value, ok := raw.(string)
			if !ok {
				return false
			}
			project, err := s.Store.GetProject(ctx, value)
			if err != nil || !identity.CanProject(project.ID) {
				return false
			}
		case "release_id":
			value, ok := raw.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return false
			}
			if strings.EqualFold(strings.TrimSpace(value), "unassigned") || strings.EqualFold(strings.TrimSpace(value), "none") {
				continue
			}
			release, err := s.Store.GetRelease(ctx, value)
			if err != nil || !identity.CanProject(release.ProjectID) {
				return false
			}
		case "project_ids", "projects":
			values, ok := raw.([]any)
			if !ok {
				if strings, stringOK := raw.([]string); stringOK {
					values = make([]any, len(strings))
					for i := range strings {
						values[i] = strings[i]
					}
				} else {
					return false
				}
			}
			for _, rawValue := range values {
				value, ok := rawValue.(string)
				if !ok {
					return false
				}
				project, err := s.Store.GetProject(ctx, value)
				if err != nil || !identity.CanProject(project.ID) {
					return false
				}
			}
		}
	}
	return true
}

type savedViewPayload struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Filters     json.RawMessage `json:"filters"`
	Sort        json.RawMessage `json:"sort"`
	Shared      *bool           `json:"shared"`
}

func decodeSavedViewInput(r *http.Request, creating bool) (store.SavedViewInput, error) {
	payload := savedViewPayload{}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		return store.SavedViewInput{}, err
	}
	if creating {
		if err := requireJSONFields(fields, "name", "filters"); err != nil {
			return store.SavedViewInput{}, err
		}
	}
	if err := rejectJSONNull(fields, "name", "description", "filters", "sort", "shared"); err != nil {
		return store.SavedViewInput{}, err
	}
	input := store.SavedViewInput{Name: payload.Name, Description: payload.Description, Shared: payload.Shared}
	if raw, ok := fields["filters"]; ok {
		var filters map[string]any
		if err := json.Unmarshal(raw, &filters); err != nil || filters == nil {
			return store.SavedViewInput{}, errors.New("filters must be a JSON object")
		}
		input.Filters, input.FiltersSet = filters, true
	}
	if raw, ok := fields["sort"]; ok {
		var terms []store.SearchSort
		if err := json.Unmarshal(raw, &terms); err != nil {
			// A single object is convenient for callers saving one sort field.
			var one store.SearchSort
			if objectErr := json.Unmarshal(raw, &one); objectErr != nil || one.Field == "" {
				return store.SavedViewInput{}, errors.New("sort must be an array or object")
			}
			terms = []store.SearchSort{one}
		}
		input.Sort, input.SortSet = terms, true
	}
	return input, nil
}

func (s *Server) savedViews(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
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
		visible, nextOffset, more, err := s.listVisibleSavedViewsPage(r, identity, limit, offset)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more {
			next = encodeCursor(nextOffset)
		}
		s.writeCollection(w, visible, next)
	case http.MethodPost:
		if !requireScope(w, identity, "tasks:write") {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		input, err := decodeSavedViewInput(r, true)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := s.validateSavedViewProjectCeiling(r, identity, input.Filters); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if err := s.validateSavedViewFilters(r, identity, input.Filters, input.Sort); err != nil {
			s.writeSavedViewValidationError(w, err)
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			view, createErr := s.Store.CreateSavedView(r.Context(), identity.Actor.ID, input)
			if createErr != nil {
				return 0, nil, "", createErr
			}
			body, marshalErr := json.Marshal(view)
			if marshalErr != nil {
				return 0, nil, "", marshalErr
			}
			w.Header().Set("Location", "/api/v1/views/"+view.ID)
			return http.StatusCreated, body, "", nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) savedView(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
	case http.MethodPatch, http.MethodDelete:
		if !requireScope(w, identity, "tasks:write") {
			return
		}
	}
	view, err := s.Store.GetSavedView(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !s.savedViewVisible(r.Context(), identity, view) {
		s.writeError(w, http.StatusNotFound, "not_found", "view not found", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, view)
	case http.MethodPatch:
		if view.ActorID != identity.Actor.ID {
			s.writeError(w, http.StatusForbidden, "forbidden", "only the view owner can update it", nil)
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		input, decodeErr := decodeSavedViewInput(r, false)
		if decodeErr != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if len(inputNameFields(input)) == 0 {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "patch must include at least one view field", nil)
			return
		}
		if input.FiltersSet {
			if err := s.validateSavedViewProjectCeiling(r, identity, input.Filters); err != nil {
				s.writeStoreError(w, err)
				return
			}
			if err := s.validateSavedViewFilters(r, identity, input.Filters, input.Sort); err != nil {
				s.writeSavedViewValidationError(w, err)
				return
			}
		}
		if !input.FiltersSet && input.SortSet {
			if err := s.validateSavedViewFilters(r, identity, view.Filters, input.Sort); err != nil {
				s.writeSavedViewValidationError(w, err)
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			updated, updateErr := s.Store.UpdateSavedView(r.Context(), id, identity.Actor.ID, input)
			if updateErr != nil {
				return 0, nil, "", updateErr
			}
			body, marshalErr := json.Marshal(updated)
			if marshalErr != nil {
				return 0, nil, "", marshalErr
			}
			return http.StatusOK, body, "", nil
		})
	case http.MethodDelete:
		if view.ActorID != identity.Actor.ID {
			s.writeError(w, http.StatusForbidden, "forbidden", "only the view owner can delete it", nil)
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			if deleteErr := s.Store.DeleteSavedView(r.Context(), id, identity.Actor.ID); deleteErr != nil {
				return 0, nil, "", deleteErr
			}
			return http.StatusNoContent, nil, "", nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func inputNameFields(input store.SavedViewInput) []string {
	fields := make([]string, 0, 5)
	if input.Name != nil {
		fields = append(fields, "name")
	}
	if input.Description != nil {
		fields = append(fields, "description")
	}
	if input.FiltersSet {
		fields = append(fields, "filters")
	}
	if input.SortSet {
		fields = append(fields, "sort")
	}
	if input.Shared != nil {
		fields = append(fields, "shared")
	}
	return fields
}

func (s *Server) validateSavedViewProjectCeiling(r *http.Request, identity auth.Identity, filters map[string]any) error {
	if scopedProjectIDs(identity) == nil {
		return nil
	}
	for key, raw := range filters {
		canonicalKey := strings.ToLower(strings.TrimSpace(key))
		switch canonicalKey {
		case "project", "project_id":
			value, ok := raw.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return &store.Error{Kind: store.ErrInvalid, Message: canonicalKey + " must be a project identifier"}
			}
			project, err := s.Store.GetProject(r.Context(), value)
			if err != nil {
				return err
			}
			if !identity.CanProject(project.ID) {
				return &store.Error{Kind: store.ErrForbidden, Message: "token is not scoped to this project"}
			}
		case "project_ids", "projects":
			values, ok := raw.([]any)
			if !ok {
				if strings, stringOK := raw.([]string); stringOK {
					values = make([]any, len(strings))
					for i := range strings {
						values[i] = strings[i]
					}
				} else {
					return &store.Error{Kind: store.ErrInvalid, Message: canonicalKey + " must be an array of project identifiers"}
				}
			}
			if len(values) > 200 {
				return &store.Error{Kind: store.ErrInvalid, Message: canonicalKey + " has too many projects"}
			}
			for _, rawValue := range values {
				value, ok := rawValue.(string)
				if !ok || strings.TrimSpace(value) == "" {
					return &store.Error{Kind: store.ErrInvalid, Message: canonicalKey + " must contain project identifiers"}
				}
				project, err := s.Store.GetProject(r.Context(), value)
				if err != nil {
					return err
				}
				if !identity.CanProject(project.ID) {
					return &store.Error{Kind: store.ErrForbidden, Message: "token is not scoped to this project"}
				}
			}
		case "release_id":
			value, ok := raw.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return &store.Error{Kind: store.ErrInvalid, Message: canonicalKey + " must be a release identifier"}
			}
			if strings.EqualFold(strings.TrimSpace(value), "unassigned") || strings.EqualFold(strings.TrimSpace(value), "none") {
				continue
			}
			release, err := s.Store.GetRelease(r.Context(), value)
			if err != nil {
				return err
			}
			if !identity.CanProject(release.ProjectID) {
				return &store.Error{Kind: store.ErrForbidden, Message: "token is not scoped to this project"}
			}
		}
	}
	return nil
}

func (s *Server) validateSavedViewFilters(r *http.Request, identity auth.Identity, filters map[string]any, sortTerms []store.SearchSort) error {
	view := store.SavedView{Filters: filters, Sort: sortTerms}
	_, err := s.searchFilterFromView(r, identity, view)
	return err
}

func (s *Server) writeSavedViewValidationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
		s.writeStoreError(w, err)
		return
	}
	s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
}
