package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// releases serves project-scoped release discovery and creation.
//
//	GET  /api/v1/projects/{project}/releases
//	POST /api/v1/projects/{project}/releases
func (s *Server) releases(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectReference string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	requiredScope := "tasks:read"
	if r.Method == http.MethodPost {
		requiredScope = "tasks:write"
	}
	if !requireScope(w, identity, requiredScope) {
		return
	}
	project, err := s.Store.GetProject(r.Context(), projectReference)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !identity.CanProject(project.ID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}

	if r.Method == http.MethodPost {
		if !requireReleaseIdempotencyKey(w, r) || s.idempotencyReplay(w, r, identity) {
			return
		}
		input, decodeErr := decodeReleaseInput(r, true)
		if decodeErr != nil {
			writeReleaseInputError(w, decodeErr)
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			release, createErr := s.Store.CreateRelease(r.Context(), project.ID, input, identity.Actor.ID)
			if createErr != nil {
				return 0, nil, "", createErr
			}
			body, marshalErr := marshalReleaseForIdentity(identity, release)
			if marshalErr != nil {
				return 0, nil, "", marshalErr
			}
			location := "/api/v1/releases/" + release.ID
			w.Header().Set("Location", location)
			return http.StatusCreated, body, releaseETag(release.Version), nil
		})
		return
	}

	limit, offset, paginationErr := parsePagination(r, 50)
	if paginationErr != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", paginationErr.Error(), nil)
		return
	}
	status, err := parseOptionalEnum(r, "status", map[string]struct{}{"planned": {}, "released": {}})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	targetFrom, err := parseOptionalReleaseDate(r, "target_from")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	targetTo, err := parseOptionalReleaseDate(r, "target_to")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if targetFrom != nil && targetTo != nil && *targetFrom > *targetTo {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "target_from must not be after target_to", nil)
		return
	}
	items, more, err := s.Store.ListProjectReleases(r.Context(), project.ID, store.ReleaseFilter{Status: status, TargetFrom: targetFrom, TargetTo: targetTo, Cursor: offset, Limit: limit})
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	next := ""
	if more {
		next = encodeCursor(offset + len(items))
	}
	s.writeCollection(w, items, next)
}

// release serves the release resource and its explicit lifecycle actions.
//
//	GET    /api/v1/releases/{release}
//	PATCH  /api/v1/releases/{release}
//	DELETE /api/v1/releases/{release}
//	POST   /api/v1/releases/{release}/complete
//	POST   /api/v1/releases/{release}/reopen
//	GET    /api/v1/releases/{release}/work-queue
func (s *Server) release(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string, action string) {
	if action == "work-queue" {
		s.releaseWorkQueue(w, r, identity, reference)
		return
	}
	if action == "complete" {
		s.completeRelease(w, r, identity, reference)
		return
	}
	if action == "reopen" {
		s.reopenRelease(w, r, identity, reference)
		return
	}

	var version int64
	switch r.Method {
	case http.MethodGet:
		if !requireScope(w, identity, "tasks:read") {
			return
		}
	case http.MethodPatch, http.MethodDelete:
		if !requireScope(w, identity, "tasks:write") {
			return
		}
		var versionErr error
		version, versionErr = parseVersion(r)
		if versionErr != nil {
			s.writeStoreError(w, versionErr)
			return
		}
		if !requireReleaseIdempotencyKey(w, r) || s.idempotencyReplay(w, r, identity) {
			return
		}
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}

	release, err := s.Store.GetRelease(r.Context(), reference)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if !identity.CanProject(release.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("ETag", releaseETag(release.Version))
		s.writeJSON(w, http.StatusOK, release)
	case http.MethodPatch:
		input, decodeErr := decodeReleaseInput(r, false)
		if decodeErr != nil {
			writeReleaseInputError(w, decodeErr)
			return
		}
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			updated, updateErr := s.Store.UpdateRelease(r.Context(), release.ID, input, version, identity.Actor.ID)
			if updateErr != nil {
				return 0, nil, "", updateErr
			}
			body, marshalErr := marshalReleaseForIdentity(identity, updated)
			if marshalErr != nil {
				return 0, nil, "", marshalErr
			}
			return http.StatusOK, body, releaseETag(updated.Version), nil
		})
	case http.MethodDelete:
		s.mutation(w, r, identity, func() (int, []byte, string, error) {
			if deleteErr := s.Store.DeleteRelease(r.Context(), release.ID, version, identity.Actor.ID); deleteErr != nil {
				return 0, nil, "", deleteErr
			}
			return http.StatusNoContent, nil, "", nil
		})
	}
}

