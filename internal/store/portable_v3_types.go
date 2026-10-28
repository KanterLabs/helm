package store

const PortableVersionGithub = 3

// PortableGithubArchive contains durable history only. Provider credentials,
// selected repositories, disposable caches and executable jobs are excluded.
type PortableGithubArchive struct {
	Artifacts     []PortableGithubArtifact `json:"artifacts"`
	TaskArtifacts []PortableTaskArtifact   `json:"task_artifacts"`
	AuditRuns     []PortableAuditRun       `json:"audit_runs"`
	AuditFindings []PortableAuditFinding   `json:"audit_findings"`
	AuditEvidence []GithubAuditEvidence    `json:"audit_evidence"`
}
type PortableGithubArtifact struct {
	ID           string `json:"id"`
	ProjectID    string `json:"project_id"`
	Repository   string `json:"repository"`
	RepositoryID *int64 `json:"repository_id,omitempty"`
	Number       int    `json:"number"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at"`
}
type PortableTaskArtifact struct {
	TaskID     string  `json:"task_id"`
	ArtifactID string  `json:"artifact_id"`
	CreatedAt  string  `json:"created_at"`
	CreatedBy  *string `json:"created_by,omitempty"`
}
type PortableAuditRun struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	ActorID     string  `json:"actor_id"`
	Scope       string  `json:"scope"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	FinalizedAt *string `json:"finalized_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}
type PortableAuditFinding struct {
	Assessment                  *GithubAssessment `json:"assessment,omitempty"`
	ID                          string            `json:"id"`
	AuditID                     string            `json:"audit_id"`
	TaskID                      string            `json:"task_id"`
	CapturedVersion             int64             `json:"captured_version"`
	SourceColumn                string            `json:"source_column"`
	Verdict                     string            `json:"verdict"`
	ProposedSemanticDestination *string           `json:"proposed_semantic_destination,omitempty"`
	Confidence                  float64           `json:"confidence"`
	Reason                      string            `json:"reason"`
	EvidenceRefs                []string          `json:"evidence_refs"`
	ReviewState                 string            `json:"review_state"`
	Version                     int64             `json:"version"`
	CreatedAt                   string            `json:"created_at"`
	UpdatedAt                   string            `json:"updated_at"`
}

type portableV3Record[T any] struct {
	value  T
	create bool
}
type portableGithubPlan struct {
	artifactMap map[string]string
	runMap      map[string]string
	findingMap  map[string]string
	digests     map[string]string
	eventRefs   map[string]map[int]int64
	artifacts   []portableV3Record[PortableGithubArtifact]
	links       []portableV3Record[PortableTaskArtifact]
	runs        []portableV3Record[PortableAuditRun]
	findings    []portableV3Record[PortableAuditFinding]
	evidence    []GithubAuditEvidence
}
