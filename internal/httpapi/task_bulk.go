package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		batch, err := s.Store.ApplyBulkTaskMutations(r.Context(), project.ID, identity.Actor.ID, mode, mutations, !identity.IsToken && identity.Actor.Admin)
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

func decodeBulkTaskRequest(r *http.Request) (string, []store.BulkTaskMutation, error) {
	var fields map[string]json.RawMessage
	decoded, err := decodeJSONObject(r, &fields)
	if err != nil {
		return "", nil, err
	}
	fields = decoded
	for name := range fields {
		if name != "mode" && name != "atomic" && name != "mutations" && name != "items" {
			return "", nil, taskInputError("unknown bulk task field: " + name)
		}
	}
	mode := "partial"
	if raw, ok := fields["mode"]; ok {
		value, err := parseTaskString(raw, "mode", false)
		if err != nil {
			return "", nil, err
		}
		mode = strings.ToLower(strings.TrimSpace(*value))
	}
	if raw, ok := fields["atomic"]; ok {
		var atomic bool
		if err := json.Unmarshal(raw, &atomic); err != nil {
			return "", nil, err
		}
		if atomic {
			if _, hasMode := fields["mode"]; hasMode && mode != "atomic" {
				return "", nil, taskInputError("atomic and mode must agree")
			}
			mode = "atomic"
		} else if _, hasMode := fields["mode"]; hasMode && mode == "atomic" {
			return "", nil, taskInputError("atomic and mode must agree")
		}
	}
	if mode != "partial" && mode != "atomic" {
		return "", nil, taskInputError("mode must be atomic or partial")
	}
	rawItems, hasMutations := fields["mutations"]
	if alternate, hasItems := fields["items"]; hasItems {
		if hasMutations {
			return "", nil, taskInputError("send mutations or items, not both")
		}
		rawItems, hasMutations = alternate, true
	}
	if !hasMutations || isJSONNull(rawItems) {
		return "", nil, taskInputError("mutations is required")
	}
	var rawMutations []json.RawMessage
	if err := json.Unmarshal(rawItems, &rawMutations); err != nil {
		return "", nil, err
	}
	if len(rawMutations) == 0 {
		return "", nil, taskInputError("mutations must contain at least one item")
	}
	if len(rawMutations) > store.BulkTaskMutationLimit {
		return "", nil, taskInputError(fmt.Sprintf("mutations must contain at most %d items", store.BulkTaskMutationLimit))
	}
	mutations := make([]store.BulkTaskMutation, 0, len(rawMutations))
	for index, raw := range rawMutations {
		mutation, err := decodeBulkTaskMutation(raw)
		if err != nil {
			return "", nil, taskInputError(fmt.Sprintf("mutation %d: %s", index+1, err))
		}
		mutations = append(mutations, mutation)
	}
	return mode, mutations, nil
}