func (s *Server) completeRelease(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodPost {
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
	if !requireReleaseIdempotencyKey(w, r) || s.idempotencyReplay(w, r, identity) {
		return
	}
	release, err := s.Store.GetRelease(r.Context(), reference)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if !identity.CanProject(release.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		updated, completeErr := s.Store.CompleteRelease(r.Context(), release.ID, version, identity.Actor.ID)
		if completeErr != nil {
			return 0, nil, "", completeErr
		}
		body, marshalErr := marshalReleaseForIdentity(identity, updated)
		if marshalErr != nil {
			return 0, nil, "", marshalErr
		}
		return http.StatusOK, body, releaseETag(updated.Version), nil
	})
}

func (s *Server) reopenRelease(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodPost {
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
	if !requireReleaseIdempotencyKey(w, r) || s.idempotencyReplay(w, r, identity) {
		return
	}
	release, err := s.Store.GetRelease(r.Context(), reference)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if !identity.CanProject(release.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	reason, decodeErr := decodeReleaseReopenReason(r)
	if decodeErr != nil {
		writeReleaseInputError(w, decodeErr)
		return
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		updated, reopenErr := s.Store.ReopenReleaseWithReason(r.Context(), release.ID, reason, version, identity.Actor.ID)
		if reopenErr != nil {
			return 0, nil, "", reopenErr
		}
		body, marshalErr := marshalReleaseForIdentity(identity, updated)
		if marshalErr != nil {
			return 0, nil, "", marshalErr
		}
		return http.StatusOK, body, releaseETag(updated.Version), nil
	})
}

func (s *Server) releaseWorkQueue(w http.ResponseWriter, r *http.Request, identity auth.Identity, reference string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:read") {
		return
	}
	workRelease, err := s.Store.GetRelease(r.Context(), reference)
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	if !identity.CanProject(workRelease.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	limit, paginationErr := parseLimit(r, 50)
	if paginationErr != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", paginationErr.Error(), nil)
		return
	}
	cursor, _, err := queryValue(r, "cursor")
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	queue, err := s.Store.GetReleaseWorkQueue(r.Context(), workRelease.ID, identity.Actor.ID, store.ReleaseWorkQueueFilter{Cursor: cursor, Limit: limit})
	if err != nil {
		s.writeStoreErrorForIdentity(w, identity, err)
		return
	}
	s.writeJSON(w, http.StatusOK, queue)
}

func decodeReleaseInput(r *http.Request, creating bool) (store.ReleaseInput, error) {
	var payload struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		TargetDate  *string `json:"target_date"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		return store.ReleaseInput{}, err
	}
	if creating {
		if err := requireJSONFields(fields, "name"); err != nil {
			return store.ReleaseInput{}, err
		}
	} else if len(fields) == 0 {
		return store.ReleaseInput{}, taskInputError("patch must include at least one release field")
	}
	if jsonFieldNull(fields, "name") {
		return store.ReleaseInput{}, taskInputError("name cannot be null")
	}
	input := store.ReleaseInput{Name: payload.Name}
	if raw, ok := fields["description"]; ok {
		input.Description = payload.Description
		input.DescriptionSet = true
		if !isJSONNull(raw) && payload.Description == nil {
			return store.ReleaseInput{}, taskInputError("description must be a string or null")
		}
	}
	if raw, ok := fields["target_date"]; ok {
		input.TargetDate = payload.TargetDate
		input.TargetDateSet = true
		if !isJSONNull(raw) && payload.TargetDate == nil {
			return store.ReleaseInput{}, taskInputError("target_date must be an ISO YYYY-MM-DD date or null")
		}
	}
	if !creating && input.Name == nil && !input.DescriptionSet && !input.TargetDateSet {
		return store.ReleaseInput{}, taskInputError("patch must include at least one release field")
	}
	return input, nil
}

func decodeReleaseReopenReason(r *http.Request) (string, error) {
	var payload struct {
		Reason *string `json:"reason"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		return "", err
	}
	if err := requireJSONFields(fields, "reason"); err != nil {
		return "", err
	}
	if payload.Reason == nil || strings.TrimSpace(*payload.Reason) == "" {
		return "", taskInputError("reopen reason is required")
	}
	return strings.TrimSpace(*payload.Reason), nil
}

