package store

// Messages are closed, sanitized categories. Provider errors, prompts and model
// output never become general job-history text.
func githubAuditFailureMessage(code string) string {
	switch code {
	case "cancelled":
		return "Audit cancelled"
	case "interrupted":
		return "Audit interrupted; explicitly retry to capture fresh evidence"
	case "authorization_changed":
		return "Account or project authorization changed; check readiness and retry"
	case "permission_required", "repository_not_allowed":
		return "Restore GitHub repository access before retrying"
	case "rate_limited":
		return "GitHub rate limit reached; retry later"
	case "not_configured":
		return "Configure GitHub before retrying"
	case "source_changed", "head_changed":
		return "Source changed during capture; retry with fresh evidence"
	case "inference_unavailable":
		return "AI analysis was unavailable; check your personal connection and retry"
	case "deadline_exceeded":
		return "Audit deadline exceeded; explicitly retry"
	case "invalid_model_output":
		return "AI response failed evidence validation; explicitly retry"
	case "input_limit", "capture_invalid":
		return "Evidence exceeded capture limits; narrow the scope and retry"
	case "capture_unavailable":
		return "Captured task scope is unavailable"
	case "attempt_fenced":
		return "Audit attempt lost ownership and cannot publish"
	default:
		return "GitHub evidence was unavailable; check readiness and retry"
	}
}