func decodeBulkTaskMutation(raw json.RawMessage) (store.BulkTaskMutation, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return store.BulkTaskMutation{}, taskInputError("mutation must be an object")
	}
	allowed := map[string]bool{
		"task": true, "task_id": true, "reference": true, "id": true,
		"version": true, "expected_version": true, "operation": true, "action": true, "type": true,
		"changes": true, "patch": true,
		"destination_column_id": true, "destination_column": true, "to_column_id": true, "to_column": true,
		"column_id": true, "column": true, "expected_source_column_id": true, "source_column_id": true,
		"from_column_id": true, "expected_column_id": true, "source_column": true, "from_column": true,
		"source": true, "reason": true, "comment": true, "assignee": true, "assignee_id": true,
		"priority": true, "labels": true, "label_ids": true, "due_at": true, "due_date": true,
	}
	for name := range fields {
		if !allowed[name] {
			return store.BulkTaskMutation{}, taskInputError("unknown mutation field: " + name)
		}
	}
	if nested, ok := fields["changes"]; ok {
		if err := mergeBulkFields(fields, nested); err != nil {
			return store.BulkTaskMutation{}, err
		}
	}
	if nested, ok := fields["patch"]; ok {
		if err := mergeBulkFields(fields, nested); err != nil {
			return store.BulkTaskMutation{}, err
		}
	}
	// Nested change objects are merged before validating the operation so an
	// unsupported nested key cannot be silently ignored by lifecycle actions.
	for name := range fields {
		if !allowed[name] {
			return store.BulkTaskMutation{}, taskInputError("unknown mutation field: " + name)
		}
	}
	reference, err := parseBulkAliases(fields, "task", []string{"task", "task_id", "reference", "id"}, true)
	if err != nil {
		return store.BulkTaskMutation{}, err
	}
	version, err := parseBulkVersion(fields)
	if err != nil {
		return store.BulkTaskMutation{}, err
	}
	operation, found, err := parseBulkAliasesOptional(fields, "operation", []string{"operation", "action", "type"})
	if err != nil {
		return store.BulkTaskMutation{}, err
	}
	if found {
		operation = normalizeBulkOperation(operation)
	} else {
		operation = inferBulkOperation(fields)
	}
	if err := validateBulkOperationFields(fields, operation); err != nil {
		return store.BulkTaskMutation{}, err
	}
	mutation := store.BulkTaskMutation{Reference: reference, ExpectedVersion: version, Operation: operation}
	switch operation {
	case "move":
		destination, err := parseBulkAliases(fields, "destination column", []string{"destination_column_id", "destination_column", "to_column_id", "to_column", "column_id", "column"}, true)
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		source, err := parseBulkAliases(fields, "expected source column", []string{"expected_source_column_id", "source_column_id", "from_column_id", "expected_column_id", "source_column", "from_column"}, true)
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		sourceLabel, sourceFound, parseErr := parseBulkAliasesOptional(fields, "source", []string{"source"})
		if parseErr != nil {
			return store.BulkTaskMutation{}, parseErr
		}
		if !sourceFound || sourceLabel == "" {
			return store.BulkTaskMutation{}, taskInputError("source is required")
		}
		reason := ""
		if value, ok, parseErr := parseBulkAliasesOptional(fields, "reason", []string{"reason"}); parseErr != nil {
			return store.BulkTaskMutation{}, parseErr
		} else if ok {
			reason = value
		}
		mutation.Move = &store.TaskMoveInput{DestinationColumnID: destination, ExpectedSourceColumnID: source, Source: sourceLabel, Reason: reason}
	case "complete":
		if value, ok, parseErr := parseBulkAliasesOptional(fields, "comment", []string{"comment"}); parseErr != nil {
			return store.BulkTaskMutation{}, parseErr
		} else if ok {
			mutation.Note = value
		}
		mutation.RequireClaim = true
	case "block":
		if value, ok, parseErr := parseBulkAliasesOptional(fields, "reason", []string{"reason", "comment"}); parseErr != nil {
			return store.BulkTaskMutation{}, parseErr
		} else if ok {
			mutation.Note = value
		}
		mutation.RequireClaim = true
	case "assign":
		input, err := decodeBulkTaskInput(fields, "assign")
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		mutation.Input = input
	case "priority":
		input, err := decodeBulkTaskInput(fields, "priority")
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		mutation.Input = input
	case "labels":
		input, err := decodeBulkTaskInput(fields, "labels")
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		mutation.Input = input
	case "due_at":
		input, err := decodeBulkTaskInput(fields, "due_at")
		if err != nil {
			return store.BulkTaskMutation{}, err
		}
		mutation.Input = input
	default:
		return store.BulkTaskMutation{}, taskInputError("operation is unsupported")
	}
	return mutation, nil
}

