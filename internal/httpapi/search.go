package httpapi

import (
	"errors"
	"fmt"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// searchCollection is intentionally task-shaped in data so existing API
// clients can consume global search exactly like a task collection. Projects
// and saved views are included as optional command-palette results without
// making a second uncached request for every keystroke.
type searchCollection struct {
	Data       []store.Task      `json:"data"`
	NextCursor string            `json:"next_cursor"`
	Projects   []store.Project   `json:"projects,omitempty"`
	Views      []store.SavedView `json:"views,omitempty"`
}

// search handles the global task search surface. It keeps project and view
// metadata in the same response for command search while applying the task
// project ceiling in the SQL query before pagination.
func (s *Server) search(w http.ResponseWriter, r *http.Request, identity auth.Identity, viewReference string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	filter, err := s.parseSearchFilter(r, identity, viewReference)
	if err != nil {
		if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
			s.writeStoreError(w, err)
		} else {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		}
		return
	}
	tasks, more, err := s.Store.ListSearchTasksWithExtra(r.Context(), filter)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	next := ""
	if more && len(tasks) > 0 {
		next = encodeCursor(filter.Cursor + len(tasks))
	}
	result := searchCollection{Data: tasks, NextCursor: next}
	// q is also useful for project and view navigation. Those records are
	// independently filtered by the same project ceiling; task pagination is
	// intentionally not affected by their count.
	if filter.Query != "" {
		q := strings.ToLower(strings.TrimSpace(filter.Query))
		projects, projectErr := s.Store.ListSearchProjects(r.Context(), filter.ProjectIDs, q)
		if projectErr != nil {
			s.writeInternal(w, projectErr)
			return
		}
		for _, project := range projects {
			result.Projects = append(result.Projects, project)
		}
		views, viewErr := s.listVisibleSavedViews(r, identity)
		if viewErr != nil {
			s.writeInternal(w, viewErr)
			return
		}
		for _, view := range views {
			if strings.Contains(strings.ToLower(view.Name), q) || strings.Contains(strings.ToLower(view.Description), q) {
				result.Views = append(result.Views, view)
			}
		}
		if result.Projects == nil {
			result.Projects = []store.Project{}
		}
		if result.Views == nil {
			result.Views = []store.SavedView{}
		}
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) parseSearchFilter(r *http.Request, identity auth.Identity, viewReference string) (store.SearchFilter, error) {
	limit, offset, err := parsePagination(r, 50)
	if err != nil {
		return store.SearchFilter{}, err
	}
	filter := store.SearchFilter{Limit: limit, Cursor: offset, ProjectIDs: scopedProjectIDs(identity)}
	if viewReference == "" {
		if reference, parseErr := parseOptionalIdentifier(r, "view"); parseErr != nil {
			return store.SearchFilter{}, parseErr
		} else if reference != "" {
			viewReference = reference
		}
	}
	if viewReference != "" {
		view, err := s.Store.GetSavedView(r.Context(), viewReference)
		if err != nil {
			return store.SearchFilter{}, err
		}
		if !s.savedViewVisible(r.Context(), identity, view) {
			return store.SearchFilter{}, &store.Error{Kind: store.ErrForbidden, Message: "view is not available to this actor"}
		}
		filter, err = s.searchFilterFromView(r, identity, view)
		if err != nil {
			return store.SearchFilter{}, err
		}
		filter.Cursor, filter.Limit = offset, limit
	}
	// A saved view can be refined without changing its stored definition. Clear
	// only the explicitly named editable criteria; retain project scope and all
	// other saved filters. Explicit values below can then replace cleared fields.
	if clear, present, parseErr := queryValue(r, "clear"); parseErr != nil {
		return store.SearchFilter{}, parseErr
	} else if present {
		for _, name := range strings.Split(clear, ",") {
			switch name {
			case "q":
				filter.Query = ""
			case "priority":
				filter.Priority = ""
			case "state":
				filter.State = ""
			case "release_id":
				filter.ReleaseID = ""
			default:
				return store.SearchFilter{}, fmt.Errorf("unsupported clear criterion %q", name)
			}
		}
	}
	// Explicit query parameters override matching saved-view filters. This
	// makes a view URL useful as a starting point without mutating the view.
	if value, present, parseErr := searchTermValue(r, "q"); parseErr != nil {
		return store.SearchFilter{}, parseErr
	} else if present {
		filter.Query = value
	}
	for _, field := range []struct {
		name string
		dest *string
	}{
		{"key", &filter.Key}, {"title", &filter.Title}, {"description", &filter.Description},
		{"label", &filter.Label}, {"assignee", &filter.Assignee}, {"claim_owner", &filter.ClaimOwner}, {"claimed_by", &filter.ClaimOwner},
		{"release_id", &filter.ReleaseID},
	} {
		value, present, parseErr := searchTermValue(r, field.name)
		if parseErr != nil {
			return store.SearchFilter{}, parseErr
		}
		if present {
			*field.dest = value
		}
	}
	if filter.ReleaseID != "" {
		filter.ReleaseID, err = s.resolveGlobalReleaseFilter(r, identity, filter.ReleaseID)
		if err != nil {
			return store.SearchFilter{}, err
		}
	}
	if filter.State, err = overrideOptionalEnum(r, "state", semanticStates, filter.State); err != nil {
		return store.SearchFilter{}, err
	}
	if filter.Priority, err = overrideOptionalEnum(r, "priority", taskPriorities, filter.Priority); err != nil {
		return store.SearchFilter{}, err
	}
	if project, parseErr := parseOptionalIdentifier(r, "project"); parseErr != nil {
		return store.SearchFilter{}, parseErr
	} else if project != "" {
		resolved, resolveErr := s.Store.GetProject(r.Context(), project)
		if resolveErr != nil {
			return store.SearchFilter{}, resolveErr
		}
		if !identity.CanProject(resolved.ID) {
			return store.SearchFilter{}, &store.Error{Kind: store.ErrForbidden, Message: "token is not scoped to this project"}
		}
		filter.Project = resolved.ID
		filter.ProjectIDs = []string{resolved.ID}
	}
	for _, aliases := range [][2]string{{"due_from", "due_after"}, {"due_to", "due_before"}} {
		parsed, present, parseErr := optionalTimestampAliases(r, aliases[0], aliases[1])
		if parseErr != nil {
			return store.SearchFilter{}, parseErr
		}
		if present {
			if aliases[0] == "due_from" {
				filter.DueFrom = parsed
			} else {
				filter.DueTo = parsed
			}
		}
	}
	if filter.DueFrom != nil && filter.DueTo != nil && filter.DueFrom.After(*filter.DueTo) {
		return store.SearchFilter{}, errors.New("due_from must be before due_to")
	}
	if sortValue, present, parseErr := queryValue(r, "sort"); parseErr != nil {
		return store.SearchFilter{}, parseErr
	} else if present {
		filter.Sort, err = parseSearchSort(sortValue)
		if err != nil {
			return store.SearchFilter{}, err
		}
	}
	return filter, nil
}

func searchTermValue(r *http.Request, name string) (string, bool, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return "", false, err
	}
	if !present {
		return "", false, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, fmt.Errorf("%s must not be empty", name)
	}
	if utf8.RuneCountInString(value) > 200 {
		return "", false, fmt.Errorf("%s is too long", name)
	}
	return value, true, nil
}

