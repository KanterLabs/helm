package store

// GithubAuditCondition preserves a quote, its source and the exact task version.
// It is a capture identity, not a claim that the requirement is satisfied.
type GithubAuditCondition struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Origin      string `json:"origin"` // goal, acceptance_criterion, checklist
	SourceID    string `json:"source_id,omitempty"`
	TaskVersion int64  `json:"task_version"`
	Line        int    `json:"line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	Completed   *bool  `json:"completed,omitempty"` // checklist fact, not code proof
}

type GithubAuditCriterionGap struct {
	TaskID      string `json:"task_id"`
	CriterionID string `json:"criterion_id,omitempty"`
	Repository  string `json:"repository,omitempty"`
	Ref         string `json:"ref,omitempty"`
	SHA         string `json:"sha,omitempty"`
	SourceRole  string `json:"source_role"`
	Path        string `json:"path,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

// Coverage is retrieval coverage only. An excerpt matching a route or symbol is
// not an evaluator verdict, nor does test source establish a passing check.
type GithubAuditCriterionCoverage struct {
	TaskID         string   `json:"task_id"`
	CriterionID    string   `json:"criterion_id"`
	Repository     string   `json:"repository"`
	SHA            string   `json:"sha"`
	SourceRole     string   `json:"source_role"`
	EvidenceIDs    []string `json:"evidence_ids"`
	InspectedPaths []string `json:"inspected_paths"`
}

type GithubAuditRetrievalBudget struct {
	PerCardFiles             int `json:"per_card_files"`
	PerCardBytes             int `json:"per_card_bytes"`
	RunFiles                 int `json:"run_files"`
	RunReadBytes             int `json:"run_read_bytes"`
	RunExcerptBytes          int `json:"run_excerpt_bytes"`
	RunDiscoveryRequests     int `json:"run_discovery_requests"`
	PerCardDiscoveryRequests int `json:"per_card_discovery_requests"`
	RefinementRounds         int `json:"refinement_rounds"`
	ExcerptBytes             int `json:"excerpt_bytes"`
	DurationSeconds          int `json:"duration_seconds"`
}

// Repository coverage describes the read scope, independently of whether a
// criterion's relevant owners were found. Unread files are not contradictions.
type GithubAuditSourceCoverage struct {
	Repository     string `json:"repository"`
	Ref            string `json:"ref"`
	SHA            string `json:"sha"`
	SourceRole     string `json:"source_role"`
	IndexedFiles   int    `json:"indexed_files"`
	InspectedFiles int    `json:"inspected_files"`
	UnreadFiles    int    `json:"unread_files"`
	IndexPartial   bool   `json:"index_partial"`
}