func validateBulkOperationFields(fields map[string]json.RawMessage, operation string) error {
	for name := range fields {
		switch name {
		case "task", "task_id", "reference", "id", "version", "expected_version", "operation", "action", "type", "changes", "patch":
			continue
		}
		allowed := false
		switch operation {
		case "move":
			allowed = name == "destination_column_id" || name == "destination_column" || name == "to_column_id" || name == "to_column" || name == "column_id" || name == "column" || name == "expected_source_column_id" || name == "source_column_id" || name == "from_column_id" || name == "expected_column_id" || name == "source_column" || name == "from_column" || name == "source" || name == "reason"
		case "complete":
			allowed = name == "comment"
		case "block":
			allowed = name == "reason" || name == "comment"
		case "assign":
			allowed = name == "assignee" || name == "assignee_id"
		case "priority":
			allowed = name == "priority"
		case "labels":
			allowed = name == "labels" || name == "label_ids"
		case "due_at":
			allowed = name == "due_at" || name == "due_date"
		}
		if !allowed {
			return taskInputError("field " + name + " is not supported by this operation")
		}
	}
	return nil
}

func mergeBulkFields(fields map[string]json.RawMessage, raw json.RawMessage) error {
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil || nested == nil {
		return taskInputError("changes must be an object")
	}
	for name, value := range nested {
		if existing, ok := fields[name]; ok && string(existing) != string(value) {
			return taskInputError(name + " aliases must agree")
		}
		fields[name] = value
	}
	return nil
}

func parseBulkVersion(fields map[string]json.RawMessage) (int64, error) {
	raw, ok := fields["version"]
	if alternate, hasAlternate := fields["expected_version"]; hasAlternate {
		if ok && string(raw) != string(alternate) {
			return 0, taskInputError("version aliases must agree")
		}
		raw, ok = alternate, true
	}
	if !ok || isJSONNull(raw) {
		return 0, taskInputError("version is required")
	}
	var version int64
	if err := json.Unmarshal(raw, &version); err != nil || version <= 0 {
		return 0, taskInputError("version must be a positive integer")
	}
	return version, nil
}

func parseBulkAliases(fields map[string]json.RawMessage, label string, names []string, required bool) (string, error) {
	value, found, err := parseBulkAliasesOptional(fields, label, names)
	if err != nil {
		return "", err
	}
	if !found {
		if required {
			return "", taskInputError(label + " is required")
		}
		return "", nil
	}
	if value == "" {
		return "", taskInputError(label + " must not be empty")
	}
	return value, nil
}

func parseBulkAliasesOptional(fields map[string]json.RawMessage, label string, names []string) (string, bool, error) {
	value, found := "", false
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		parsed, err := parseTaskString(raw, name, false)
		if err != nil {
			return "", false, err
		}
		parsedValue := strings.TrimSpace(*parsed)
		if found && parsedValue != value {
			return "", false, taskInputError(label + " aliases must agree")
		}
		value, found = parsedValue, true
	}
	return value, found, nil
}

func normalizeBulkOperation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "assignment", "assignee":
		return "assign"
	case "label", "labeling", "label_update":
		return "labels"
	case "due", "due_date", "deadline":
		return "due_at"
	case "completion", "done":
		return "complete"
	case "blocking", "blocked":
		return "block"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func inferBulkOperation(fields map[string]json.RawMessage) string {
	for _, names := range [][]string{{"destination_column_id", "destination_column", "to_column_id", "to_column"}, {"assignee", "assignee_id"}, {"priority"}, {"labels", "label_ids"}, {"due_at", "due_date"}} {
		for _, name := range names {
			if _, ok := fields[name]; ok {
				if name == "destination_column_id" || name == "destination_column" || name == "to_column_id" || name == "to_column" {
					return "move"
				}
				if name == "assignee" || name == "assignee_id" {
					return "assign"
				}
				if name == "priority" {
					return "priority"
				}
				if name == "labels" || name == "label_ids" {
					return "labels"
				}
				return "due_at"
			}
		}
	}
	if _, ok := fields["comment"]; ok {
		return "complete"
	}
	if _, ok := fields["reason"]; ok {
		return "block"
	}
	return "unsupported"
}