func overrideOptionalEnum(r *http.Request, name string, allowed map[string]struct{}, current string) (string, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return "", err
	}
	if !present {
		return current, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if _, ok := allowed[value]; !ok {
		return "", fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func optionalTimestampAliases(r *http.Request, first, second string) (*time.Time, bool, error) {
	firstValue, firstPresent, err := queryValue(r, first)
	if err != nil {
		return nil, false, err
	}
	secondValue, secondPresent, err := queryValue(r, second)
	if err != nil {
		return nil, false, err
	}
	if firstPresent && secondPresent {
		return nil, false, fmt.Errorf("%s and %s cannot both be supplied", first, second)
	}
	if !firstPresent && !secondPresent {
		return nil, false, nil
	}
	name, value := first, firstValue
	if secondPresent {
		name, value = second, secondValue
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, fmt.Errorf("%s must be RFC3339", name)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, false, fmt.Errorf("%s must be RFC3339", name)
	}
	return &parsed, true, nil
}

func parseSearchSort(value string) ([]store.SearchSort, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("sort must not be empty")
	}
	parts := strings.Split(value, ",")
	if len(parts) > 8 {
		return nil, errors.New("sort has too many fields")
	}
	result := make([]store.SearchSort, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("sort contains an empty field")
		}
		pieces := strings.SplitN(part, ":", 2)
		field := strings.TrimSpace(pieces[0])
		direction := "asc"
		if len(pieces) == 2 {
			direction = strings.TrimSpace(pieces[1])
		}
		if direction != "asc" && direction != "desc" {
			return nil, errors.New("sort direction must be asc or desc")
		}
		result = append(result, store.SearchSort{Field: field, Direction: direction})
	}
	return result, nil
}
