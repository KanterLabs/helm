package store

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var auditSafeRefPattern = regexp.MustCompile(`^[A-Za-z0-9/][A-Za-z0-9._:/+~-]*$`)

func validAuditReviewState(value string) bool {
	switch value {
	case "pending", "approved", "dismissed":
		return true
	default:
		return false
	}
}

func validateAuditRunInput(input AuditRunInput) (AuditRunInput, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		return AuditRunInput{}, invalid("audit scope is required", nil)
	}
	if utf8.RuneCountInString(input.Scope) > maxAuditScope || strings.IndexFunc(input.Scope, unicode.IsControl) >= 0 {
		return AuditRunInput{}, invalid("audit scope must be between 1 and 200 safe characters", nil)
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "running"
	}
	if input.Status != "queued" && input.Status != "running" {
		return AuditRunInput{}, invalid("audit status must be queued or running when creating a run", nil)
	}
	return input, nil
}

func validateAuditFindingInput(input AuditFindingInput) (AuditFindingInput, error) {
	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		return AuditFindingInput{}, invalid("task_id is required", nil)
	}
	if input.CapturedVersion <= 0 {
		return AuditFindingInput{}, invalid("captured_version must be a positive integer", nil)
	}
	if strings.TrimSpace(input.SourceColumn) == "" {
		input.SourceColumn = strings.TrimSpace(input.SourceColumnID)
	}
	input.SourceColumn = strings.TrimSpace(input.SourceColumn)
	if input.SourceColumn == "" {
		return AuditFindingInput{}, invalid("source_column is required", nil)
	}
	if utf8.RuneCountInString(input.SourceColumn) > maxAuditSourceColumn || strings.IndexFunc(input.SourceColumn, unicode.IsControl) >= 0 {
		return AuditFindingInput{}, invalid("source_column must be between 1 and 200 characters", nil)
	}
	if input.SourceColumnID != "" && strings.TrimSpace(input.SourceColumnID) != input.SourceColumn {
		return AuditFindingInput{}, invalid("source_column and source_column_id must match", nil)
	}
	input.SourceColumnID = input.SourceColumn

	switch input.Verdict {
	case "correct", "needs_attention", "move_proposed":
	default:
		return AuditFindingInput{}, invalid("verdict must be correct, needs_attention, or move_proposed", nil)
	}
	if input.ProposedSemanticDestination != nil {
		value := strings.ToLower(strings.TrimSpace(*input.ProposedSemanticDestination))
		if !validState(value) {
			return AuditFindingInput{}, invalid("proposed_semantic_destination is invalid", nil)
		}
		input.ProposedSemanticDestination = &value
	}
	if input.Verdict == "move_proposed" && input.ProposedSemanticDestination == nil {
		return AuditFindingInput{}, invalid("proposed_semantic_destination is required for move_proposed findings", nil)
	}
	if input.Verdict != "move_proposed" && input.ProposedSemanticDestination != nil {
		return AuditFindingInput{}, invalid("proposed_semantic_destination is only valid for move_proposed findings", nil)
	}
	if math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) || input.Confidence < 0 || input.Confidence > 1 {
		return AuditFindingInput{}, invalid("confidence must be between 0 and 1", nil)
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return AuditFindingInput{}, invalid("reason is required", nil)
	}
	if utf8.RuneCountInString(input.Reason) > maxAuditReason || strings.IndexFunc(input.Reason, unicode.IsControl) >= 0 {
		return AuditFindingInput{}, invalid("reason must be between 1 and 2000 characters", nil)
	}
	refs, err := normalizeAuditEvidenceRefs(input.EvidenceRefs)
	if err != nil {
		return AuditFindingInput{}, err
	}
	input.EvidenceRefs = refs
	input.ReviewState = strings.TrimSpace(input.ReviewState)
	if input.ReviewState == "" {
		input.ReviewState = "pending"
	}
	if utf8.RuneCountInString(input.ReviewState) > maxAuditReviewState || input.ReviewState != "pending" {
		return AuditFindingInput{}, invalid("review_state must be pending when appending a finding", nil)
	}
	return input, nil
}

func normalizeAuditEvidenceRefs(refs []string) ([]string, error) {
	if refs == nil {
		return []string{}, nil
	}
	if len(refs) > maxAuditEvidenceRefs {
		return nil, invalid("evidence_refs must contain at most 100 items", nil)
	}
	result := make([]string, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if utf8.RuneCountInString(ref) == 0 || utf8.RuneCountInString(ref) > maxAuditEvidenceRef || !auditSafeRefPattern.MatchString(ref) {
			return nil, invalid("evidence_refs items must be safe references of at most 512 characters", nil)
		}
		lower := strings.ToLower(ref)
		if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "data:") {
			return nil, invalid("evidence_refs cannot contain executable or data URLs", nil)
		}
		if _, ok := seen[ref]; ok {
			return nil, invalid("evidence_refs must not contain duplicate items", nil)
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxAuditEvidenceJSON {
		return nil, invalid("evidence_refs payload is too large", nil)
	}
	return result, nil
}

func validateAuditFindingReviewInput(input AuditFindingReviewInput) (AuditFindingReviewInput, error) {
	input.ReviewState = strings.TrimSpace(input.ReviewState)
	if !validAuditReviewState(input.ReviewState) {
		return AuditFindingReviewInput{}, invalid("review_state must be pending, approved, or dismissed", nil)
	}
	if input.ProposedSemanticDestination != nil {
		// Direct store callers do not have JSON omission metadata. A non-nil
		// pointer therefore means the destination was intentionally supplied.
		input.ProposedSemanticDestinationSet = true
	}
	if input.ProposedSemanticDestinationSet {
		if input.ProposedSemanticDestination != nil {
			value := strings.ToLower(strings.TrimSpace(*input.ProposedSemanticDestination))
			if !validState(value) {
				return AuditFindingReviewInput{}, invalid("proposed_semantic_destination is invalid", nil)
			}
			input.ProposedSemanticDestination = &value
		}
	}
	return input, nil
}