func decodeBulkTaskInput(fields map[string]json.RawMessage, operation string) (store.TaskInput, error) {
	var input store.TaskInput
	allowed := map[string]bool{"title": true, "description": true, "priority": true, "assignee": true, "assignee_id": true, "due_at": true, "due_date": true, "labels": true, "label_ids": true}
	for name := range fields {
		switch name {
		case "task", "task_id", "reference", "id", "version", "expected_version", "operation", "action", "type", "changes", "patch", "source", "reason", "comment", "destination_column_id", "destination_column", "to_column_id", "to_column", "column_id", "column", "expected_source_column_id", "source_column_id", "from_column_id", "expected_column_id", "source_column", "from_column":
			continue
		default:
			if !allowed[name] {
				return store.TaskInput{}, taskInputError("field " + name + " is not supported by this operation")
			}
		}
	}
	expected := map[string]string{"assign": "assignee", "priority": "priority", "labels": "labels", "due_at": "due_at"}[operation]
	if operation == "assign" {
		raw, ok := fields["assignee"]
		if alternate, alternateOK := fields["assignee_id"]; alternateOK {
			if ok && string(raw) != string(alternate) {
				return store.TaskInput{}, taskInputError("assignee aliases must agree")
			}
			raw, ok = alternate, true
		}
		if !ok {
			return store.TaskInput{}, taskInputError("assignee is required")
		}
		value, err := parseTaskString(raw, "assignee", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		input.Assignee, input.AssigneeSet = value, true
		return input, nil
	}
	if operation == "due_at" {
		if raw, ok := fields["due_date"]; ok {
			if existing, hasExisting := fields["due_at"]; hasExisting && string(existing) != string(raw) {
				return store.TaskInput{}, taskInputError("due date aliases must agree")
			}
			fields["due_at"] = raw
		}
	}
	if expected != "" {
		if _, ok := fields[expected]; !ok && !(expected == "labels" && fields["label_ids"] != nil) {
			return store.TaskInput{}, taskInputError(expected + " is required")
		}
	}
	if raw, ok := fields["title"]; ok {
		value, err := parseTaskString(raw, "title", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("title must not be empty")
		}
		input.Title = value
	}
	if raw, ok := fields["description"]; ok {
		if isJSONNull(raw) {
			empty := ""
			input.Description = &empty
		} else {
			value, err := parseTaskString(raw, "description", false)
			if err != nil {
				return store.TaskInput{}, err
			}
			input.Description = value
		}
	}
	if raw, ok := fields["priority"]; ok {
		value, err := parseTaskString(raw, "priority", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		input.Priority = value
	}
	if raw, ok := fields["labels"]; ok {
		if alternate, alternateOK := fields["label_ids"]; alternateOK && string(raw) != string(alternate) {
			return store.TaskInput{}, taskInputError("labels aliases must agree")
		}
		if err := validateIdentifierArray(raw, "labels", true); err != nil {
			return store.TaskInput{}, err
		}
		if isJSONNull(raw) {
			input.Labels, input.LabelsSet = []string{}, true
		} else if err := json.Unmarshal(raw, &input.Labels); err != nil {
			return store.TaskInput{}, err
		} else {
			input.LabelsSet = true
		}
	} else if raw, ok := fields["label_ids"]; ok {
		if err := validateIdentifierArray(raw, "label_ids", true); err != nil {
			return store.TaskInput{}, err
		}
		if isJSONNull(raw) {
			input.Labels, input.LabelsSet = []string{}, true
		} else if err := json.Unmarshal(raw, &input.Labels); err != nil {
			return store.TaskInput{}, err
		} else {
			input.LabelsSet = true
		}
	}
	if raw, ok := fields["due_at"]; ok {
		value, err := parseTaskString(raw, "due_at", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil {
			if _, err := time.Parse(time.RFC3339, *value); err != nil {
				return store.TaskInput{}, taskInputError("due_at must be RFC3339 or null")
			}
		}
		input.DueAt, input.DueAtSet = value, true
	}
	return input, nil
}