func parseOptionalReleaseDate(r *http.Request, name string) (*string, error) {
	value, present, err := queryValue(r, name)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New(name + " must be an ISO YYYY-MM-DD date")
	}
	if len(value) != len("2006-01-02") {
		return nil, errors.New(name + " must be an ISO YYYY-MM-DD date")
	}
	if _, err := parseReleaseDate(value); err != nil {
		return nil, errors.New(name + " must be an ISO YYYY-MM-DD date")
	}
	return &value, nil
}

// parseProjectReleaseFilter resolves the project-local convenience `release`
// query parameter to an opaque ID before a task collection reaches the store.
// The unassigned sentinel remains a first-class value and avoids a release
// lookup for a NULL membership query.
func (s *Server) parseProjectReleaseFilter(r *http.Request, projectID string) (string, error) {
	value, present, err := queryValue(r, "release")
	if err != nil || !present {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("release must not be empty")
	}
	if strings.EqualFold(value, "unassigned") || strings.EqualFold(value, "none") {
		return "unassigned", nil
	}
	release, err := s.Store.ResolveReleaseReference(r.Context(), projectID, value)
	if err != nil {
		return "", err
	}
	return release.ID, nil
}

// resolveGlobalReleaseFilter canonicalizes the stable release_id query form.
// Unlike the project-local `release` form, a global identifier is never
// interpreted as a release name. Resolving it before a collection query also
// applies the caller's project ceiling before any matching task can be read.
func (s *Server) resolveGlobalReleaseFilter(r *http.Request, identity auth.Identity, value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "unassigned") || strings.EqualFold(value, "none") {
		return "unassigned", nil
	}
	release, err := s.Store.GetRelease(r.Context(), value)
	if err != nil {
		return "", err
	}
	if !identity.CanProject(release.ProjectID) {
		return "", &store.Error{Kind: store.ErrForbidden, Message: "token is not scoped to this project"}
	}
	return release.ID, nil
}

func parseReleaseDate(value string) (string, error) {
	// Keep date parsing in the HTTP package independent from store internals;
	// the store repeats the validation before querying.
	if len(value) != 10 || value[4] != '-' || value[7] != '-' {
		return "", errors.New("invalid date")
	}
	for index, character := range value {
		if index == 4 || index == 7 {
			continue
		}
		if character < '0' || character > '9' {
			return "", errors.New("invalid date")
		}
	}
	if parsed, err := time.Parse("2006-01-02", value); err != nil || parsed.Format("2006-01-02") != value {
		return "", errors.New("invalid date")
	}
	return value, nil
}

func releaseETag(version int64) string { return `"v` + formatInt64(version) + `"` }

func formatInt64(value int64) string {
	// strconv.FormatInt is kept behind this tiny helper to make the ETag format
	// parallel with taskETag while avoiding conversion through float values.
	return strconv.FormatInt(value, 10)
}

func marshalReleaseForIdentity(identity auth.Identity, release store.Release) ([]byte, error) {
	if identity.IsToken && !identity.HasScope("tasks:read") {
		return json.Marshal(map[string]any{"id": release.ID, "version": release.Version})
	}
	return json.Marshal(release)
}

func requireReleaseIdempotencyKey(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		sDummyWriteError(w, http.StatusBadRequest, "idempotency_required", "Idempotency-Key is required for release mutations", nil)
		return false
	}
	return true
}

func writeReleaseInputError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrInvalid) {
		sDummyWriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	sDummyWriteError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
}
