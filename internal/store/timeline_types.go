package store

import (
	"encoding/json"
)

type Comment struct {
	ID      string `json:"id"`
	TaskID  string `json:"task_id"`
	ActorID string `json:"actor_id"`
	Body    string `json:"body"`
	// Version is incremented on every edit or tombstone mutation.  It gives
	// comment writes the same optimistic-concurrency protection as task writes.
	Version   int64  `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Deleted comments are retained for audit/event integrity but are omitted
	// from ordinary comment and timeline comment reads.
	DeletedAt *string `json:"deleted_at,omitempty"`
}

// AgentNote is bounded, curated task knowledge. It is intentionally separate
// from comments and progress: active notes are injected into agent recovery
// context, while resolved notes remain retained for auditability.
type AgentNote struct {
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

// TimelineActor is the least-privilege actor identity embedded in a task
// activity item.  Timeline reads intentionally do not expose actor email,
// administrator state, token metadata, or project grants.
type TimelineActor struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// TaskTimelineProgress is one immutable structured agent-work pulse.  The
// latest pulse remains available as Task.AgentWork; this shape is the durable
// historical counterpart used by the task timeline.
type TaskTimelineProgress struct {
	OperationID         string   `json:"operation_id"`
	ActorID             string   `json:"actor_id"`
	State               string   `json:"state"`
	Phase               string   `json:"phase"`
	Summary             string   `json:"summary"`
	NextAction          string   `json:"next_action"`
	CheckpointRefs      []string `json:"checkpoint_refs"`
	CheckpointCompleted *int     `json:"checkpoint_completed"`
	CheckpointTotal     *int     `json:"checkpoint_total"`
	StartedAt           string   `json:"started_at"`
}

// TaskTimelineChange retains the original event type and safe payload.  The
// payload is deliberately not converted into guessed rich fields: legacy
// events remain visible exactly as events when no structured history exists.
type TaskTimelineChange struct {
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// TaskTimelineItem is a discriminated union. Exactly one of Progress,
// Comment, or Change is non-nil for server-produced rows; the other fields are
// still emitted as JSON null so clients can switch on kind without guessing.
// Cursor is an opaque keyset token and is stable for this item across pages.
type TaskTimelineItem struct {
	ID        string                `json:"id"`
	Cursor    string                `json:"cursor"`
	Kind      string                `json:"kind"`
	TaskID    string                `json:"task_id"`
	Actor     *TimelineActor        `json:"actor"`
	CreatedAt string                `json:"created_at"`
	Progress  *TaskTimelineProgress `json:"progress"`
	Comment   *Comment              `json:"comment"`
	Change    *TaskTimelineChange   `json:"change"`
}

// TaskTimelineFilter controls a task-scoped timeline page. Before is the
// opaque cursor returned on an earlier item; an empty value starts at the
// newest activity. Kind may be agent_progress, comment, or task_change.
type TaskTimelineFilter struct {
	Before string
	Kind   string
	Limit  int
}

// TimelineFilter is a descriptive alias retained for callers that prefer a
// shorter name.
type TimelineFilter = TaskTimelineFilter
