package githubprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// CredentialSource supplies the token the provider sends to GitHub.
type CredentialSource interface {
	// Current returns the token held now. It performs no network I/O.
	// identity changes only when authorization may have changed (different
	// token-file content, connect, disconnect). A refreshed token of the
	// same grant keeps its identity.
	Current() (token, identity string, err error)
	// Fresh is Current, but first renews a token that is close to expiry.
	Fresh(ctx context.Context) (token, identity string, err error)
}

// CredentialRejecter is optionally implemented by a CredentialSource. The
// provider calls Rejected once when GitHub answers 401 for token, before it
// denies the repository. It is never called while the provider holds a lock.
type CredentialRejecter interface {
	Rejected(token string)
}

// fileSource is the operator_token mode: the token file is read on every use
// and its identity is the token's SHA-256, so any rotation is a new identity.
type fileSource struct{ filename string }

func (s fileSource) Current() (string, string, error) {
	token, err := readToken(s.filename)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func (s fileSource) Fresh(context.Context) (string, string, error) { return s.Current() }

// checkCredential confines what a source returns: only the three source codes
// pass through, and a token that could not be sent verbatim is never used.
// The token file is already validated by readToken; strict adds the checks for
// an explicit source so operator_token mode behaves exactly as before.
func checkCredential(token, identity string, err error, strict bool) (string, string, error) {
	if err != nil {
		switch ErrorCode(err) {
		case "not_configured", "reconnect_required":
			return "", "", providerError(ErrorCode(err))
		default:
			return "", "", providerError("unavailable")
		}
	}
	if identity == "" || len(token) < 1 || len(token) > 4096 {
		return "", "", providerError("unavailable")
	}
	for _, r := range token {
		if strict && (unicode.IsSpace(r) || unicode.IsControl(r)) {
			return "", "", providerError("unavailable")
		}
	}
	if strings.ContainsAny(token, " \t\r\n\x00") {
		return "", "", providerError("unavailable")
	}
	return token, identity, nil
}
