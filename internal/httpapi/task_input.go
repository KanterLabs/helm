package httpapi

import (
	"encoding/json"
	"errors"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strings"
	"time"
)

func decodeTaskInput(r *http.Request, creating bool) (store.TaskInput, error) {
	var payload map[string]json.RawMessage
	var typed map[string]json.RawMessage
	fields, err := decodeJSONObject(r, &typed)
	if err != nil {
		return store.TaskInput{}, err
	}
	payload = fields
	for name := range payload {
		switch name {
		case "title", "description", "priority", "kind", "bug", "column_id", "column", "position", "assignee", "assignee_id", "due_at", "labels", "label_ids", "parent", "parent_id", "parent_task_id", "release_id":
		default:
			return store.TaskInput{}, taskInputError("unknown task field: " + name)
		}
	}
	if creating {
		if err := requireJSONFields(payload, "title"); err != nil {
			return store.TaskInput{}, err
		}
	}

	// A PATCH with no recognized fields would otherwise increment the task
	// version while making no change. Unknown-only payloads are also rejected so
	// clients cannot accidentally believe an ignored field was applied.
	if !creating {
		recognized := false
		for _, name := range []string{"title", "description", "priority", "kind", "bug", "column_id", "column", "position", "assignee", "assignee_id", "due_at", "labels", "label_ids", "parent", "parent_id", "parent_task_id", "release_id"} {
			if _, ok := payload[name]; ok {
				recognized = true
				break
			}
		}
		if !recognized {
			return store.TaskInput{}, taskInputError("patch must include at least one task field")
		}
	}

	var input store.TaskInput
	if raw, ok := preferredTaskField(payload, "title"); ok {
		value, err := parseTaskString(raw, "title", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("title must not be empty")
		}
		input.Title = value
	}
	if raw, ok := preferredTaskField(payload, "description"); ok {
		if isJSONNull(raw) {
			if creating {
				return store.TaskInput{}, taskInputError("description cannot be null")
			}
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
	if raw, ok := preferredTaskField(payload, "priority"); ok {
		value, err := parseTaskString(raw, "priority", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("priority must not be empty")
		}
		input.Priority = value
	}
	if raw, ok := preferredTaskField(payload, "kind"); ok {
		value, err := parseTaskString(raw, "kind", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("kind must not be empty")
		}
		input.Kind = value
	}
	if raw, ok := preferredTaskField(payload, "bug"); ok {
		input.BugSet = true
		if isJSONNull(raw) {
			input.Bug = nil
		} else {
			bug, err := decodeBugInput(raw)
			if err != nil {
				return store.TaskInput{}, err
			}
			input.Bug = &bug
		}
	}
	if raw, ok := preferredTaskField(payload, "column_id"); ok {
		value, err := parseTaskString(raw, "column_id", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("column_id must not be empty")
		}
		input.ColumnID = value
	} else if raw, ok := preferredTaskField(payload, "column"); ok {
		value, err := parseTaskString(raw, "column", false)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value == nil || strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("column must not be empty")
		}
		input.ColumnID = value
	}
	if raw, ok := preferredTaskField(payload, "position"); ok {
		if isJSONNull(raw) {
			return store.TaskInput{}, taskInputError("position cannot be null")
		}
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return store.TaskInput{}, err
		}
		input.Position = &value
	}
	if raw, ok := preferredTaskField(payload, "assignee"); ok {
		value, err := parseTaskString(raw, "assignee", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil && strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("assignee must not be empty")
		}
		input.Assignee, input.AssigneeSet = value, true
	} else if raw, ok := preferredTaskField(payload, "assignee_id"); ok {
		value, err := parseTaskString(raw, "assignee_id", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil && strings.TrimSpace(*value) == "" {
			return store.TaskInput{}, taskInputError("assignee_id must not be empty")
		}
		input.Assignee, input.AssigneeSet = value, true
	}
	if raw, ok := preferredTaskField(payload, "due_at"); ok {
		value, err := parseTaskString(raw, "due_at", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil {
			if strings.TrimSpace(*value) == "" {
				return store.TaskInput{}, taskInputError("due_at must be RFC3339 or null")
			}
			if _, err := time.Parse(time.RFC3339, *value); err != nil {
				return store.TaskInput{}, taskInputError("due_at must be RFC3339 or null")
			}
		}
		input.DueAt, input.DueAtSet = value, true
	}
	if raw, ok := preferredTaskField(payload, "parent_task_id", "parent_id", "parent"); ok {
		value, err := parseTaskString(raw, "parent_task_id", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil {
			trimmed := strings.TrimSpace(*value)
			if trimmed == "" {
				return store.TaskInput{}, taskInputError("parent_task_id must not be empty")
			}
			if len(trimmed) > maxDependencyReferenceLength {
				return store.TaskInput{}, taskInputError("parent_task_id is too long")
			}
			value = &trimmed
		}
		input.ParentTaskID, input.ParentSet = value, true
	}
	if raw, ok := payload["release_id"]; ok {
		value, err := parseTaskString(raw, "release_id", true)
		if err != nil {
			return store.TaskInput{}, err
		}
		if value != nil {
			trimmed := strings.TrimSpace(*value)
			if trimmed == "" {
				return store.TaskInput{}, taskInputError("release_id must not be empty")
			}
			value = &trimmed
		}
		input.ReleaseID, input.ReleaseSet, input.ReleaseIDSet = value, true, true
	}
	if raw, ok := preferredTaskField(payload, "labels"); ok {
		if err := validateIdentifierArray(raw, "labels", true); err != nil {
			return store.TaskInput{}, err
		}
		if isJSONNull(raw) {
			input.Labels, input.LabelsSet = []string{}, true
		} else {
			if err := json.Unmarshal(raw, &input.Labels); err != nil {
				return store.TaskInput{}, err
			}
			input.LabelsSet = true
		}
	} else if raw, ok := preferredTaskField(payload, "label_ids"); ok {
		if err := validateIdentifierArray(raw, "label_ids", true); err != nil {
			return store.TaskInput{}, err
		}
		if isJSONNull(raw) {
			input.Labels, input.LabelsSet = []string{}, true
		} else {
			if err := json.Unmarshal(raw, &input.Labels); err != nil {
				return store.TaskInput{}, err
			}
			input.LabelsSet = true
		}
	}
	return input, nil
}

func decodeBugInput(raw json.RawMessage) (store.BugInput, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return store.BugInput{}, taskInputError("bug must be an object")
	}
	for name := range payload {
		switch name {
		case "severity", "actual_behavior", "expected_behavior", "reproduction_steps", "environment", "affected_version":
		default:
			return store.BugInput{}, taskInputError("unknown bug field: " + name)
		}
	}
	var input store.BugInput
	if value, ok := payload["severity"]; ok {
		input.SeveritySet = true
		if !isJSONNull(value) {
			parsed, err := parseTaskString(value, "severity", false)
			if err != nil {
				return store.BugInput{}, err
			}
			input.Severity = parsed
		}
	}
	if value, ok := payload["actual_behavior"]; ok {
		if isJSONNull(value) {
			return store.BugInput{}, taskInputError("actual_behavior cannot be null")
		}
		parsed, err := parseTaskString(value, "actual_behavior", false)
		if err != nil {
			return store.BugInput{}, err
		}
		input.ActualBehavior, input.ActualBehaviorSet = parsed, true
	}
	for name, target := range map[string]struct {
		value **string
		set   *bool
	}{
		"expected_behavior":  {&input.ExpectedBehavior, &input.ExpectedBehaviorSet},
		"reproduction_steps": {&input.ReproductionSteps, &input.ReproductionStepsSet},
		"environment":        {&input.Environment, &input.EnvironmentSet},
		"affected_version":   {&input.AffectedVersion, &input.AffectedVersionSet},
	} {
		value, ok := payload[name]
		if !ok {
			continue
		}
		*target.set = true
		if isJSONNull(value) {
			continue
		}
		parsed, err := parseTaskString(value, name, false)
		if err != nil {
			return store.BugInput{}, err
		}
		*target.value = parsed
	}
	return input, nil
}

func preferredTaskField(payload map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if value, ok := payload[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func parseTaskString(raw json.RawMessage, field string, nullable bool) (*string, error) {
	if isJSONNull(raw) {
		if nullable {
			return nil, nil
		}
		return nil, taskInputError(field + " cannot be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func taskInputError(message string) error {
	return &store.Error{Kind: store.ErrInvalid, Message: message}
}

func writeTaskInputError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrInvalid) {
		sDummyWriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	sDummyWriteError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
}
