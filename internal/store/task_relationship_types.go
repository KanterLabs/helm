package store

import ()

// TaskChecklistItem is one ordered, actor-aware acceptance criterion on a
// task. CompletedAt and CompletedBy are nullable so an item can be reopened
// without losing its stable identity or creation timestamp.
type TaskChecklistItem struct {
	ID          string  `json:"id"`
	TaskID      string  `json:"task_id"`
	Text        string  `json:"text"`
	Position    int     `json:"position"`
	Completed   bool    `json:"completed"`
	CompletedAt *string `json:"completed_at"`
	CompletedBy *string `json:"completed_by"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

// TaskChecklistSummary is included on task reads and board collections. The
// completion warning is true only when a completed task still has open items
// under the project's default warn policy.
type TaskChecklistSummary struct {
	Total            int     `json:"total"`
	Completed        int     `json:"completed"`
	Open             int     `json:"open"`
	Percent          float64 `json:"percent"`
	CompletionPolicy string  `json:"completion_policy"`
	Warning          bool    `json:"warning"`
}

// ChecklistItemInput is used for both create and partial update operations.
// A nil field is omitted; CompletedSet and PositionSet distinguish explicit
// false/zero values from omission.
type ChecklistItemInput struct {
	Text         *string
	Completed    *bool
	CompletedSet bool
	Position     *int
	PositionSet  bool
}

// ChecklistReorderInput is normalized by the HTTP layer to a complete list of
// item IDs. Positions are allocated contiguously by the store.
type ChecklistReorderInput struct {
	ItemIDs []string
}

// ChecklistCollection is the public read shape for a task checklist. It
// carries the owning task's current version so clients can send If-Match on
// the next mutation without fetching task details separately.
type ChecklistCollection struct {
	TaskID  string               `json:"task_id"`
	Version int64                `json:"version"`
	Items   []TaskChecklistItem  `json:"data"`
	Summary TaskChecklistSummary `json:"summary"`
}

// DependencySummary is the bounded, derived graph state embedded in every
// task response.  Counts include only direct relationships to live,
// same-project tasks; unmet prerequisites are those not currently in a
// completed semantic column with a completion timestamp.
type DependencySummary struct {
	PrerequisiteCount      int  `json:"prerequisite_count"`
	UnmetPrerequisiteCount int  `json:"unmet_prerequisite_count"`
	DependentCount         int  `json:"dependent_count"`
	Blocked                bool `json:"blocked"`
}

// TaskReference is the compact relation shape used by expanded dependency
// reads. Satisfied describes the referenced task itself: for a
// prerequisite it means the prerequisite is complete; for a dependent it
// means that dependent is complete.
type TaskReference struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Title       string  `json:"title"`
	CompletedAt *string `json:"completed_at"`
	Satisfied   bool    `json:"satisfied"`
}

// TaskDependencies contains the direct graph edges in both directions.
// Relations are bounded by the mutation limit and are returned in a stable
// deterministic order.
type TaskDependencies struct {
	Prerequisites []TaskReference `json:"prerequisites"`
	Dependents    []TaskReference `json:"dependents"`
}

// TaskHierarchyReference is the bounded relation shape used by hierarchy
// reads. It carries enough server-owned state for board and drawer rollups
// without recursively embedding full tasks.
type TaskHierarchyReference struct {
	ID            string     `json:"id"`
	Number        int        `json:"number"`
	Key           string     `json:"key"`
	ProjectID     string     `json:"project_id"`
	Title         string     `json:"title"`
	Kind          string     `json:"kind"`
	ColumnID      string     `json:"column_id"`
	SemanticState string     `json:"semantic_state"`
	State         string     `json:"state,omitempty"`
	Version       int64      `json:"version"`
	ParentID      *string    `json:"parent_id,omitempty"`
	CompletedAt   *string    `json:"completed_at,omitempty"`
	AgentWork     *AgentWork `json:"agent_work,omitempty"`
}

// HierarchySummary is derived from live child rows at read time. It never
// trusts browser-maintained counters or cached client state.
type HierarchySummary struct {
	ChildCount          int            `json:"child_count"`
	CompletedChildCount int            `json:"completed_child_count"`
	CompletionPercent   float64        `json:"completion_percent"`
	StateCounts         map[string]int `json:"state_counts"`
	BlockedChildCount   int            `json:"blocked_child_count"`
	LiveAgentWorkCount  int            `json:"live_agent_work_count"`
	ActionNeededCount   int            `json:"action_needed_count"`
	StaleAgentWorkCount int            `json:"stale_agent_work_count"`
}

// TaskHierarchy contains bounded direct children and the complete ancestor
// chain up to MaxTaskHierarchyDepth. Descendants is included as a bounded
// convenience for tree navigation; callers should use children for direct
// editing operations.
type TaskHierarchy struct {
	Parent      *TaskHierarchyReference  `json:"parent,omitempty"`
	Children    []TaskHierarchyReference `json:"children"`
	Ancestors   []TaskHierarchyReference `json:"ancestors"`
	Descendants []TaskHierarchyReference `json:"descendants"`
	Summary     HierarchySummary         `json:"summary"`
}
