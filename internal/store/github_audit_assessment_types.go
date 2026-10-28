package store

// V2 is deliberately selected by new captures, never guessed from model output.
const GithubAssessmentVersion = 2

type GithubConditionAssessment struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Origin      string   `json:"origin"`
	Result      string   `json:"result"`
	Explanation string   `json:"explanation"`
	CitationIDs []string `json:"citation_ids"`
	Gaps        []string `json:"gaps"`
}

// Basis supplies affirmative placement facts independently of completion proof.
// Task prose/checklist state can support context, never implementation.
type GithubAssessmentBasis struct {
	Kind        string   `json:"kind"`
	Explanation string   `json:"explanation"`
	CitationIDs []string `json:"citation_ids"`
}

type GithubAssessmentInput struct {
	TaskID              string                      `json:"task_id"`
	CapturedVersion     int64                       `json:"captured_version"`
	SourceColumnID      string                      `json:"source_column_id"`
	Conditions          []GithubConditionAssessment `json:"conditions"`
	Placement           string                      `json:"placement"`
	TargetColumnID      *string                     `json:"target_column_id"`
	TargetSemanticState *string                     `json:"target_semantic_state"`
	LifecycleAction     string                      `json:"lifecycle_action"`
	Reason              string                      `json:"reason"`
	Basis               []GithubAssessmentBasis     `json:"basis"`
}

// Eligibility is a server observation, not permission to mutate. TC-309 must
// preview/revalidate lifecycle, actor, task, column and source facts again.
type GithubAssessmentEligibility struct {
	Status  string   `json:"status"` // not_applicable, review_required, blocked
	Reasons []string `json:"reasons"`
}
type GithubAssessment struct {
	GithubAssessmentInput
	AssessmentVersion      int                         `json:"assessment_version"`
	TaskKey                string                      `json:"task_key"`
	TaskKind               string                      `json:"task_kind"`
	SourceColumnName       string                      `json:"source_column_name"`
	SourceSemanticState    string                      `json:"source_semantic_state"`
	Baselines              []GithubAuditRepository     `json:"baselines"`
	CapturedAt             string                      `json:"captured_at"`
	ApplicationEligibility GithubAssessmentEligibility `json:"application_eligibility"`
}

type GithubAuditDependency struct {
	TaskID        string `json:"task_id"`
	Version       int64  `json:"version"`
	SemanticState string `json:"semantic_state"`
}

// These are captured task-context declarations, not source-code proof. Unknown
// declarations remain absent. They never arise from an absence of commits/CI.
type GithubAuditContext struct {
	ClaimedBy       string                  `json:"claimed_by,omitempty"`
	ClaimExpiresAt  string                  `json:"claim_expires_at,omitempty"`
	Dependencies    []GithubAuditDependency `json:"dependencies"`
	Unstarted       bool                    `json:"unstarted"`
	Readiness       string                  `json:"readiness,omitempty"`
	BlockerSourceID string                  `json:"blocker_source_id,omitempty"`
	Blocker         string                  `json:"blocker,omitempty"`
}

// PR heads require explicit selection and are pinned independently of linked
// PR evidence. The repository must already be in the selected source scope.
type GithubAuditBaseline struct {
	Repository  string `json:"repository"`
	PullRequest int    `json:"pull_request"`
}

// Counts come from persisted validated assessments, never model totals.
type GithubAssessmentSummary struct {
	Checked        int `json:"checked"`
	Corrections    int `json:"corrections"`
	Correct        int `json:"correct"`
	UnableToVerify int `json:"unable_to_verify"`
}
