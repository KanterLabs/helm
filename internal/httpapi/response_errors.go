package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
)

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	s.writeStoreErrorForIdentity(w, auth.Identity{}, err)
}

// writeStoreErrorForIdentity preserves the detailed task snapshot for human
// callers and bearer credentials with tasks:read. Mutation and action
// conflicts are also returned to write-only/claim-only credentials, but their
// current task snapshot is reduced to retry metadata so read-protected task
// fields cannot be used as a side channel.
func (s *Server) writeStoreErrorForIdentity(w http.ResponseWriter, identity auth.Identity, err error) {
	if err == nil {
		return
	}
	status, code, message, details := http.StatusInternalServerError, "internal_error", "internal server error", any(map[string]any{})
	switch {
	case errors.Is(err, store.ErrDependencySelfReference):
		status, code, message = http.StatusBadRequest, "dependency_self_reference", err.Error()
	case errors.Is(err, store.ErrDependencyCrossProject):
		status, code, message = http.StatusBadRequest, "dependency_cross_project", err.Error()
	case errors.Is(err, store.ErrDependencyAlreadyExists):
		status, code, message = http.StatusConflict, "dependency_already_exists", err.Error()
	case errors.Is(err, store.ErrDependencyLimitExceeded):
		status, code, message = http.StatusBadRequest, "dependency_limit_exceeded", err.Error()
	case errors.Is(err, store.ErrDependencyCycle):
		status, code, message = http.StatusConflict, "dependency_cycle", err.Error()
	case errors.Is(err, store.ErrDependencyNotFound):
		status, code, message = http.StatusNotFound, "dependency_not_found", err.Error()
	case errors.Is(err, store.ErrUnmetDependencies):
		status, code, message = http.StatusConflict, "unmet_dependencies", err.Error()
	case errors.Is(err, store.ErrDependencyInUse):
		status, code, message = http.StatusConflict, "dependency_in_use", err.Error()
	case errors.Is(err, store.ErrChecklistLimitExceeded):
		status, code, message = http.StatusBadRequest, "checklist_limit_exceeded", "checklist payload exceeds the configured limit"
	case errors.Is(err, store.ErrChecklistIncomplete):
		status, code, message = http.StatusConflict, "checklist_incomplete", err.Error()
	case errors.Is(err, store.ErrHierarchySelfReference):
		status, code, message = http.StatusBadRequest, "hierarchy_self_reference", err.Error()
	case errors.Is(err, store.ErrHierarchyCrossProject):
		status, code, message = http.StatusBadRequest, "hierarchy_cross_project", err.Error()
	case errors.Is(err, store.ErrHierarchyAlreadyExists):
		status, code, message = http.StatusConflict, "hierarchy_already_exists", err.Error()
	case errors.Is(err, store.ErrHierarchyFanoutExceeded), errors.Is(err, store.ErrHierarchyLimitExceeded):
		status, code, message = http.StatusBadRequest, "hierarchy_limit_exceeded", err.Error()
	case errors.Is(err, store.ErrHierarchyDepthExceeded):
		status, code, message = http.StatusBadRequest, "hierarchy_depth_exceeded", err.Error()
	case errors.Is(err, store.ErrHierarchyCycle):
		status, code, message = http.StatusConflict, "hierarchy_cycle", err.Error()
	case errors.Is(err, store.ErrHierarchyNotFound):
		status, code, message = http.StatusNotFound, "hierarchy_not_found", err.Error()
	case errors.Is(err, store.ErrHierarchyInUse):
		status, code, message = http.StatusConflict, "hierarchy_in_use", err.Error()
	case errors.Is(err, store.ErrReleaseNotFound):
		status, code, message = http.StatusNotFound, "release_not_found", err.Error()
	case errors.Is(err, store.ErrReleaseNameExists):
		status, code, message = http.StatusConflict, "release_name_exists", err.Error()
	case errors.Is(err, store.ErrReleaseCrossProject):
		status, code, message = http.StatusBadRequest, "release_cross_project", err.Error()
	case errors.Is(err, store.ErrReleaseAlreadyCompleted):
		status, code, message = http.StatusConflict, "release_already_completed", err.Error()
	case errors.Is(err, store.ErrReleaseNotCompleted):
		status, code, message = http.StatusConflict, "release_not_completed", err.Error()
	case errors.Is(err, store.ErrReleaseHasTasks):
		status, code, message = http.StatusConflict, "release_has_tasks", err.Error()
	case errors.Is(err, store.ErrReleaseIncomplete):
		status, code, message = http.StatusConflict, "release_incomplete", err.Error()
	case errors.Is(err, store.ErrReleaseDependencyConflict):
		status, code, message = http.StatusConflict, "release_dependency_conflict", err.Error()
	case errors.Is(err, store.ErrReleaseFrozen):
		status, code, message = http.StatusConflict, "release_frozen", err.Error()
	case errors.Is(err, store.ErrReleaseQueueChanged):
		status, code, message = http.StatusConflict, "release_queue_changed", err.Error()
	case errors.Is(err, store.ErrInvalid):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, store.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", err.Error()
	case errors.Is(err, store.ErrTaskCollectionChanged):
		status, code, message = http.StatusConflict, "task_collection_changed", err.Error()
	case errors.Is(err, store.ErrConflict):
		status, code, message = http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, store.ErrAlreadyExists):
		status, code, message = http.StatusConflict, "already_exists", err.Error()
	case errors.Is(err, store.ErrPrecondition):
		status, code, message = http.StatusPreconditionRequired, "if_match_required", "If-Match is required"
	case errors.Is(err, store.ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", err.Error()
	case errors.Is(err, store.ErrClaimUnavailable):
		status, code, message = http.StatusConflict, "task_already_claimed", err.Error()
	}
	var typed *store.Error
	if errors.As(err, &typed) && typed.Details != nil {
		details = typed.Details
	}
	if errors.Is(err, store.ErrConflict) {
		if strings.Contains(strings.ToLower(err.Error()), "idempotency") {
			code = "idempotency_key_reused"
		}
		if typed != nil {
			if detailMap, ok := typed.Details.(map[string]any); ok {
				if _, hasCurrent := detailMap["current"]; hasCurrent {
					code = "stale_task"
				}
			}
		}
		if strings.Contains(strings.ToLower(err.Error()), "already configured") {
			code = "setup_already_complete"
		}
	}
	details = redactTaskConflictDetails(identity, details)
	details = redactDependencyDetails(identity, err, details)
	details = redactReleaseDetails(identity, err, details)
	s.writeError(w, status, code, message, details)
}

