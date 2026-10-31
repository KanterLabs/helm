package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) tasks(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method == http.MethodPost {
		if !requireScope(w, identity, "tasks:write") {
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
		limit, offset, cursorToken, paginationErr := parseTaskPagination(r, 50)
		if paginationErr != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", paginationErr.Error(), nil)
			return
		}
		sortName, descending, sortErr := parseTaskOrdering(r)
		if sortErr != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", sortErr.Error(), nil)
			return
		}
		state, err := parseOptionalEnum(r, "state", semanticStates)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		columnFilter, err := parseOptionalIdentifier(r, "column")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		priority, err := parseOptionalEnum(r, "priority", taskPriorities)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		label, err := parseOptionalIdentifier(r, "label")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		assignee, err := parseOptionalIdentifier(r, "assignee")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		kind, err := parseOptionalEnum(r, "kind", taskKinds)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		severity, err := parseOptionalEnum(r, "severity", bugSeverityFilters)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		reporter, err := parseOptionalIdentifier(r, "reporter")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		resolution, err := parseOptionalEnum(r, "resolution", bugResolutionFilters)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		agentState, err := parseOptionalEnum(r, "agent_state", agentWorkStates)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		actionNeeded, err := parseOptionalStrictBool(r, "action_needed")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		query, err := parseOptionalSearch(r)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		updatedAfter, err := parseOptionalTimestamp(r, "updated_after")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		dependency, err := parseOptionalEnum(r, "dependency", dependencyFilters)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		releaseFilter, err := s.parseProjectReleaseFilter(r, project.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		filter := store.TaskFilter{State: state, Column: columnFilter, Priority: priority, Label: label, Assignee: assignee, Kind: kind, Severity: severity, Reporter: reporter, Resolution: resolution, ReleaseID: releaseFilter, Dependency: dependency, AgentState: agentState, ActionNeeded: actionNeeded, Query: query, Cursor: offset, CursorToken: cursorToken, Sort: sortName, Descending: descending, Limit: limit, UpdatedAfter: updatedAfter}
		tasks, more, nextCursor, err := s.Store.ListTasksCursor(r.Context(), project.ID, filter)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !more {
			nextCursor = ""
		}
		s.writeCollection(w, tasks, nextCursor)
	case http.MethodPost:
		input, err := decodeTaskInput(r, true)
		if err != nil {
			writeTaskInputError(w, err)
			return
		}
		if input.ParentTaskID != nil {
			parent, parentErr := s.Store.ResolveTaskReference(r.Context(), *input.ParentTaskID)
			if parentErr != nil {
				s.writeStoreErrorForIdentity(w, identity, parentErr)
				return
			}
			// A scoped bearer must not learn whether a parent outside its
			// project ceiling exists. Human callers still reach the store's
			// same-project validation and receive the typed 400 response.
			if !identity.CanProject(parent.ProjectID) {
				s.writeError(w, http.StatusNotFound, "not_found", "task not found", nil)
				return
			}
		}
		if input.ReleaseID != nil {
			release, releaseErr := s.Store.GetRelease(r.Context(), *input.ReleaseID)
			if releaseErr != nil {
				s.writeStoreErrorForIdentity(w, identity, releaseErr)
				return
			}
			if release.ProjectID != project.ID {
				if identity.IsToken {
					s.writeError(w, http.StatusNotFound, "not_found", "release not found", nil)
				} else {
					s.writeStoreError(w, &store.Error{Kind: store.ErrReleaseCrossProject, Message: "release belongs to another project"})
				}
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			task, err := s.Store.CreateTask(r.Context(), project.ID, input, identity.Actor.ID)
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
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// parseTaskPagination preserves decimal/base64 offset cursors for retained
// API clients while recognizing the tc1 keyset cursors emitted by the board
// endpoint. Keyset cursors never fall back to offsets when malformed.
func parseTaskPagination(r *http.Request, fallback int) (int, int, string, error) {
	limit, err := parseLimit(r, fallback)
	if err != nil {
		return 0, 0, "", err
	}
	values, present := r.URL.Query()["cursor"]
	if !present {
		return limit, 0, "", nil
	}
	if len(values) != 1 {
		return 0, 0, "", errors.New("cursor must be supplied at most once")
	}
	value := values[0]
	if strings.HasPrefix(value, "tc1.") {
		if _, err := store.DecodeTaskCursor(value); err != nil {
			return 0, 0, "", err
		}
		return limit, 0, value, nil
	}
	offset, err := parseCursor(value)
	if err != nil {
		return 0, 0, "", err
	}
	return limit, offset, "", nil
}

func parseTaskOrdering(r *http.Request) (string, bool, error) {
	sortValue, sortPresent, err := queryValue(r, "sort")
	if err != nil {
		return "", false, err
	}
	orderValue, orderPresent, err := queryValue(r, "order")
	if err != nil {
		return "", false, err
	}
	if sortPresent && strings.TrimSpace(sortValue) == "" {
		return "", false, errors.New("sort must not be empty")
	}
	descending := false
	var embeddedDirection *bool
	if sortPresent {
		value := strings.ToLower(strings.TrimSpace(sortValue))
		for _, suffix := range []string{"_asc", ":asc", "_desc", ":desc"} {
			if strings.HasSuffix(value, suffix) {
				direction := strings.HasSuffix(suffix, "desc")
				descending = direction
				embeddedDirection = &direction
				value = strings.TrimSuffix(value, suffix)
				break
			}
		}
		sortValue = value
	}
	canonical, err := store.NormalizeTaskSort(sortValue)
	if err != nil {
		return "", false, err
	}
	if orderPresent {
		switch orderValue {
		case "asc":
			if embeddedDirection != nil && *embeddedDirection {
				return "", false, errors.New("sort and order disagree")
			}
			descending = false
		case "desc":
			if embeddedDirection != nil && !*embeddedDirection {
				return "", false, errors.New("sort and order disagree")
			}
			descending = true
		default:
			return "", false, errors.New("order must be asc or desc")
		}
	}
	return canonical, descending, nil
}

func (s *Server) task(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	var version int64
	if r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		if !requireScope(w, identity, "tasks:write") {
			return
		}
		var err error
		version, err = parseVersion(r)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
	}
	task, err := s.Store.ResolveTaskReference(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	id = task.ID
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
		s.writeTask(w, http.StatusOK, task)
	case http.MethodPatch:
		input, err := decodeTaskInput(r, false)
		if err != nil {
			writeTaskInputError(w, err)
			return
		}
		if input.ParentTaskID != nil {
			parent, parentErr := s.Store.ResolveTaskReference(r.Context(), *input.ParentTaskID)
			if parentErr != nil {
				s.writeStoreErrorForIdentity(w, identity, parentErr)
				return
			}
			if !identity.CanProject(parent.ProjectID) {
				s.writeError(w, http.StatusNotFound, "not_found", "task not found", nil)
				return
			}
		}
		if input.ReleaseID != nil {
			release, releaseErr := s.Store.GetRelease(r.Context(), *input.ReleaseID)
			if releaseErr != nil {
				s.writeStoreErrorForIdentity(w, identity, releaseErr)
				return
			}
			if release.ProjectID != task.ProjectID {
				if identity.IsToken {
					s.writeError(w, http.StatusNotFound, "not_found", "release not found", nil)
				} else {
					s.writeStoreError(w, &store.Error{Kind: store.ErrReleaseCrossProject, Message: "release belongs to another project"})
				}
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			updated, err := s.Store.UpdateTaskWithClaimOverride(r.Context(), id, input, version, identity.Actor.ID, !identity.IsToken && identity.Actor.Admin)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := marshalTaskForIdentity(identity, updated)
			if err != nil {
				return 0, nil, "", err
			}
			return http.StatusOK, body, taskETag(updated), nil
		})
	case http.MethodDelete:
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			if err := s.Store.DeleteTaskWithClaimOverride(r.Context(), id, version, identity.Actor.ID, !identity.IsToken && identity.Actor.Admin); err != nil {
				return 0, nil, "", err
			}
			return http.StatusNoContent, nil, "", nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func taskETag(task store.Task) string { return `"v` + strconv.FormatInt(task.Version, 10) + `"` }

// marshalTaskForIdentity keeps successful task mutations least-privilege. A
// bearer credential that can mutate or claim work but cannot read tasks gets
// only the immutable identity and new version; the ETag still carries the
// complete optimistic-concurrency validator. Humans and bearer credentials
// with tasks:read retain the normal full task response.
func marshalTaskForIdentity(identity auth.Identity, task store.Task) ([]byte, error) {
	if identity.IsToken && !identity.HasScope("tasks:read") {
		return json.Marshal(map[string]any{"id": task.ID, "version": task.Version})
	}
	return json.Marshal(task)
}

func (s *Server) writeTask(w http.ResponseWriter, status int, task store.Task) {
	body, err := json.Marshal(task)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeRaw(w, status, body, taskETag(task))
}
