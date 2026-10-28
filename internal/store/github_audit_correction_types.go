package store

// GithubAuditCorrectionInput binds human-supplied policy inputs to one preview.
// A fixed recommendation cannot be relabeled as a different bug resolution.
type GithubAuditCorrectionInput struct {
	FindingID         string `json:"finding_id"`
	Resolution        string `json:"resolution,omitempty"`
	Reason            string `json:"reason,omitempty"`
	ConfirmationToken string `json:"confirmation_token,omitempty"`
}
type GithubAuditCorrectionResult struct {
	OutcomeKnown        bool                               `json:"outcome_known"`
	Baselines           []GithubAuditRepository            `json:"baselines,omitempty"`
	FindingID           string                             `json:"finding_id"`
	TaskID              string                             `json:"task_id,omitempty"`
	Outcome             string                             `json:"outcome"`
	Message             string                             `json:"message"`
	Action              string                             `json:"action,omitempty"`
	SourceColumnID      string                             `json:"source_column_id,omitempty"`
	TargetColumnID      string                             `json:"target_column_id,omitempty"`
	TargetSemanticState string                             `json:"target_semantic_state,omitempty"`
	CapturedVersion     int64                              `json:"captured_version,omitempty"`
	Fingerprint         string                             `json:"fingerprint,omitempty"`
	RequiredInputs      []string                           `json:"required_inputs,omitempty"`
	ConfirmationToken   string                             `json:"confirmation_token,omitempty"`
	ExpiresAt           string                             `json:"expires_at,omitempty"`
	Task                *GithubAuditCorrectionTaskSnapshot `json:"task,omitempty"`
	AuditID             string                             `json:"audit_id"`
	ActorID             string                             `json:"actor_id"`
	RequestKey          string                             `json:"request_key,omitempty"`
}

// A receipt preserves the committed lifecycle fields, independent of later card
// edits. Other task details are fetched through the ordinary task endpoint.
type GithubAuditCorrectionTaskSnapshot struct {
	ID             string  `json:"id"`
	Key            string  `json:"key"`
	ProjectID      string  `json:"project_id"`
	Kind           string  `json:"kind"`
	ColumnID       string  `json:"column_id"`
	Version        int64   `json:"version"`
	Position       float64 `json:"position"`
	CompletedAt    *string `json:"completed_at"`
	ClaimedBy      *string `json:"claimed_by"`
	ClaimExpiresAt *string `json:"claim_expires_at"`
	Bug            *Bug    `json:"bug,omitempty"`
}
