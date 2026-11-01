package store

type PortableImportOptions struct {
	// TargetProjectID imports a one-project archive into an existing project.
	// The target is never overwritten; source IDs are mapped to it.
	TargetProjectID string
	// Conflict is "remap" (the default) or "fail".  Remapping never silently
	// overwrites a record and is reported in ImportReport.Remaps.
	Conflict string
	DryRun   bool
	ActorID  string
}

type PortableImportRemap struct {
	Entity string `json:"entity"`
	Source string `json:"source"`
	Target string `json:"target"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

type PortableImportIssue struct {
	Entity  string `json:"entity"`
	ID      string `json:"id,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type PortableImportCounts struct {
	ProjectsCreated     int `json:"projects_created"`
	ProjectsSkipped     int `json:"projects_skipped"`
	ColumnsCreated      int `json:"columns_created"`
	ColumnsSkipped      int `json:"columns_skipped"`
	ReleasesCreated     int `json:"releases_created"`
	ReleasesSkipped     int `json:"releases_skipped"`
	TasksCreated        int `json:"tasks_created"`
	TasksSkipped        int `json:"tasks_skipped"`
	LabelsCreated       int `json:"labels_created"`
	LabelsSkipped       int `json:"labels_skipped"`
	TaskLabelsCreated   int `json:"task_labels_created"`
	TaskLabelsSkipped   int `json:"task_labels_skipped"`
	CommentsCreated     int `json:"comments_created"`
	CommentsSkipped     int `json:"comments_skipped"`
	AgentNotesCreated   int `json:"agent_notes_created"`
	AgentNotesSkipped   int `json:"agent_notes_skipped"`
	DependenciesCreated int `json:"dependencies_created"`
	DependenciesSkipped int `json:"dependencies_skipped"`
	LinksCreated        int `json:"links_created"`
	LinksSkipped        int `json:"links_skipped"`
	EventsCreated       int `json:"events_created"`
	EventsSkipped       int `json:"events_skipped"`
	AgentWorkCreated    int `json:"agent_work_created"`
	AgentWorkSkipped    int `json:"agent_work_skipped"`
	HistoryCreated      int `json:"agent_work_history_created"`
	HistorySkipped      int `json:"agent_work_history_skipped"`
}

type PortableImportReport struct {
	Format   string                `json:"format"`
	Version  int                   `json:"version"`
	DryRun   bool                  `json:"dry_run"`
	Conflict string                `json:"conflict"`
	Counts   PortableImportCounts  `json:"counts"`
	Remaps   []PortableImportRemap `json:"remaps"`
	Warnings []string              `json:"warnings"`
	Errors   []PortableImportIssue `json:"errors"`
}
