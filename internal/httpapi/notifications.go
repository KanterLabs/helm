package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func requireNotificationRead(w http.ResponseWriter, identity auth.Identity) bool {
	if identity.HasScope("notifications:read") || identity.HasScope("tasks:read") {
		return true
	}
	return requireScope(w, identity, "notifications:read")
}

func requireNotificationWrite(w http.ResponseWriter, identity auth.Identity) bool {
	if identity.HasScope("notifications:write") || identity.HasScope("tasks:write") || identity.HasScope("projects:write") {
		return true
	}
	return requireScope(w, identity, "notifications:write")
}

// marshalNotificationMutationForIdentity keeps notification read-state
// mutations least-privilege. A bearer credential may be allowed to change
// inbox state through a write scope without being allowed to read the
// notification's title, body, payload, or related task/project details.
func marshalNotificationMutationForIdentity(identity auth.Identity, notification store.Notification) ([]byte, error) {
	if identity.IsToken && !identity.HasScope("notifications:read") && !identity.HasScope("tasks:read") {
		return json.Marshal(struct {
			ID     string  `json:"id"`
			ReadAt *string `json:"read_at"`
		}{ID: notification.ID, ReadAt: notification.ReadAt})
	}
	return json.Marshal(notification)
}

func (s *Server) notifications(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	switch r.Method {
	case http.MethodGet:
		if !requireNotificationRead(w, identity) {
			return
		}
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		unread, err := parseOptionalStrictBool(r, "unread")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		items, more, err := s.Store.ListNotifications(r.Context(), identity.Actor.ID, store.NotificationFilter{UnreadOnly: unread, Limit: limit, Offset: offset, ProjectIDs: scopedProjectIDs(identity)})
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		next := ""
		if more && len(items) > 0 {
			next = encodeCursor(offset + len(items))
		}
		s.writeCollection(w, items, next)
	case http.MethodPost:
		if !requireNotificationWrite(w, identity) {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		var payload struct {
			IDs []string `json:"ids"`
			All *bool    `json:"all"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		for name := range fields {
			if name != "ids" && name != "all" {
				s.writeError(w, http.StatusBadRequest, "invalid_request", "unknown notification field: "+name, nil)
				return
			}
		}
		if err := rejectJSONNull(fields, "ids", "all"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if raw, present := fields["ids"]; present {
			if err := validateIdentifierArray(raw, "ids", false); err != nil {
				s.writeStoreError(w, err)
				return
			}
		}
		all := payload.All != nil && *payload.All
		if len(payload.IDs) == 0 && !all {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "ids or all=true is required", nil)
			return
		}
		s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
			count := 0
			var err error
			if all {
				count, err = s.Store.MarkAllNotificationsReadScoped(r.Context(), identity.Actor.ID, scopedProjectIDs(identity))
			} else {
				count, err = s.Store.MarkNotificationsReadScoped(r.Context(), identity.Actor.ID, payload.IDs, scopedProjectIDs(identity))
			}
			if err != nil {
				return 0, nil, "", err
			}
			body, _ := json.Marshal(map[string]any{"marked_read": count})
			return http.StatusOK, body, "", nil
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) notification(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireNotificationWrite(w, identity) {
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	readValue := true
	if r.Method == http.MethodPatch {
		var payload struct {
			Read *bool `json:"read"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		for name := range fields {
			if name != "read" {
				s.writeError(w, http.StatusBadRequest, "invalid_request", "unknown notification field: "+name, nil)
				return
			}
		}
		if err := requireJSONFields(fields, "read"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if payload.Read == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "read is required", nil)
			return
		}
		readValue = *payload.Read
	}
	s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
		var item store.Notification
		var err error
		if r.Method == http.MethodPatch {
			item, err = s.Store.SetNotificationReadScoped(r.Context(), identity.Actor.ID, id, readValue, scopedProjectIDs(identity))
		} else {
			item, err = s.Store.MarkNotificationReadScoped(r.Context(), identity.Actor.ID, id, scopedProjectIDs(identity))
		}
		if err != nil {
			return 0, nil, "", err
		}
		body, err := marshalNotificationMutationForIdentity(identity, item)
		return http.StatusOK, body, "", err
	})
}

