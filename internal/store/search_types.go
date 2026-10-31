package store

import (
	"time"
)

// SearchSort describes one stable ordering term for global task search and
// saved views.  The HTTP layer validates the field/direction vocabulary before
// a query reaches the store; the store still normalizes values so direct
// callers cannot inject SQL through a saved view.
type SearchSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

// SearchFilter is the cross-project task search contract. ProjectIDs is the
// caller's effective project ceiling and is always applied before pagination.
// A non-nil empty slice intentionally matches no projects.
type SearchFilter struct {
	Query       string
	Key         string
	Title       string
	Description string
	Label       string
	State       string
	Priority    string
	Assignee    string
	ClaimOwner  string
	Project     string
	// ReleaseID is a stable opaque release identifier or "unassigned" for
	// cross-project searches. The HTTP layer resolves project-local names.
	ReleaseID  string
	ProjectIDs []string
	DueFrom    *time.Time
	DueTo      *time.Time
	Sort       []SearchSort
	Cursor     int
	Limit      int
}

// SavedView stores a named search/filter combination. Filters are kept as a
// JSON object because the public search vocabulary can grow without another
// schema migration; the HTTP layer validates keys and values when creating or
// updating a view.
type SavedView struct {
	ID          string         `json:"id"`
	ActorID     string         `json:"owner_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Filters     map[string]any `json:"filters"`
	Sort        []SearchSort   `json:"sort"`
	Shared      bool           `json:"shared"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
}

// SavedViewInput is used by create and patch operations. FiltersSet and
// SortSet distinguish an omitted patch field from an explicit empty value.
type SavedViewInput struct {
	Name        *string
	Description *string
	Filters     map[string]any
	FiltersSet  bool
	Sort        []SearchSort
	SortSet     bool
	Shared      *bool
}

type ProjectFilter struct {
	IncludeArchived bool
	FavoriteOnly    bool
	// ProjectIDs restricts the query to an explicit allow-list. A non-nil empty
	// slice intentionally matches no projects, which is useful for scoped
	// bearer tokens with no effective project access.
	ProjectIDs []string
}

type EventFilter struct {
	After     int64
	ProjectID string
	// ProjectIDs restricts the query to an explicit allow-list. A non-nil empty
	// slice intentionally matches no events.
	ProjectIDs []string
	Limit      int
}
