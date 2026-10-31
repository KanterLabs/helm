package store

type Roadmap struct {
	Project              *Project         `json:"project,omitempty"`
	TaskTotals           int              `json:"task_total"`
	Completed            int              `json:"completed"`
	CompletionPercent    float64          `json:"completion_percent"`
	StateCounts          map[string]int   `json:"state_counts"`
	Overdue              int              `json:"overdue"`
	DueSoon              int              `json:"due_soon"`
	Upcoming             []Task           `json:"upcoming"`
	RecentActivity       []Event          `json:"recent_activity"`
	Projects             []map[string]any `json:"projects,omitempty"`
	TotalTasks           int              `json:"total_tasks,omitempty"`
	CompletedTasks       int              `json:"completed_tasks,omitempty"`
	CompletionPercentage float64          `json:"completion_percentage,omitempty"`
	OverdueCount         int              `json:"overdue_count,omitempty"`
	DueSoonCount         int              `json:"due_soon_count,omitempty"`
	UpcomingTasks        []Task           `json:"upcoming_tasks"`
}
