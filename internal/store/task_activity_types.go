package store

// AgentWork is the latest progress pulse published for a task. Stale and
// action-needed are derived at read time so clients do not have to infer the
// liveness of a server-owned timestamp.
type AgentWork struct {
	OperationID         string   `json:"operation_id"`
	ActorID             string   `json:"actor_id"`
	State               string   `json:"state"`
	Phase               string   `json:"phase"`
	Summary             string   `json:"summary"`
	NextAction          string   `json:"next_action"`
	CheckpointRefs      []string `json:"checkpoint_refs"`
	CheckpointCompleted *int     `json:"checkpoint_completed,omitempty"`
	CheckpointTotal     *int     `json:"checkpoint_total,omitempty"`
	StartedAt           string   `json:"started_at"`
	UpdatedAt           string   `json:"updated_at"`
	Stale               bool     `json:"stale"`
	ActionNeeded        bool     `json:"action_needed"`
}

// AgentWorkInput is a complete replacement snapshot. Omitted optional text,
// refs, and checkpoint counts clear the corresponding prior values.
type AgentWorkInput struct {
	OperationID         string   `json:"operation_id"`
	State               string   `json:"state"`
	Phase               string   `json:"phase"`
	Summary             string   `json:"summary"`
	NextAction          string   `json:"next_action"`
	CheckpointRefs      []string `json:"checkpoint_refs"`
	CheckpointCompleted *int     `json:"checkpoint_completed"`
	CheckpointTotal     *int     `json:"checkpoint_total"`
}

// Bug contains the typed metadata associated with a bug task. The detail row
// is deliberately kept separate from tasks so existing task rows and clients
// remain valid while a bug can still carry a richer lifecycle than a task.
// Severity and resolution are nullable until triage/resolution respectively.
type Bug struct {
	ReporterID        string  `json:"reporter_id"`
	Severity          *string `json:"severity,omitempty"`
	ActualBehavior    string  `json:"actual_behavior"`
	ExpectedBehavior  string  `json:"expected_behavior,omitempty"`
	ReproductionSteps string  `json:"reproduction_steps,omitempty"`
	Environment       string  `json:"environment,omitempty"`
	AffectedVersion   string  `json:"affected_version,omitempty"`
	Resolution        *string `json:"resolution,omitempty"`
	ResolvedBy        *string `json:"resolved_by,omitempty"`
	ResolvedAt        *string `json:"resolved_at,omitempty"`
	DuplicateOf       *string `json:"duplicate_of,omitempty"`
}

// BugDetails is retained as an alias for callers that prefer the persistence
// terminology. Task.Bug uses the shorter API-facing Bug name.
type BugDetails = Bug

// BugInput is the patch/create shape for bug metadata. Set flags distinguish
// an omitted field from an explicit JSON null, which is important for nullable
// severity and for clearing optional text on a patch.
type BugInput struct {
	Severity             *string
	SeveritySet          bool
	ActualBehavior       *string
	ActualBehaviorSet    bool
	ExpectedBehavior     *string
	ExpectedBehaviorSet  bool
	ReproductionSteps    *string
	ReproductionStepsSet bool
	Environment          *string
	EnvironmentSet       bool
	AffectedVersion      *string
	AffectedVersionSet   bool
}

// TriageBugInput changes only the pre-resolution severity. A nil severity is
// accepted when SeveritySet is true to explicitly return a bug to an
// untriaged state.
type TriageBugInput struct {
	Severity    *string
	SeveritySet bool
	Priority    *string
	Assignee    *string
	AssigneeSet bool
	ColumnID    *string
}

// ResolveBugInput supplies the terminal resolution. DuplicateOf is required
// when Resolution is "duplicate" and ignored/forbidden for other resolutions.
type ResolveBugInput struct {
	Resolution     string
	DuplicateOf    *string
	DuplicateOfSet bool
	Note           string
}
