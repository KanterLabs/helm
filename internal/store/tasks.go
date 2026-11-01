package store

import (
	"strings"
	"time"
)

const (
	// Task claims default to a 30-minute lease when no duration is supplied.
	// Explicit durations are bounded to keep leases useful without allowing a
	// caller to hold work indefinitely.
	DefaultTaskClaimDuration = 30 * time.Minute
	MinTaskClaimDuration     = 30 * time.Second
	MaxTaskClaimDuration     = 7 * 24 * time.Hour
)

type TaskInput struct {
	Title       *string
	Description *string
	Priority    *string
	Kind        *string
	Bug         *BugInput
	BugSet      bool
	ColumnID    *string
	Position    *float64
	Assignee    *string
	AssigneeSet bool
	DueAt       *string
	DueAtSet    bool
	Labels      []string
	LabelsSet   bool
	// ParentTaskID accepts an opaque task ID or project-local task key. A
	// separate set flag distinguishes an omitted patch field from an explicit
	// null that unlinks the task from its parent.
	ParentTaskID *string
	ParentSet    bool
	// ReleaseID accepts an opaque release ID. ReleaseSet distinguishes an
	// omitted patch field from an explicit null that clears membership.
	ReleaseID    *string
	ReleaseSet   bool
	ReleaseIDSet bool // compatibility alias; ReleaseSet takes the same meaning.
}

const (
	defaultTaskKind = "task"
	bugKind         = "bug"
)

func validateTaskInput(input TaskInput, creating bool) (TaskInput, error) {
	if creating && input.Title == nil {
		return TaskInput{}, invalid("title is required", nil)
	}
	if input.Title != nil {
		value := strings.TrimSpace(*input.Title)
		if value == "" || len(value) > 500 {
			return TaskInput{}, invalid("title must be between 1 and 500 characters", nil)
		}
		input.Title = &value
	}
	if input.Description != nil && len(*input.Description) > 100000 {
		return TaskInput{}, invalid("description is too long", nil)
	}
	if input.Priority != nil {
		value := *input.Priority
		if !validPriority(value) {
			return TaskInput{}, invalid("priority must be low, normal, high, or urgent", nil)
		}
		input.Priority = &value
	}
	if input.Kind != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Kind))
		if value != defaultTaskKind && value != bugKind {
			return TaskInput{}, invalid("kind must be task or bug", nil)
		}
		input.Kind = &value
	}
	if input.Bug != nil {
		normalized, err := validateBugInput(*input.Bug, false)
		if err != nil {
			return TaskInput{}, err
		}
		input.Bug = &normalized
		input.BugSet = true
	}
	if creating {
		kind := defaultTaskKind
		if input.Kind != nil {
			kind = *input.Kind
		}
		if kind == bugKind {
			if input.Bug == nil {
				return TaskInput{}, invalid("bug details are required when kind is bug", nil)
			}
			if err := validateBugCreateInput(*input.Bug); err != nil {
				return TaskInput{}, err
			}
		} else if input.Bug != nil || input.BugSet {
			return TaskInput{}, invalid("bug details require kind bug", nil)
		}
	}
	if input.Position != nil && (*input.Position < 0 || *input.Position > 1e12) {
		return TaskInput{}, invalid("position is invalid", nil)
	}
	if input.DueAt != nil {
		parsed, err := parseTime(*input.DueAt)
		if err != nil {
			return TaskInput{}, invalid(err.Error(), nil)
		}
		input.DueAt = parsed
	}
	if len(input.Labels) > 100 {
		return TaskInput{}, invalid("too many labels", nil)
	}
	if input.ParentTaskID != nil {
		value := strings.TrimSpace(*input.ParentTaskID)
		if value == "" {
			return TaskInput{}, invalid("parent_task_id must not be empty", nil)
		}
		input.ParentTaskID = &value
	}
	if input.ReleaseID != nil {
		value := strings.TrimSpace(*input.ReleaseID)
		if value == "" {
			return TaskInput{}, invalid("release_id must not be empty", nil)
		}
		input.ReleaseID = &value
	}
	return input, nil
}

func nullableStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// validateBugInput normalizes patch values and enforces bounded text sizes.
// A missing field remains nil; an explicit Set flag lets callers clear a
// nullable or optional field without confusing it with omission.
func validateBugInput(input BugInput, creating bool) (BugInput, error) {
	if input.Severity != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Severity))
		if !validBugSeverity(value) {
			return BugInput{}, invalid("severity must be s1, s2, s3, or s4", nil)
		}
		input.Severity = &value
	}
	if input.ActualBehavior != nil {
		value := strings.TrimSpace(*input.ActualBehavior)
		if value == "" {
			return BugInput{}, invalid("actual_behavior must not be empty", nil)
		}
		if len(value) > 100000 {
			return BugInput{}, invalid("actual_behavior is too long", nil)
		}
		input.ActualBehavior = &value
	}
	for _, field := range []struct {
		name  string
		value **string
		set   bool
	}{
		{"expected_behavior", &input.ExpectedBehavior, input.ExpectedBehaviorSet},
		{"reproduction_steps", &input.ReproductionSteps, input.ReproductionStepsSet},
		{"environment", &input.Environment, input.EnvironmentSet},
		{"affected_version", &input.AffectedVersion, input.AffectedVersionSet},
	} {
		if *field.value == nil {
			if field.set && creating {
				// Explicit null is represented as an empty optional value on
				// create; this keeps the stored shape deterministic.
			}
			continue
		}
		normalized := strings.TrimSpace(**field.value)
		if len(normalized) > 100000 {
			return BugInput{}, invalid(field.name+" is too long", nil)
		}
		*field.value = &normalized
	}
	return input, nil
}

func validateBugCreateInput(input BugInput) error {
	if input.ActualBehavior == nil || strings.TrimSpace(*input.ActualBehavior) == "" {
		return invalid("actual_behavior is required for bugs", nil)
	}
	return nil
}

func validBugSeverity(value string) bool {
	switch value {
	case "s1", "s2", "s3", "s4":
		return true
	}
	return false
}

func validBugResolution(value string) bool {
	switch value {
	case "fixed", "duplicate", "not_planned", "cannot_reproduce", "works_as_designed":
		return true
	}
	return false
}

func validPriority(value string) bool {
	switch value {
	case "low", "normal", "high", "urgent":
		return true
	}
	return false
}
