package store

type Task struct {
	ID                string               `json:"id"`
	Number            int                  `json:"number"`
	Key               string               `json:"key"`
	ProjectID         string               `json:"project_id"`
	Kind              string               `json:"kind"`
	ColumnID          string               `json:"column_id"`
	Title             string               `json:"title"`
	Description       string               `json:"description"`
	Priority          string               `json:"priority"`
	Position          float64              `json:"position"`
	Assignee          *string              `json:"assignee,omitempty"`
	ClaimedBy         *string              `json:"claimed_by,omitempty"`
	ClaimExpiresAt    *string              `json:"claim_expires_at,omitempty"`
	DueAt             *string              `json:"due_at,omitempty"`
	Version           int64                `json:"version"`
	CompletedAt       *string              `json:"completed_at,omitempty"`
	CreatedAt         string               `json:"created_at"`
	UpdatedAt         string               `json:"updated_at"`
	Labels            []Label              `json:"labels"`
	CommentCount      int                  `json:"comment_count"`
	Bug               *Bug                 `json:"bug,omitempty"`
	AgentWork         *AgentWork           `json:"agent_work,omitempty"`
	DependencySummary DependencySummary    `json:"dependency_summary"`
	Checklist         []TaskChecklistItem  `json:"checklist"`
	ChecklistSummary  TaskChecklistSummary `json:"checklist_summary"`
	// ParentTaskID is the persisted hierarchy edge. ParentID is a response
	// alias retained for clients that use the shorter relation naming; both
	// values are populated from the same live, same-project parent row.
	ParentTaskID     *string                 `json:"parent_task_id,omitempty"`
	ParentID         *string                 `json:"parent_id,omitempty"`
	Parent           *TaskHierarchyReference `json:"parent,omitempty"`
	HierarchySummary HierarchySummary        `json:"hierarchy_summary"`
	// ReleaseID is an explicit, nullable project-local planning assignment.
	// Release is the compact read reference populated by the store in batches
	// for task collections.
	ReleaseID *string           `json:"release_id,omitempty"`
	Release   *ReleaseReference `json:"release,omitempty"`
	// AlertSource is read-only external alert evidence for tasks created by
	// alert intake; omitted for all other tasks.
	AlertSource *AlertSource `json:"alert_source,omitempty"`
	// Ticket is present only for Tickets workspace members.
	Ticket *TicketMembership `json:"ticket,omitempty"`
}
