package githubconnect

import "errors"

// Error codes returned by Begin, Sync and Disconnect. They are the only
// detail an error carries: GitHub's text, tokens and codes never reach an
// error. WP3 maps them to HTTP statuses; none of them means 502 or 504
// (amendment A2).
const (
	// CodeSigninUnavailable: no client ID is configured, or the manager is closed.
	CodeSigninUnavailable = "signin_unavailable"
	// CodeOperatorManaged is never returned by the manager. It is defined here
	// for the routes to answer in operator_token mode, where no manager exists.
	CodeOperatorManaged = "operator_managed"
	// CodeInvalidRequest: an empty actor ID.
	CodeInvalidRequest = "invalid_request"
	// CodeAlreadyConnected: Begin while a connection is in place.
	CodeAlreadyConnected = "already_connected"
	// CodeNotConnected: Sync with no connection.
	CodeNotConnected = "not_connected"
	// CodeGitHubUnreachable: GitHub could not be reached, timed out, answered
	// 5xx, redirected, or sent an oversized or malformed reply.
	CodeGitHubUnreachable = "github_unreachable"
	// CodeGitHubRefused: GitHub answered with an explicit refusal (an OAuth
	// error such as device_flow_disabled, or a 4xx other than 401).
	CodeGitHubRefused = "github_refused"
	// CodeStorageFailed: the credential file could not be written or removed.
	CodeStorageFailed = "storage_failed"
	// CodeReconnectRequired: the sign-in has ended; connect again.
	CodeReconnectRequired = "reconnect_required"
)

// Codes lists every code above.
var Codes = []string{CodeSigninUnavailable, CodeOperatorManaged, CodeInvalidRequest, CodeAlreadyConnected, CodeNotConnected,
	CodeGitHubUnreachable, CodeGitHubRefused, CodeStorageFailed, CodeReconnectRequired}

// Error is the only error type Begin, Sync and Disconnect return.
type Error struct{ Code string }

func (e *Error) Error() string { return "github connection: " + e.Code }

// Code returns the code of err, or "" for nil or a foreign error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func fail(code string) error { return &Error{Code: code} }

// codeOr keeps a code already chosen, or uses fallback.
func codeOr(err error, fallback string) error {
	if Code(err) != "" {
		return err
	}
	return fail(fallback)
}
