package store

import (
	"encoding/json"
)

// This file contains the portability format and its transactional importer.
// Portability is deliberately separate from the SQLite backup/restore path:
// exports contain live, user-facing records and imports only insert records
// after the complete archive has been validated.  No import operation ever
// replaces, deletes, or restores the database.

const (
	PortableFormat         = "helm.portable"
	PortableVersion        = 2 // Retained v2 constant for legacy callers.
	PortableVersionCurrent = 4
	portableMaxProjects    = 100
	portableMaxColumns     = 1000
	portableMaxTasks       = 10000
	portableMaxReleases    = 1000
	portableMaxLabels      = 5000
	portableMaxComments    = 50000
	portableMaxAgentNotes  = 50000
	portableMaxRelations   = 50000
	portableMaxEvents      = 100000
	portableMaxActivity    = 100000
	portableConflictRemap  = "remap"
	portableConflictFail   = "fail"
)

// PortableSource identifies the producer without carrying deployment or
// credential metadata.  It gives operators a stable way to inspect an
// archive before importing it.
type PortableSource struct {
	Product string `json:"product"`
	API     string `json:"api"`
}

type PortableProject struct {
	ChecklistCompletionPolicy string  `json:"checklist_completion_policy,omitempty"`
	ID                        string  `json:"id"`
	Key                       string  `json:"key"`
	Slug                      string  `json:"slug"`
	Name                      string  `json:"name"`
	Description               string  `json:"description"`
	Color                     string  `json:"color"`
	Favorite                  bool    `json:"favorite"`
	ArchivedAt                *string `json:"archived_at,omitempty"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
}

type PortableColumn struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	Name          string `json:"name"`
	SemanticState string `json:"semantic_state"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// PortableRelease is the persistence shape for a planned or released
// project release. Status is derived from ReleasedAt by the store; it is
// included as a convenience for consumers and accepted as an optional import
// hint for v2 archives. A v1 archive has no release records.
type PortableRelease struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	TargetDate  *string `json:"target_date,omitempty"`
	Status      string  `json:"status,omitempty"`
	ReleasedAt  *string `json:"released_at,omitempty"`
	ReleasedBy  *string `json:"released_by,omitempty"`
	Version     int64   `json:"version"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type PortableLabel struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type PortableBug struct {
	ReporterID        string  `json:"reporter_id"`
	Severity          *string `json:"severity,omitempty"`
	ActualBehavior    string  `json:"actual_behavior"`
	ExpectedBehavior  string  `json:"expected_behavior"`
	ReproductionSteps string  `json:"reproduction_steps"`
	Environment       string  `json:"environment"`
	AffectedVersion   string  `json:"affected_version"`
	Resolution        *string `json:"resolution,omitempty"`
	ResolvedBy        *string `json:"resolved_by,omitempty"`
	ResolvedAt        *string `json:"resolved_at,omitempty"`
	DuplicateOf       *string `json:"duplicate_of,omitempty"`
}

type PortableTask struct {
	ParentTaskID   *string                 `json:"parent_task_id,omitempty"`
	Checklist      []PortableChecklistItem `json:"checklist,omitempty"`
	ID             string                  `json:"id"`
	Number         int                     `json:"number"`
	ProjectID      string                  `json:"project_id"`
	Kind           string                  `json:"kind"`
	ColumnID       string                  `json:"column_id"`
	Title          string                  `json:"title"`
	Description    string                  `json:"description"`
	Priority       string                  `json:"priority"`
	Position       float64                 `json:"position"`
	AssigneeID     *string                 `json:"assignee_id,omitempty"`
	ClaimedBy      *string                 `json:"claimed_by,omitempty"`
	ClaimExpiresAt *string                 `json:"claim_expires_at,omitempty"`
	DueAt          *string                 `json:"due_at,omitempty"`
	Version        int64                   `json:"version"`
	CompletedAt    *string                 `json:"completed_at,omitempty"`
	ReleaseID      *string                 `json:"release_id,omitempty"`
	CreatedAt      string                  `json:"created_at"`
	UpdatedAt      string                  `json:"updated_at"`
	Bug            *PortableBug            `json:"bug,omitempty"`
}

type PortableTaskLabel struct {
	TaskID  string `json:"task_id"`
	LabelID string `json:"label_id"`
}

type PortableDependency struct {
	TaskID         string  `json:"task_id"`
	PrerequisiteID string  `json:"prerequisite_task_id"`
	CreatedBy      *string `json:"created_by,omitempty"`
	CreatedAt      string  `json:"created_at"`
}

type PortableTaskLink struct {
	SourceTaskID string `json:"source_task_id"`
	TargetTaskID string `json:"target_task_id"`
	LinkType     string `json:"link_type"`
	CreatedAt    string `json:"created_at"`
}

type PortableComment struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	ActorID   string `json:"actor_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type PortableAgentNote struct {
	ID         string   `json:"id"`
	TaskID     string   `json:"task_id"`
	ActorID    string   `json:"actor_id"`
	Category   string   `json:"category"`
	Body       string   `json:"body"`
	Evidence   []string `json:"evidence"`
	Version    int64    `json:"version"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
	ResolvedAt *string  `json:"resolved_at,omitempty"`
}

type PortableEvent struct {
	Cursor    int64           `json:"cursor"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	ActorID   *string         `json:"actor_id,omitempty"`
	ProjectID *string         `json:"project_id,omitempty"`
	TaskID    *string         `json:"task_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// PortableActorReference contains only safe display metadata.  Password
// hashes, tokens, sessions, and Codex credentials are never portable.
type PortableActorReference struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	DisabledAt  *string `json:"disabled_at,omitempty"`
}

type PortableAgentWork struct {
	TaskID              string   `json:"task_id"`
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
}

type PortableAgentWorkHistory struct {
	ID                  string   `json:"id"`
	TaskID              string   `json:"task_id"`
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
	CreatedAt           string   `json:"created_at"`
	GeneratedCommentID  *string  `json:"generated_comment_id,omitempty"`
	ProgressEventCursor *int64   `json:"progress_event_cursor,omitempty"`
}

type PortableRelationships struct {
	TaskLabels   []PortableTaskLabel  `json:"task_labels"`
	Dependencies []PortableDependency `json:"dependencies"`
	TaskLinks    []PortableTaskLink   `json:"task_links"`
}

type PortableActivity struct {
	Events           []PortableEvent            `json:"events"`
	AgentWork        []PortableAgentWork        `json:"agent_work"`
	AgentWorkHistory []PortableAgentWorkHistory `json:"agent_work_history"`
}

// PortableArchive is the documented JSON portability envelope.  The nested
// relationships/activity sections are canonical.  The flat fields are
// accepted as import aliases for early clients and are omitted by exports.
type PortableArchive struct {
	Warnings      []string                 `json:"warnings,omitempty"`
	Format        string                   `json:"format"`
	Version       int                      `json:"version"`
	ExportedAt    string                   `json:"exported_at"`
	Source        PortableSource           `json:"source"`
	Projects      []PortableProject        `json:"projects"`
	Columns       []PortableColumn         `json:"columns"`
	Releases      []PortableRelease        `json:"releases"`
	Tasks         []PortableTask           `json:"tasks"`
	Labels        []PortableLabel          `json:"labels"`
	Actors        []PortableActorReference `json:"actors,omitempty"`
	Relationships PortableRelationships    `json:"relationships"`
	Activity      PortableActivity         `json:"activity"`
	Github        *PortableGithubArchive   `json:"github,omitempty"`

	TaskLabels       []PortableTaskLabel        `json:"task_labels,omitempty"`
	Dependencies     []PortableDependency       `json:"dependencies,omitempty"`
	TaskLinks        []PortableTaskLink         `json:"task_links,omitempty"`
	Events           []PortableEvent            `json:"events,omitempty"`
	AgentWork        []PortableAgentWork        `json:"agent_work,omitempty"`
	AgentWorkHistory []PortableAgentWorkHistory `json:"agent_work_history,omitempty"`
	Comments         []PortableComment          `json:"comments"`
	AgentNotes       []PortableAgentNote        `json:"agent_notes,omitempty"`
}

func (a *PortableArchive) normalize() {
	normalizePortableChecklistOrder(a)
	// Version 1 predates releases. Treat any release-shaped fields supplied by
	// a permissive decoder as unknown v1 extensions so importing a legacy
	// archive can never assign existing tasks to a release. Copy the task slice
	// first because callers may retain the archive after validation/import.
	if a.Version == 1 {
		if len(a.Tasks) > 0 {
			a.Tasks = append([]PortableTask(nil), a.Tasks...)
			for index := range a.Tasks {
				a.Tasks[index].ReleaseID = nil
			}
		}
		a.Releases = nil
	}
	if len(a.Relationships.TaskLabels) == 0 && len(a.TaskLabels) > 0 {
		a.Relationships.TaskLabels = a.TaskLabels
	}
	if len(a.Relationships.Dependencies) == 0 && len(a.Dependencies) > 0 {
		a.Relationships.Dependencies = a.Dependencies
	}
	if len(a.Relationships.TaskLinks) == 0 && len(a.TaskLinks) > 0 {
		a.Relationships.TaskLinks = a.TaskLinks
	}
	if len(a.Activity.Events) == 0 && len(a.Events) > 0 {
		a.Activity.Events = a.Events
	}
	if len(a.Activity.AgentWork) == 0 && len(a.AgentWork) > 0 {
		a.Activity.AgentWork = a.AgentWork
	}
	if len(a.Activity.AgentWorkHistory) == 0 && len(a.AgentWorkHistory) > 0 {
		a.Activity.AgentWorkHistory = a.AgentWorkHistory
	}
	if a.Projects == nil {
		a.Projects = []PortableProject{}
	}
	if a.Columns == nil {
		a.Columns = []PortableColumn{}
	}
	if a.Releases == nil {
		a.Releases = []PortableRelease{}
	}
	if a.Tasks == nil {
		a.Tasks = []PortableTask{}
	}
	if a.Labels == nil {
		a.Labels = []PortableLabel{}
	}
	if a.Comments == nil {
		a.Comments = []PortableComment{}
	}
	if a.AgentNotes == nil {
		a.AgentNotes = []PortableAgentNote{}
	}
	if a.Actors == nil {
		a.Actors = []PortableActorReference{}
	}
	if a.Relationships.TaskLabels == nil {
		a.Relationships.TaskLabels = []PortableTaskLabel{}
	}
	if a.Relationships.Dependencies == nil {
		a.Relationships.Dependencies = []PortableDependency{}
	}
	if a.Relationships.TaskLinks == nil {
		a.Relationships.TaskLinks = []PortableTaskLink{}
	}
	if a.Activity.Events == nil {
		a.Activity.Events = []PortableEvent{}
	}
	if a.Activity.AgentWork == nil {
		a.Activity.AgentWork = []PortableAgentWork{}
	}
	if a.Activity.AgentWorkHistory == nil {
		a.Activity.AgentWorkHistory = []PortableAgentWorkHistory{}
	}
}
