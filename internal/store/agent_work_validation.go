package store

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var agentWorkOperationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]*$`)

func validateAgentWorkOperationID(operationID string) (string, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || utf8.RuneCountInString(operationID) > 128 || !agentWorkOperationIDPattern.MatchString(operationID) {
		return "", invalid("operation_id must be between 1 and 128 safe identifier characters", nil)
	}
	return operationID, nil
}

func validateAgentWorkInput(input AgentWorkInput) (AgentWorkInput, error) {
	var err error
	input.OperationID, err = validateAgentWorkOperationID(input.OperationID)
	if err != nil {
		return AgentWorkInput{}, err
	}

	input.State = strings.TrimSpace(input.State)
	switch input.State {
	case "working", "waiting", "verifying", "handoff":
	default:
		return AgentWorkInput{}, invalid("state must be working, waiting, verifying, or handoff", nil)
	}

	input.Phase = strings.TrimSpace(input.Phase)
	if utf8.RuneCountInString(input.Phase) > 120 {
		return AgentWorkInput{}, invalid("phase is too long", nil)
	}

	input.Summary = strings.TrimSpace(input.Summary)
	if input.Summary == "" {
		return AgentWorkInput{}, invalid("summary is required", nil)
	}
	if utf8.RuneCountInString(input.Summary) > 1000 {
		return AgentWorkInput{}, invalid("summary is too long", nil)
	}

	input.NextAction = strings.TrimSpace(input.NextAction)
	if utf8.RuneCountInString(input.NextAction) > 1000 {
		return AgentWorkInput{}, invalid("next_action is too long", nil)
	}

	refs := input.CheckpointRefs
	if refs == nil {
		refs = []string{}
	}
	if len(refs) > 100 {
		return AgentWorkInput{}, invalid("checkpoint_refs must contain at most 100 items", nil)
	}
	normalizedRefs := make([]string, 0, len(refs))
	seenRefs := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || utf8.RuneCountInString(ref) > 128 {
			return AgentWorkInput{}, invalid("checkpoint_refs items must be between 1 and 128 characters", nil)
		}
		if _, exists := seenRefs[ref]; exists {
			return AgentWorkInput{}, invalid("checkpoint_refs must not contain duplicate items", nil)
		}
		seenRefs[ref] = struct{}{}
		normalizedRefs = append(normalizedRefs, ref)
	}
	input.CheckpointRefs = normalizedRefs

	if (input.CheckpointCompleted == nil) != (input.CheckpointTotal == nil) {
		return AgentWorkInput{}, invalid("checkpoint_completed and checkpoint_total must be provided together", nil)
	}
	if input.CheckpointCompleted != nil {
		completed, total := *input.CheckpointCompleted, *input.CheckpointTotal
		if total < 1 || total > 100 || completed < 0 || completed > total {
			return AgentWorkInput{}, invalid("checkpoint progress must satisfy 0 <= completed <= total <= 100", nil)
		}
		if len(normalizedRefs) > 0 && len(normalizedRefs) != total {
			return AgentWorkInput{}, invalid("checkpoint_refs length must equal checkpoint_total", nil)
		}
	} else if len(normalizedRefs) > 0 {
		return AgentWorkInput{}, invalid("checkpoint_total is required when checkpoint_refs is non-empty", nil)
	}
	return input, nil
}
