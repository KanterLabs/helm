package store

type Project struct {
	ID                        string  `json:"id"`
	Key                       string  `json:"key"`
	Slug                      string  `json:"slug"`
	Name                      string  `json:"name"`
	Description               string  `json:"description"`
	Color                     string  `json:"color"`
	Favorite                  bool    `json:"favorite"`
	ArchivedAt                *string `json:"archived_at,omitempty"`
	ChecklistCompletionPolicy string  `json:"checklist_completion_policy"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
	TaskCount                 int     `json:"task_count,omitempty"`
	CompletedCount            int     `json:"completed_count,omitempty"`
	CompletedTaskCount        int     `json:"completed_task_count,omitempty"`
	OpenTaskCount             int     `json:"open_task_count,omitempty"`
	OverdueTaskCount          int     `json:"overdue_task_count,omitempty"`
	ColumnCount               int     `json:"column_count,omitempty"`
	Version                   int64   `json:"version,omitempty"`
	// SystemKind is "tickets" for the built-in ticket queue
	// (docs/TICKET_QUEUE_PLAN.md); empty for ordinary projects.
	SystemKind string `json:"system_kind,omitempty"`
}

type Column struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	Name            string  `json:"name"`
	SemanticState   string  `json:"semantic_state"`
	Position        int     `json:"position"`
	ArchivedAt      *string `json:"archived_at,omitempty"`
	OrderingVersion int64   `json:"ordering_version,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
	Version         int64   `json:"version,omitempty"`
}

type Label struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
