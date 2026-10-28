package store

// GithubAuditJob contains sanitized progress metadata. Private evidence and model
// input belong exclusively to the project-scoped capture and manifest.
type GithubAuditJob struct {
	AuditID            string  `json:"audit_id"`
	ProjectID          string  `json:"project_id"`
	Stage              string  `json:"stage"`
	Outcome            string  `json:"outcome"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
	FinishedAt         *string `json:"finished_at,omitempty"`
	TaskCount          int     `json:"task_count"`
	CapturedFiles      int     `json:"captured_files"`
	OmittedFiles       int     `json:"omitted_files"`
	Message            string  `json:"message"`
	ErrorCode          string  `json:"error_code,omitempty"`
	RetryOf            string  `json:"retry_of,omitempty"`
	ActorID            string  `json:"requested_by"`
	AccountOwnerID     string  `json:"account_owner_id"`
	AuthorizationID    string  `json:"authorization_id,omitempty"`
	RequestTokenID     string  `json:"-"`
	Fence              int64   `json:"-"`
	LeaseOwner         string  `json:"-"`
	SelectionVersion   int64   `json:"-"`
	ProviderGeneration string  `json:"-"`
	AccountGeneration  string  `json:"-"`
	Model              string  `json:"-"`
	Effort             string  `json:"-"`
}
type GithubAuditStartInput struct {
	AuthorizationID    string             `json:"authorization_id,omitempty"`
	RequestTokenID     string             `json:"-"`
	AccountOwnerID     string             `json:"-"`
	Repositories       []GithubRepository `json:"repositories,omitempty"`
	States             []string           `json:"states,omitempty"`
	TaskIDs            []string           `json:"task_ids,omitempty"`
	SelectionVersion   int64              `json:"-"`
	RequestKey         string             `json:"-"`
	Fingerprint        string             `json:"-"`
	RetryOf            string             `json:"-"`
	ProviderGeneration string             `json:"-"`
	AccountGeneration  string             `json:"-"`
	Model              string             `json:"-"`
	Effort             string             `json:"-"`
}
type GithubAuditTask struct {
	Kind            string                 `json:"kind,omitempty"`
	ChecklistPolicy string                 `json:"checklist_policy,omitempty"`
	Conditions      []GithubAuditCondition `json:"conditions,omitempty"`
	ID              string                 `json:"id"`
	Key             string                 `json:"key"`
	Title           string                 `json:"title"`
	Description     string                 `json:"description"`
	Version         int64                  `json:"version"`
	ColumnID        string                 `json:"column_id"`
	SemanticState   string                 `json:"semantic_state,omitempty"`
	ColumnName      string                 `json:"column_name,omitempty"`
	Criteria        []string               `json:"criteria"`
}
type GithubAuditCapture struct {
	Request   GithubAuditStartInput `json:"request"`
	Tasks     []GithubAuditTask     `json:"tasks"`
	Omissions []string              `json:"omissions"`
}
type GithubAuditRepository struct {
	Repository   string `json:"repository"`
	Ref          string `json:"ref"`
	SHA          string `json:"sha"`
	RepositoryID int64  `json:"repository_id"`
}
type GithubAuditItem struct {
	Ref           string   `json:"ref,omitempty"`
	SourceRole    string   `json:"source_role,omitempty"`
	EvidenceClass string   `json:"evidence_class,omitempty"`
	CriterionIDs  []string `json:"criterion_ids,omitempty"`
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Repository    string   `json:"repository"`
	SHA           string   `json:"sha"`
	BlobSHA       string   `json:"blob_sha,omitempty"`
	Path          string   `json:"path,omitempty"`
	StartLine     int      `json:"start_line,omitempty"`
	EndLine       int      `json:"end_line,omitempty"`
	Text          string   `json:"text"`
	TaskIDs       []string `json:"task_ids,omitempty"`
}
type GithubAuditEvidence struct {
	SourceCoverage    []GithubAuditSourceCoverage    `json:"source_coverage,omitempty"`
	RetrievalBudget   *GithubAuditRetrievalBudget    `json:"retrieval_budget,omitempty"`
	CriterionGaps     []GithubAuditCriterionGap      `json:"criterion_gaps,omitempty"`
	CriterionCoverage []GithubAuditCriterionCoverage `json:"criterion_coverage,omitempty"`
	AuditID           string                         `json:"audit_id"`
	CapturedAt        string                         `json:"captured_at"`
	Partial           bool                           `json:"partial"`
	OmittedFiles      int                            `json:"omitted_files,omitempty"`
	Omissions         []string                       `json:"omissions"`
	Repositories      []GithubAuditRepository        `json:"repositories"`
	Items             []GithubAuditItem              `json:"items"`
	// Captured tasks remain available independently of board pagination.
	Tasks []GithubAuditTask `json:"tasks"`
}
