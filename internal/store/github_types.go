package store

// GithubRepository is one project-authorized repository/ref pair.
type GithubRepository struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}
type GithubSelection struct {
	Version      int64              `json:"version"`
	Repositories []GithubRepository `json:"repositories"`
}
type GithubCheck struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
	SHA        string `json:"sha"`
}
type GithubSnapshot struct {
	RepositoryID int64         `json:"repository_id"`
	Title        string        `json:"title"`
	State        string        `json:"state"`
	Draft        bool          `json:"draft"`
	HeadSHA      string        `json:"head_sha"`
	BaseSHA      string        `json:"base_sha"`
	MergeSHA     string        `json:"merge_sha,omitempty"`
	ChecksState  string        `json:"checks_state"`
	Checks       []GithubCheck `json:"checks"`
	ReviewState  string        `json:"review_state"`
	FetchedAt    string        `json:"fetched_at"`
	Stale        bool          `json:"stale"`
	Partial      bool          `json:"partial"`
}
type GithubArtifact struct {
	ID               string          `json:"id"`
	TaskID           string          `json:"task_id"`
	ProjectID        string          `json:"-"`
	Kind             string          `json:"kind"`
	Provider         string          `json:"provider"`
	URL              string          `json:"url"`
	Repository       string          `json:"repository"`
	Number           int             `json:"number"`
	CreatedAt        string          `json:"created_at"`
	Verification     string          `json:"verification"`
	ErrorCode        string          `json:"error_code,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	Snapshot         *GithubSnapshot `json:"snapshot,omitempty"`
	SelectionVersion int64           `json:"-"`
}
type GithubSummary struct {
	TaskID string `json:"task_id"`
	Count  int    `json:"count"`
	Label  string `json:"label"`
	State  string `json:"state"`
}
