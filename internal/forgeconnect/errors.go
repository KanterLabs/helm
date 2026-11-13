package forgeconnect

import "errors"

// Error codes (contract C1). They are the only detail an error carries:
// upstream bodies, error descriptions and secrets never reach an error.
const (
	CodeUnavailable        = "unavailable"
	CodeInvalidServerURL   = "invalid_server_url"
	CodeInsecureHTTP       = "insecure_http"
	CodeServerUnreachable  = "server_unreachable"
	CodeTLSError           = "tls_error"
	CodeNotAForge          = "not_a_forge"
	CodeIssuerMismatch     = "issuer_mismatch"
	CodeAlreadyConnected   = "already_connected"
	CodeStateInvalid       = "state_invalid"
	CodeDenied             = "denied"
	CodeScopeConflict      = "scope_conflict"
	CodeAuthorizeFailed    = "authorize_failed"
	CodeClientConfidential = "client_confidential"
	CodeClientUnknown      = "client_unknown"
	CodeExchangeFailed     = "exchange_failed"
	CodeStorageFailed      = "storage_failed"
	CodeReconnectRequired  = "reconnect_required"
)

// Error is the only error type this package returns.
type Error struct {
	Code string
	// Issuer is set only for issuer_mismatch: the address the server says
	// it has, normalized and at most 255 characters.
	Issuer string
}

func (e *Error) Error() string { return "gitea connection: " + e.Code }

// Code returns the C1 code of err, or "" for nil or a foreign error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func fail(code string) error { return &Error{Code: code} }