// redactReleaseDetails keeps a write-only bearer from learning release names,
// task counts, or dependency metadata through a lifecycle error. A queue
// restart marker and optimistic versions are safe retry metadata; everything
// else requires the tasks:read capability.
func redactReleaseDetails(identity auth.Identity, err error, details any) any {
	if !identity.IsToken || identity.HasScope("tasks:read") || !isReleaseError(err) {
		return details
	}
	value, ok := details.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	redacted := make(map[string]any)
	for _, key := range []string{"current_version", "expected_version", "restart"} {
		if item, exists := value[key]; exists {
			redacted[key] = item
		}
	}
	return redacted
}

func isReleaseError(err error) bool {
	for _, candidate := range []error{
		store.ErrReleaseNotFound,
		store.ErrReleaseNameExists,
		store.ErrReleaseCrossProject,
		store.ErrReleaseAlreadyCompleted,
		store.ErrReleaseNotCompleted,
		store.ErrReleaseHasTasks,
		store.ErrReleaseIncomplete,
		store.ErrReleaseDependencyConflict,
		store.ErrReleaseFrozen,
		store.ErrReleaseQueueChanged,
	} {
		if errors.Is(err, candidate) {
			return true
		}
	}
	return false
}

// redactDependencyDetails keeps dependency error envelopes useful to tokens
// that can mutate a task but cannot read task content. Relation/path details
// may contain keys, titles, completion timestamps, and graph state for a
// second task; those fields, including opaque relation IDs, are removed. This
// deliberately fails closed because a write-only token has no read contract
// for either side of the relation.
func redactDependencyDetails(identity auth.Identity, err error, details any) any {
	if !identity.IsToken || identity.HasScope("tasks:read") || (!isDependencyError(err) && !isHierarchyError(err)) {
		return details
	}
	// Store error details are intentionally small maps today, but future
	// lifecycle guards may return typed TaskReference slices. Normalize through
	// JSON first so the redaction policy covers both representations and fails
	// closed if a detail value cannot be serialized.
	encoded, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		return map[string]any{}
	}
	var generic any
	if unmarshalErr := json.Unmarshal(encoded, &generic); unmarshalErr != nil {
		return map[string]any{}
	}
	return redactDependencyValue(generic)
}

