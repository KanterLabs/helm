package store

import (
	"encoding/json"
)

// Watch follows either an entire project (TaskID nil) or one task. A watch is
// always owned by ActorID and can be removed by that actor.
type Watch struct {
	ID        string  `json:"id"`
	ActorID   string  `json:"actor_id"`
	ProjectID string  `json:"project_id"`
	TaskID    *string `json:"task_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// Notification is an in-app inbox item. Payload is intentionally retained as
// JSON so clients can handle new event-specific fields without a migration.
type Notification struct {
	ID          string  `json:"id"`
	RecipientID string  `json:"recipient_id"`
	ActorID     *string `json:"actor_id,omitempty"`
	EventType   string  `json:"event_type"`
	// Type is a compatibility alias for clients that use the shorter event
	// vocabulary. It is always the same value as EventType.
	Type      string          `json:"type,omitempty"`
	ProjectID *string         `json:"project_id,omitempty"`
	TaskID    *string         `json:"task_id,omitempty"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Payload   json.RawMessage `json:"payload"`
	DedupeKey string          `json:"dedupe_key"`
	ReadAt    *string         `json:"read_at,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// NotificationFilter controls an actor's inbox page.
type NotificationFilter struct {
	UnreadOnly bool
	Limit      int
	Offset     int
	// ProjectIDs restricts an inbox read to the effective project ceiling of
	// a scoped bearer token. Nil means an unscoped human/operator read.
	ProjectIDs []string
}

// NotificationPreferences controls which event categories create inbox
// items. All categories default to true for existing and new actors.
type NotificationPreferences struct {
	ActorID      string `json:"actor_id"`
	Assignments  bool   `json:"assignments"`
	Mentions     bool   `json:"mentions"`
	Blockers     bool   `json:"blockers"`
	StateChanges bool   `json:"state_changes"`
	UpdatedAt    string `json:"updated_at"`
}

// NotificationPreferencesInput is a partial update. Nil fields preserve the
// current preference; a non-nil pointer explicitly enables or disables it.
type NotificationPreferencesInput struct {
	Assignments  *bool
	Mentions     *bool
	Blockers     *bool
	StateChanges *bool
}
