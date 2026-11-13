package forgeconnect

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// scopeMarker precedes the granted scope in Gitea's refusal: "token does not
// have at least one of required scope(s), required=[…], token scope=…".
// Gitea has worded it this way since 1.23, the first release with limited
// OAuth grants. Forgejo names no scope.
const scopeMarker = "token scope="

var readScope = regexp.MustCompile(`^read:[a-z]+$`)

// limitStatus reads the limit check: a read Helm did not ask for. 200 means
// the grant is not limited. A refusal means it is, unless the forge names the
// granted scope and that scope is more than reading. Scope belongs to the
// grant, not the token, so a grant widened on the forge after sign-in shows
// up here on the next check.
func limitStatus(r *reply) string {
	switch r.status {
	case http.StatusOK:
		return AccessFull
	case http.StatusForbidden:
		if grantedBeyondRead(r.body) {
			return AccessFull
		}
		return AccessScoped
	}
	return AccessUnknown
}

// grantedBeyondRead is true only on positive evidence: a named scope that is
// neither read:<category> nor public-only. The body is never kept or shown.
func grantedBeyondRead(body []byte) bool {
	var refusal struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &refusal) != nil {
		return false
	}
	_, list, named := strings.Cut(refusal.Message, scopeMarker)
	if !named {
		return false
	}
	if end := strings.IndexAny(list, " \t\r\n"); end >= 0 {
		list = list[:end]
	}
	for _, scope := range strings.Split(list, ",") {
		if scope == "" || scope == "public-only" || readScope.MatchString(scope) {
			continue
		}
		return true
	}
	return false
}
