package store

// ReleaseReference is the compact release relation embedded on task reads.
// Status is derived from ReleasedAt on the release row and is never persisted
// redundantly on tasks.
type ReleaseReference struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	TargetDate *string `json:"target_date,omitempty"`
}

// ReleaseRef is a compatibility alias for callers that use the shorter
// relation name.
type ReleaseRef = ReleaseReference

// ReleaseSummary contains direct membership progress and the transitive
// dependency closure required before a release can be completed.
type ReleaseSummary struct {
	TaskCount                 int  `json:"task_count"`
	CompletedCount            int  `json:"completed_count"`
	BlockedCount              int  `json:"blocked_count"`
	ClaimedCount              int  `json:"claimed_count"`
	ChecklistWarningCount     int  `json:"checklist_warning_count"`
	RequiredTaskCount         int  `json:"required_task_count"`
	RequiredCompletedCount    int  `json:"required_completed_count"`
	CrossReleaseConflictCount int  `json:"cross_release_conflict_count"`
	ReadyToRelease            bool `json:"ready_to_release"`
}

// Release is a project-local planned or released delivery boundary.
type Release struct {
	ID          string         `json:"id"`
	ProjectID   string         `json:"project_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	TargetDate  *string        `json:"target_date,omitempty"`
	Status      string         `json:"status"`
	ReleasedAt  *string        `json:"released_at,omitempty"`
	ReleasedBy  *string        `json:"released_by,omitempty"`
	Version     int64          `json:"version"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	Summary     ReleaseSummary `json:"summary"`
}

// ReleaseInput is used for create and patch operations. TargetDateSet and
// DescriptionSet distinguish explicit null/empty updates from omission.
type ReleaseInput struct {
	Name           *string
	Description    *string
	DescriptionSet bool
	TargetDate     *string
	TargetDateSet  bool
}

// ReleaseFilter controls project-scoped release listings.
type ReleaseFilter struct {
	Status     string
	TargetFrom *string
	TargetTo   *string
	Cursor     int
	Limit      int
}
