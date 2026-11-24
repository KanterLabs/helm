package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// bulkTask handles POST /api/v1/projects/{project}/tasks/bulk.  Each item is
// versioned independently; the store owns the transaction and lifecycle
// boundaries so a project-scoped request cannot turn into an unguarded loop
// of single-task calls.
func (s *Server) bulkTask(w http.ResponseWriter, r *http.Request, identity auth.Identity, projectReference string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:write") {
		return
	}
	// Check replays before resolving the mutable project key. A retry must
	// return its original bounded response even after an administrator renames
	// the project key; authentication and write scope were already enforced.
	if s.idempotencyReplay(w, r, identity) {
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
	mode, mutations, err := decodeBulkTaskRequest(r)
	if err != nil {
		writeTaskInputError(w, err)
		return
	}
	for index := range mutations {
		mutation := &mutations[index]
		operation := strings.ToLower(strings.TrimSpace(mutation.Operation))
		if (operation == "complete" || operation == "block") && !requireScope(w, identity, "tasks:claim") {
			return
		}
		// Bearer lifecycle writers must hold the task claim, matching the
		// single-task action routes. Human sessions retain their existing
		// lifecycle behavior; an administrator's explicit claim override still
		// applies to ordinary field writes.
		if operation == "complete" || operation == "block" {
			mutation.RequireClaim = identity.IsToken
		}
	}

	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		batch, err := s.Store.ApplyBulkTaskMutations(r.Context(), project.ID, identity.Actor.ID, mode, mutations, !identity.IsToken && identity.Actor.Admin, agentAdmissionSession(r, identity)...)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := marshalBulkTaskResponse(identity, batch)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusOK, body, "", nil
	})
}

type bulkTaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
	Details any    `json:"details"`
}

type bulkTaskItemResponse struct {
	TaskID    string          `json:"task_id,omitempty"`
	Reference string          `json:"reference"`
	Status    string          `json:"status"`
	Version   int64           `json:"version,omitempty"`
	Task      json.RawMessage `json:"task,omitempty"`
	Error     *bulkTaskError  `json:"error,omitempty"`
}

type bulkTaskResponse struct {
	Mode      string                 `json:"mode"`
	Status    string                 `json:"status"`
	Requested int                    `json:"requested"`
	Applied   int                    `json:"applied"`
	Skipped   int                    `json:"skipped"`
	Conflicts int                    `json:"conflicts"`
	Results   []bulkTaskItemResponse `json:"results"`
}

func marshalBulkTaskResponse(identity auth.Identity, batch store.BulkTaskMutationBatch) ([]byte, error) {
	response := bulkTaskResponse{
		Mode: batch.Mode, Requested: len(batch.Results), Results: make([]bulkTaskItemResponse, 0, len(batch.Results)),
	}
	response.Status = "complete"
	if batch.Mode == "partial" {
		response.Status = "partial"
	}
	for _, result := range batch.Results {
		item := bulkTaskItemResponse{TaskID: result.TaskID, Reference: result.Reference, Status: result.Status}
		if result.Status == "applied" {
			response.Applied++
			if result.Task != nil {
				item.Version = result.Task.Version
				body, err := marshalTaskForIdentity(identity, *result.Task)
				if err != nil {
					return nil, err
				}
				item.Task = body
			}
		} else {
			if result.Status == "conflict" {
				response.Conflicts++
			} else {
				response.Skipped++
			}
			if result.Err != nil {
				item.Error = bulkTaskErrorForIdentity(identity, result.Err)
			}
		}
		response.Results = append(response.Results, item)
	}
	if batch.Mode == "atomic" && response.Applied == 0 {
		for _, item := range response.Results {
			if item.Error != nil && item.Error.Code != "atomic_rollback" {
				response.Status = "failed"
				break
			}
		}
	}
	return json.Marshal(response)
}

func bulkTaskErrorForIdentity(identity auth.Identity, err error) *bulkTaskError {
	status, code, message, details := http.StatusInternalServerError, "internal_error", "internal server error", any(map[string]any{})
	switch {
	case errors.Is(err, store.ErrUnmetDependencies):
		status, code, message = http.StatusConflict, "unmet_dependencies", err.Error()
	case errors.Is(err, store.ErrDependencyInUse):
		status, code, message = http.StatusConflict, "dependency_in_use", err.Error()
	case errors.Is(err, store.ErrChecklistIncomplete):
		status, code, message = http.StatusConflict, "checklist_incomplete", err.Error()
	case errors.Is(err, store.ErrClaimUnavailable):
		status, code, message = http.StatusConflict, "task_already_claimed", err.Error()
	case errors.Is(err, store.ErrConflict):
		status, code, message = http.StatusConflict, "conflict", err.Error()
		if strings.Contains(strings.ToLower(err.Error()), "atomic batch rolled back") {
			status, code, message = http.StatusConflict, "atomic_rollback", "atomic batch rolled back"
		}
	case errors.Is(err, store.ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", err.Error()
	case errors.Is(err, store.ErrInvalid):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, store.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", err.Error()
	case errors.Is(err, store.ErrPrecondition):
		status, code, message = http.StatusPreconditionRequired, "if_match_required", "If-Match is required"
	}
	var typed *store.Error
	if errors.As(err, &typed) && typed.Details != nil {
		details = typed.Details
	}
	details = redactTaskConflictDetails(identity, details)
	details = redactDependencyDetails(identity, err, details)
	return &bulkTaskError{Code: code, Message: message, Status: status, Details: details}
}
