// Package betaswitch contains the root-owned beta release switch broker and
// the small Unix-socket client used by the HTTP API.
package betaswitch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultSocketPath is the only socket path used by the installed daemon.
	DefaultSocketPath = "/run/helm-beta-switcher/helm-beta-switchd.sock"
	// DefaultReleasesDir is the immutable release root owned by root.
	DefaultReleasesDir = "/var/lib/roadmap/releases"
	// DefaultCurrentPath is the active release pointer maintained by deploy.
	DefaultCurrentPath = "/var/lib/roadmap/current"
	// DefaultStateDir stores durable switch jobs.
	DefaultStateDir = "/var/lib/roadmap/beta-switch-jobs"
	// DefaultRollbackPath is the fixed rollback helper. Requests never provide
	// an executable path or any other command-line argument.
	DefaultRollbackPath = "/usr/local/sbin/helm-rollback"
	// DefaultSocketGroup is the group permitted to use the broker socket.
	DefaultSocketGroup = "roadmap"
	// DefaultBranch is the deployment branch used by the standard beta daemon.
	// A broker configured with an empty Branch accepts any validated
	// refs/heads/<branch> label, which is useful for retained test releases.
	DefaultBranch = "beta"
	// DefaultMaxBodyBytes bounds every request body accepted by the broker.
	DefaultMaxBodyBytes int64 = 4096
	// DefaultMaxResponseBytes bounds every response body accepted by the
	// client, protecting callers from a compromised or misconfigured broker.
	DefaultMaxResponseBytes int64 = 1 << 20
	// DefaultStartupGrace lets the accepted HTTP response flush before the
	// fixed rollback helper stops the running application service.
	DefaultStartupGrace = time.Second
	// MaxCommitSubjectBytes bounds the optional commit subject retained with a
	// beta build. Subjects are validated as UTF-8 and never contain controls or
	// line separators, so the field is safe to expose in owner-facing JSON.
	MaxCommitSubjectBytes = 160
)

var (
	shaPattern           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	jobPattern           = regexp.MustCompile(`^[0-9a-f]{32}$`)
	branchSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,200}$`)
)

// Release is a retained release that passed all broker validation checks.
// Ref is the signed release branch/ref label (normally refs/heads/beta).
type Release struct {
	SHA     string `json:"sha"`
	Ref     string `json:"ref"`
	Subject string `json:"subject,omitempty"`
	Current bool   `json:"current"`
}

// ReleasesResponse is the response envelope for GET /v1/releases.
type ReleasesResponse struct {
	CurrentSHA string    `json:"current_sha"`
	Releases   []Release `json:"releases"`
}

// SwitchRequest is the POST /v1/switch request envelope.
type SwitchRequest struct {
	SHA string `json:"sha"`
}

// Job is the durable status returned by switch operations. Error contains a
// stable, sanitized error class only; it never contains command output or
// filesystem details.
type Job struct {
	ID        string `json:"id"`
	TargetSHA string `json:"target_sha"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
}

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
)

// Client is the interface consumed by the HTTP API. Implementations should
// communicate with a broker rather than invoking deployment commands.
type Client interface {
	ListReleases(context.Context) (ReleasesResponse, error)
	RequestSwitch(context.Context, string) (Job, error)
	GetJob(context.Context, string) (Job, error)
}

// ErrorCode is a stable local/HTTP error classification. Internal details are
// deliberately kept out of API responses.
type ErrorCode string

const (
	ErrInvalidRequest  ErrorCode = "invalid_request"
	ErrUnauthorized    ErrorCode = "unauthorized"
	ErrNotFound        ErrorCode = "not_found"
	ErrConflict        ErrorCode = "conflict"
	ErrReleaseInvalid  ErrorCode = "release_invalid"
	ErrStateFailure    ErrorCode = "state_failure"
	ErrRollbackFailure ErrorCode = "rollback_failed"
	ErrUnavailable     ErrorCode = "unavailable"
	ErrInternal        ErrorCode = "internal"
)

// CodedError keeps an error's public class separate from its private cause.
// Error() intentionally returns only the class so callers cannot accidentally
// leak paths or command output in an HTTP response.
type CodedError struct {
	Code  ErrorCode
	Cause error
}

func (e *CodedError) Error() string {
	if e == nil || e.Code == "" {
		return string(ErrInternal)
	}
	return string(e.Code)
}

func (e *CodedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func coded(code ErrorCode, cause error) error {
	return &CodedError{Code: code, Cause: cause}
}

func codeOf(err error) ErrorCode {
	var codedErr *CodedError
	if errors.As(err, &codedErr) && codedErr != nil && codedErr.Code != "" {
		return codedErr.Code
	}
	return ErrInternal
}

func validSHA(sha string) bool  { return shaPattern.MatchString(sha) }
func validJobID(id string) bool { return jobPattern.MatchString(id) }

func validateBranchLabel(branch string) error {
	if branch == "" || len(branch) > 201 || strings.ContainsAny(branch, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x20~^:?*[\\") {
		return fmt.Errorf("invalid branch label")
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "//") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return fmt.Errorf("invalid branch label")
	}
	parts := strings.Split(branch, "/")
	for _, part := range parts {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") || !branchSegmentPattern.MatchString(part) {
			return fmt.Errorf("invalid branch label")
		}
	}
	if strings.HasSuffix(strings.ToLower(branch), ".lock") {
		return fmt.Errorf("invalid branch label")
	}
	return nil
}

func validateSHA(sha string) error {
	if !validSHA(sha) {
		return fmt.Errorf("invalid release SHA")
	}
	return nil
}

func validateJobID(id string) error {
	if !validJobID(id) {
		return fmt.Errorf("invalid job ID")
	}
	return nil
}

// ValidCommitSubject accepts only the canonical, single-line form written to
// retained release metadata. The subject is optional on old retained builds,
// but a present value must be valid before it can cross the broker boundary.
func ValidCommitSubject(subject string) bool {
	if subject == "" || !utf8.ValidString(subject) || len([]byte(subject)) > MaxCommitSubjectBytes {
		return false
	}
	if strings.TrimSpace(subject) != subject {
		return false
	}
	for _, r := range subject {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}
