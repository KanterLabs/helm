package store

// AuditRun is a durable board-audit execution. Findings are fetched through
// the bounded, paginated finding collection so run metadata stays small.
type AuditRun struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	ActorID      string         `json:"actor_id"`
	Scope        string         `json:"scope"`
	Status       string         `json:"status"`
	StartedAt    string         `json:"started_at"`
	FinalizedAt  *string        `json:"finalized_at,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	FindingCount int            `json:"finding_count"`
	Findings     []AuditFinding `json:"findings"`
}

// AuditFinding is an immutable observation of one task at a point in an
// audit. ChangedSinceAudit is derived on every read from the current task
// version and source column ID; it is never persisted, so the audit remains a
// true snapshot even as normal task work continues.
type AuditFinding struct {
	ID                          string   `json:"id"`
	AuditID                     string   `json:"audit_id"`
	TaskID                      string   `json:"task_id"`
	CapturedVersion             int64    `json:"captured_version"`
	SourceColumn                string   `json:"source_column"`
	Verdict                     string   `json:"verdict"`
	ProposedSemanticDestination *string  `json:"proposed_semantic_destination,omitempty"`
	Confidence                  float64  `json:"confidence"`
	Reason                      string   `json:"reason"`
	EvidenceRefs                []string `json:"evidence_refs"`
	ReviewState                 string   `json:"review_state"`
	Version                     int64    `json:"version"`
	ChangedSinceAudit           bool     `json:"changed_since_audit"`
	CreatedAt                   string   `json:"created_at"`
	UpdatedAt                   string   `json:"updated_at"`
	// SourceColumnID is a source-compatibility alias for callers that use the
	// task table's column_id terminology. The wire field remains source_column.
	SourceColumnID string `json:"-"`
}

// AuditRunInput is the create shape for an audit run. Scope is an opaque,
// bounded label (for example, "board" or "open_tasks") so deployments can
// evolve audit selection semantics without a schema migration.
type AuditRunInput struct {
	Scope string
	// Status may be queued when an audit request is accepted for asynchronous
	// processing. Empty retains the synchronous running default.
	Status string
}

// AuditFindingInput is the append shape for one immutable finding. Source
// column is the exact opaque columns.id captured by the auditor.
type AuditFindingInput struct {
	TaskID          string
	CapturedVersion int64
	SourceColumn    string
	// SourceColumnID is accepted as an alias for SourceColumn by direct store
	// callers and the HTTP adapter. It is not separately persisted.
	SourceColumnID              string
	Verdict                     string
	ProposedSemanticDestination *string
	Confidence                  float64
	Reason                      string
	EvidenceRefs                []string
	ReviewState                 string
}

// AuditFindingReviewInput contains the only mutable finding fields. The
// snapshot (task, captured version, source column, verdict, confidence,
// reason, and evidence) remains immutable after append.
type AuditFindingReviewInput struct {
	ReviewState                    string
	ProposedSemanticDestination    *string
	ProposedSemanticDestinationSet bool
}

type auditTaskSnapshot struct {
	Version       int64
	ColumnID      string
	ColumnName    string
	SemanticState string
}

const (
	maxAuditScope          = 200
	maxAuditSourceColumn   = 200
	maxAuditReason         = 2000
	maxAuditEvidenceRefs   = 100
	maxAuditEvidenceRef    = 512
	maxAuditEvidenceJSON   = 16384
	maxAuditReviewState    = 32
	maxAuditRunFindings    = 10000
	maxAuditRunsPerProject = 1000
)