func (s *Server) notificationPreferences(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	switch r.Method {
	case http.MethodGet:
		if !requireNotificationRead(w, identity) {
			return
		}
		preferences, err := s.Store.GetNotificationPreferences(r.Context(), identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, preferences)
	case http.MethodPatch:
		if !requireNotificationWrite(w, identity) {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		var payload struct {
			Assignments  *bool `json:"assignments"`
			Mentions     *bool `json:"mentions"`
			Blockers     *bool `json:"blockers"`
			StateChanges *bool `json:"state_changes"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		for name := range fields {
			switch name {
			case "assignments", "mentions", "blockers", "state_changes":
			default:
				s.writeError(w, http.StatusBadRequest, "invalid_request", "unknown preference field: "+name, nil)
				return
			}
		}
		if err := rejectJSONNull(fields, "assignments", "mentions", "blockers", "state_changes"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if len(fields) == 0 {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "at least one preference is required", nil)
			return
		}
		input := store.NotificationPreferencesInput{Assignments: payload.Assignments, Mentions: payload.Mentions, Blockers: payload.Blockers, StateChanges: payload.StateChanges}
		s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
			updated, err := s.Store.UpdateNotificationPreferences(r.Context(), identity.Actor.ID, input)
			if err != nil {
				return 0, nil, "", err
			}
			body, err := json.Marshal(updated)
			return http.StatusOK, body, "", err
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) watches(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	switch r.Method {
	case http.MethodGet:
		if !requireNotificationRead(w, identity) {
			return
		}
		projectID, err := parseOptionalIdentifier(r, "project")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		taskID, err := parseOptionalIdentifier(r, "task")
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		if projectID != "" {
			project, projectErr := s.Store.GetProject(r.Context(), projectID)
			if projectErr != nil {
				s.writeStoreError(w, projectErr)
				return
			}
			if !identity.CanProject(project.ID) {
				s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
				return
			}
			projectID = project.ID
		}
		if taskID != "" {
			task, taskErr := s.Store.ResolveTaskReference(r.Context(), taskID)
			if taskErr != nil {
				s.writeStoreError(w, taskErr)
				return
			}
			if !identity.CanProject(task.ProjectID) {
				s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
				return
			}
			taskID = task.ID
			if projectID != "" && projectID != task.ProjectID {
				s.writeError(w, http.StatusBadRequest, "invalid_request", "task belongs to another project", nil)
				return
			}
			projectID = task.ProjectID
		}
		items, err := s.Store.ListWatchesScoped(r.Context(), identity.Actor.ID, projectID, taskID, scopedProjectIDs(identity))
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeCollection(w, items, "")
	case http.MethodPost:
		if !requireNotificationWrite(w, identity) {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		var payload struct {
			ProjectID *string `json:"project_id"`
			TaskID    *string `json:"task_id"`
		}
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		for name := range fields {
			if name != "project_id" && name != "task_id" {
				s.writeError(w, http.StatusBadRequest, "invalid_request", "unknown watch field: "+name, nil)
				return
			}
		}
		if err := rejectJSONNull(fields, "project_id", "task_id"); err != nil {
			s.writeStoreError(w, err)
			return
		}
		if payload.ProjectID == nil && payload.TaskID == nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "project_id or task_id is required", nil)
			return
		}
		if payload.ProjectID != nil && payload.TaskID != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "provide exactly one of project_id or task_id", nil)
			return
		}
		projectReference, taskReference := "", ""
		if payload.ProjectID != nil {
			projectReference = strings.TrimSpace(*payload.ProjectID)
		}
		if payload.TaskID != nil {
			taskReference = strings.TrimSpace(*payload.TaskID)
		}
		if taskReference != "" {
			task, taskErr := s.Store.ResolveTaskReference(r.Context(), taskReference)
			if taskErr != nil {
				s.writeStoreError(w, taskErr)
				return
			}
			if !identity.CanProject(task.ProjectID) {
				s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
				return
			}
			if projectReference == "" {
				projectReference = task.ProjectID
			}
		}
		if projectReference != "" {
			project, projectErr := s.Store.GetProject(r.Context(), projectReference)
			if projectErr != nil {
				s.writeStoreError(w, projectErr)
				return
			}
			if !identity.CanProject(project.ID) {
				s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
				return
			}
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			watch, err := s.Store.CreateWatch(r.Context(), identity.Actor.ID, projectReference, taskReference)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/watches/"+watch.ID)
			body, err := json.Marshal(watch)
			return http.StatusCreated, body, "", err
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if r.Method != http.MethodDelete && r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if r.Method == http.MethodGet {
		if !requireNotificationRead(w, identity) {
			return
		}
		watch, err := s.Store.GetWatch(r.Context(), id, identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		if !identity.CanProject(watch.ProjectID) {
			s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
			return
		}
		s.writeJSON(w, http.StatusOK, watch)
		return
	}
	if !requireNotificationWrite(w, identity) {
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	s.mutationRateOnly(w, r, identity, func() (int, []byte, string, error) {
		watch, err := s.Store.GetWatch(r.Context(), id, identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		if !identity.CanProject(watch.ProjectID) {
			return 0, nil, "", store.ErrForbidden
		}
		if err := s.Store.DeleteWatch(r.Context(), identity.Actor.ID, id); err != nil {
			return 0, nil, "", err
		}
		return http.StatusNoContent, nil, "", nil
	})
}

func (s *Server) projectWatch(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
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
		if !requireNotificationRead(w, identity) {
			return
		}
		items, err := s.Store.ListWatchesScoped(r.Context(), identity.Actor.ID, project.ID, "", scopedProjectIDs(identity))
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeCollection(w, items, "")
	case http.MethodPost:
		if !requireNotificationWrite(w, identity) {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			watch, err := s.Store.CreateProjectWatch(r.Context(), identity.Actor.ID, project.ID)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/watches/"+watch.ID)
			body, err := json.Marshal(watch)
			return http.StatusCreated, body, "", err
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) taskWatch(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	task, err := s.Store.ResolveTaskReference(r.Context(), reference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(task.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !requireNotificationRead(w, identity) {
			return
		}
		items, err := s.Store.ListWatchesScoped(r.Context(), identity.Actor.ID, task.ProjectID, task.ID, scopedProjectIDs(identity))
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeCollection(w, items, "")
	case http.MethodPost:
		if !requireNotificationWrite(w, identity) {
			return
		}
		if s.idempotencyReplay(w, r, identity) {
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			watch, err := s.Store.CreateTaskWatch(r.Context(), identity.Actor.ID, task.ID)
			if err != nil {
				return 0, nil, "", err
			}
			w.Header().Set("Location", "/api/v1/watches/"+watch.ID)
			body, err := json.Marshal(watch)
			return http.StatusCreated, body, "", err
		})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}
