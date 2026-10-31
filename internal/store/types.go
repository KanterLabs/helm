package store

import (
	"errors"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrConflict         = errors.New("conflict")
	ErrPrecondition     = errors.New("precondition required")
	ErrForbidden        = errors.New("forbidden")
	ErrAlreadyExists    = errors.New("already exists")
	ErrInvalid          = errors.New("invalid input")
	ErrClaimUnavailable = errors.New("claim unavailable")

	// Dependency mutations use distinct sentinels so the HTTP layer can map
	// each graph validation failure to a stable response code without parsing
	// human-readable error text. They intentionally unwrap through Error just
	// like the existing store errors.
	ErrDependencySelfReference = errors.New("dependency_self_reference")
	ErrDependencyCrossProject  = errors.New("dependency_cross_project")
	ErrDependencyAlreadyExists = errors.New("dependency_already_exists")
	ErrDependencyLimitExceeded = errors.New("dependency_limit_exceeded")
	ErrDependencyCycle         = errors.New("dependency_cycle")
	ErrDependencyNotFound      = errors.New("dependency_not_found")
	ErrUnmetDependencies       = errors.New("unmet_dependencies")
	ErrDependencyInUse         = errors.New("dependency_in_use")
	ErrChecklistLimitExceeded  = errors.New("checklist_limit_exceeded")
	ErrChecklistIncomplete     = errors.New("checklist_incomplete")

	// Release errors retain the broad store sentinels through errors.Join while
	// exposing stable lifecycle-specific identities to API callers.
	ErrReleaseNotFound           = errors.New("release_not_found")
	ErrReleaseNameExists         = errors.New("release_name_exists")
	ErrReleaseCrossProject       = errors.New("release_cross_project")
	ErrReleaseAlreadyCompleted   = errors.New("release_already_completed")
	ErrReleaseNotCompleted       = errors.New("release_not_completed")
	ErrReleaseHasTasks           = errors.New("release_has_tasks")
	ErrReleaseIncomplete         = errors.New("release_incomplete")
	ErrReleaseDependencyConflict = errors.New("release_dependency_conflict")
	ErrReleaseFrozen             = errors.New("release_frozen")
	ErrReleaseQueueChanged       = errors.New("release_queue_changed")
)

type Error struct {
	Kind    error
	Message string
	Details any
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Kind.Error()
}
func (e *Error) Unwrap() error { return e.Kind }

func invalid(message string, details any) error {
	return &Error{Kind: ErrInvalid, Message: message, Details: details}
}
func notFound(message string) error { return &Error{Kind: ErrNotFound, Message: message} }
func conflict(message string, details any) error {
	return &Error{Kind: ErrConflict, Message: message, Details: details}
}
func forbidden(message string) error { return &Error{Kind: ErrForbidden, Message: message} }
