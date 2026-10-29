package httpapi

import (
	"context"
	"errors"
)

// This error contains only an authored literal, never a model/provider error.
type githubDiscoveryFailure string

func (e githubDiscoveryFailure) Error() string { return string(e) }

func githubDiscoveryDiagnostic(err error) string {
	var failure githubDiscoveryFailure
	if errors.As(err, &failure) {
		return string(failure)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "discovery_deadline"
	}
	return ""
}
