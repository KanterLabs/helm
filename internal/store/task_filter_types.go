package store

import (
	"encoding/json"
	"time"
)

type Event struct {
	Cursor    int64           `json:"cursor"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	ActorID   *string         `json:"actor_id,omitempty"`
	ProjectID *string         `json:"project_id,omitempty"`
	TaskID    *string         `json:"task_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

type TaskFilter struct {
	State      string
	Column     string
	Priority   string
	Label      string
	Assignee   string
	Kind       string
	Severity   string
	Reporter   string
	Resolution string
	// ReleaseID filters explicit release membership. The value "unassigned"
	// (or "none") selects tasks whose release_id is NULL.
	ReleaseID string
	// Release is a compatibility alias for project-scoped callers that use the
	// shorter query vocabulary; ReleaseID takes precedence when both are set.
	Release string
	// Dependency selects derived graph readiness. "blocked" matches tasks
	// with an unmet prerequisite; "ready" matches tasks with at least one
	// prerequisite and none unmet. Empty leaves the graph state unfiltered.
	Dependency   string
	AgentState   string
	ActionNeeded bool
	LiveWork     bool
	Query        string
	UpdatedAfter *time.Time
	// CursorToken is the opaque keyset cursor used by the large-board task
	// endpoint. Cursor remains an integer offset for retained callers and
	// older collection clients; new task reads prefer CursorToken so inserts
	// and moves before a page boundary cannot cause skips or duplicates.
	CursorToken string
	// Sort selects the stable task ordering. Empty means board order. The
	// HTTP layer validates the public values, while store callers receive the
	// same invalid-input error for unsupported values.
	Sort       string
	Descending bool
	// ProjectIDs restricts global issue listings to an explicit allow-list.
	// A non-nil empty slice intentionally matches no projects.
	ProjectIDs []string
	// Cursor is an opaque row offset for task and my-work listings. Event
	// cursors remain monotonic database cursors via EventFilter.After.
	Cursor int
	Limit  int
}
