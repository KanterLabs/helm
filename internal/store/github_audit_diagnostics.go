package store

import "errors"

// GithubAssessmentDiagnostic maps static validation failures into closed public
// categories. Unknown/database errors never contribute their text to history.
func GithubAssessmentDiagnostic(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		return "assessment_publication"
	}
	switch e.Message {
	case "assessment task snapshot does not match capture", "versioned assessments require captured evidence and the exact task set", "duplicate task assessment":
		return "assessment_identity"
	case "every captured condition requires one assessment", "condition identity/quote/origin mismatch":
		return "assessment_condition_identity"
	case "citation is outside task/criterion scope", "invalid task fact source", "citation does not match selected baseline", "unknown captured citation", "duplicate criterion citation", "duplicate basis citation":
		return "assessment_citation"
	case "conclusive criterion lacks appropriate baseline proof", "conclusive condition retains a relevant baseline gap", "Done requires every completion condition supported":
		return "assessment_proof"
	case "too many placement facts", "invalid/duplicate placement basis":
		return "assessment_basis_shape"
	case "unknown placement basis":
		return "assessment_basis_kind"
	case "placement basis lacks affirmative captured proof":
		return "assessment_basis_proof"
	case "In progress needs committed implementation and demonstrated remaining work":
		return "assessment_active_proof"
	case "unstarted placement needs affirmative work/readiness context", "Ready conflicts with an unresolved dependency":
		return "assessment_readiness_proof"
	case "Blocked requires a concrete unresolved blocker":
		return "assessment_blocker_proof"
	case "conclusive placement needs completion meaning and actual destination":
		return "assessment_completion_meaning"
	case "source column meaning is ambiguous; clarify mapping":
		return "assessment_source_meaning"
	case "destination column is unavailable or ambiguous; clarify mapping", "invalid semantic destination":
		return "assessment_destination"
	case "destination semantic mapping is unavailable or ambiguous":
		return "assessment_destination_ambiguous"
	case "destination identity does not match semantic mapping":
		return "assessment_destination_identity"
	case "conclusive placement has missing target identity":
		return "assessment_destination_id_missing"
	case "conclusive placement has missing target semantic state":
		return "assessment_destination_state_missing"
	case "unable to verify cannot recommend a mutation", "correct placement has no lifecycle action", "correction/action conflicts with captured placement":
		return "assessment_lifecycle"
	default:
		return "assessment_schema"
	}
}

// Only these literals can be persisted. No caller-provided prose is admitted.
func githubAuditDiagnostic(code string) string {
	switch code {
	case "assessment_decode", "assessment_schema", "assessment_identity", "assessment_condition_identity", "assessment_citation", "assessment_proof", "assessment_basis_shape", "assessment_basis_kind", "assessment_basis_proof", "assessment_active_proof", "assessment_readiness_proof", "assessment_blocker_proof", "assessment_destination", "assessment_destination_ambiguous", "assessment_destination_identity", "assessment_destination_id_missing", "assessment_destination_state_missing", "assessment_completion_meaning", "assessment_source_meaning", "assessment_lifecycle", "assessment_publication",
		"discovery_inference", "discovery_decode", "discovery_schema", "discovery_path", "discovery_range", "discovery_deadline":
		return " [" + code + "]"
	default:
		return ""
	}
}
