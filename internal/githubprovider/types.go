// Package githubprovider reads bounded GitHub evidence. It never executes
// repository content or makes mutations upstream.
package githubprovider

import (
	"context"
	"net/http"
	"time"
)

type Options struct {
	Enabled      bool
	TokenFile    string
	Repositories []string
	// HTTPClient and Endpoint are constructor-only injection points for tests.
	// Runtime configuration must never expose an API origin override.
	HTTPClient *http.Client
	Endpoint   string
}

type Diagnostics struct {
	Enabled             bool      `json:"enabled"`
	Configured          bool      `json:"configured"`
	State               string    `json:"state"`
	Message             string    `json:"message"`
	AllowedRepositories []string  `json:"allowed_repositories"`
	CheckedAt           time.Time `json:"checked_at"`
}

type PullRequestIdentity struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
}

type Check struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
	SHA        string `json:"sha"`
}

type PullRequestSnapshot struct {
	Repository   string    `json:"repository"`
	Number       int       `json:"number"`
	URL          string    `json:"url"`
	RepositoryID int64     `json:"repository_id"`
	Title        string    `json:"title"`
	State        string    `json:"state"`
	Draft        bool      `json:"draft"`
	HeadSHA      string    `json:"head_sha"`
	BaseSHA      string    `json:"base_sha"`
	MergeSHA     string    `json:"merge_sha,omitempty"`
	ChecksState  string    `json:"checks_state"`
	Checks       []Check   `json:"checks"`
	ReviewState  string    `json:"review_state"`
	FetchedAt    time.Time `json:"fetched_at"`
	Stale        bool      `json:"stale"`
	Partial      bool      `json:"partial"`
}

type ResolvedRef struct {
	Repository   string `json:"repository"`
	RepositoryID int64  `json:"repository_id"`
	Ref          string `json:"ref"`
	SHA          string `json:"sha"`
	URL          string `json:"url"`
}

type TreeEntry struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

type File struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	SHA        string `json:"sha"` // immutable blob SHA
	CommitSHA  string `json:"commit_sha"`
	URL        string `json:"url"`
	Text       string `json:"text"`
}

// Provider describes the evidence operations; clients retain authorization
// ownership and must recheck their project/repository selection on each use.
type Provider interface {
	Diagnostics(context.Context) Diagnostics
	FetchPullRequest(context.Context, string, int) (PullRequestSnapshot, error)
	Resolve(context.Context, string, string) (ResolvedRef, error)
	ListFiles(context.Context, string, string) ([]TreeEntry, bool, error)
	ReadFile(context.Context, string, string, string) (File, error)
}

type Error struct{ Code string }

func (e *Error) Error() string {
	switch e.Code {
	case "not_configured":
		return "GitHub integration is not configured."
	case "permission_required":
		return "GitHub repository access or token permissions are required."
	case "rate_limited":
		return "GitHub rate limit reached; retry later."
	case "repository_not_allowed":
		return "Repository is outside the configured GitHub allowlist."
	case "invalid_input":
		return "Invalid GitHub repository, reference, path or pull request."
	case "head_changed":
		return "Pull request head changed during capture; refresh the evidence."
	case "file_unavailable":
		return "File is missing, binary, oversized or unsupported."
	default:
		return "GitHub is temporarily unavailable; retry later."
	}
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "unavailable"
}
func providerError(code string) error { return &Error{Code: code} }