func isDependencyError(err error) bool {
	for _, candidate := range []error{
		store.ErrDependencySelfReference,
		store.ErrDependencyCrossProject,
		store.ErrDependencyAlreadyExists,
		store.ErrDependencyLimitExceeded,
		store.ErrDependencyCycle,
		store.ErrDependencyNotFound,
		store.ErrUnmetDependencies,
		store.ErrDependencyInUse,
	} {
		if errors.Is(err, candidate) {
			return true
		}
	}
	return false
}

func isHierarchyError(err error) bool {
	for _, candidate := range []error{
		store.ErrHierarchySelfReference,
		store.ErrHierarchyCrossProject,
		store.ErrHierarchyAlreadyExists,
		store.ErrHierarchyLimitExceeded,
		store.ErrHierarchyFanoutExceeded,
		store.ErrHierarchyDepthExceeded,
		store.ErrHierarchyCycle,
		store.ErrHierarchyNotFound,
		store.ErrHierarchyInUse,
	} {
		if errors.Is(err, candidate) {
			return true
		}
	}
	return false
}

func redactDependencyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, child := range typed {
			switch key {
			case "id", "key", "title", "completed_at", "satisfied", "task_id", "prerequisite_id", "dependent_id", "prerequisite_task_id", "task_key", "task_title", "prerequisite_key", "dependent_key", "column_id", "path", "path_ids", "task_project_id", "prerequisite_project_id", "parent_id", "parent_task_id", "child_id", "child_task_id", "parent_key", "child_key", "parent_project_id", "child_project_id":
				continue
			default:
				redacted[key] = redactDependencyValue(child)
			}
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, child := range typed {
			redacted[index] = redactDependencyValue(child)
		}
		return redacted
	default:
		return value
	}
}

func redactTaskConflictDetails(identity auth.Identity, details any) any {
	if !identity.IsToken || identity.HasScope("tasks:read") {
		return details
	}
	detailMap, ok := details.(map[string]any)
	if !ok {
		return details
	}
	current, ok := detailMap["current"]
	if !ok {
		// Task and column mutations can fail because an active claim exists.
		// Store diagnostics intentionally carry the blocking task ID, claimant,
		// and lease expiry for human diagnostics, but none of those fields are
		// readable by a bearer lacking tasks:read. Return an empty details object
		// (or the explicitly safe column ID) while preserving status and code.
		if hasSensitiveClaimDetail(detailMap) {
			redacted := make(map[string]any)
			if columnID, hasColumnID := detailMap["column_id"]; hasColumnID {
				redacted["column_id"] = columnID
			}
			return redacted
		}
		return details
	}
	var redacted map[string]any
	switch task := current.(type) {
	case store.Task:
		redacted = redactedTaskConflictCurrent(task)
	case *store.Task:
		if task == nil {
			return details
		}
		redacted = redactedTaskConflictCurrent(*task)
	case store.AuditFinding:
		redacted = map[string]any{"id": task.ID, "version": task.Version}
	case *store.AuditFinding:
		if task == nil {
			return details
		}
		redacted = map[string]any{"id": task.ID, "version": task.Version}
	default:
		return details
	}
	copyDetails := make(map[string]any, len(detailMap))
	for key, value := range detailMap {
		if isSensitiveClaimDetail(key) {
			continue
		}
		copyDetails[key] = value
	}
	copyDetails["current"] = redacted
	return copyDetails
}

func hasSensitiveClaimDetail(details map[string]any) bool {
	for key := range details {
		if isSensitiveClaimDetail(key) {
			return true
		}
	}
	return false
}

func isSensitiveClaimDetail(key string) bool {
	switch key {
	case "task_id", "claimed_by", "claim_expires_at":
		return true
	default:
		return false
	}
}

func redactedTaskConflictCurrent(task store.Task) map[string]any {
	// Keep only values needed to identify the stale version and retry against
	// it. In particular, do not include title, description, labels, assignee,
	// priority, column, timestamps, or claim metadata.
	return map[string]any{
		"id":      task.ID,
		"version": task.Version,
	}
}
