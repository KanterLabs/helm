package httpapi

import (
	"net/http"
	"testing"

	"github.com/KanterLabs/helm/internal/githubconnect"
)

// Failure list: github_connection_failures.md, "Found by the independent
// review (WP7)".
func TestGithubConnectionReviewFindings(t *testing.T) {
	e := newConnectionEnv(t)

	// Connect and disconnect take no input.
	for _, route := range []string{connRoute, discRoute} {
		for _, body := range []string{`[]`, `"x"`, `{"attempt_id":"a"}`, `{"scope":"admin"}`, `{}{}`, `{`, `null`, `1`} {
			if code := e.errorCode(e.call("owner", http.MethodPost, route, body), http.StatusBadRequest); code != "invalid_request" {
				t.Fatalf("%s body %q: code %s", route, body, code)
			}
		}
	}
	if calls := e.gh.read(func(g *connGitHub) int { return g.deviceCalls }); calls != 0 {
		t.Fatalf("a refused body reached GitHub %d times", calls)
	}

	// An empty object is as good as no body.
	began := e.document(e.call("owner", http.MethodPost, connRoute, `{}`))
	attempt := began.Connection.Attempt
	if attempt == nil || attempt.State != githubconnect.AttemptPending {
		t.Fatalf("connect with an empty object: %+v", began.Connection)
	}

	// Only the beginner cancels. Another administrator's cancel changes nothing.
	other := e.document(e.call("admin", http.MethodPost, cancelRoute, `{"attempt_id":"`+attempt.ID+`"}`))
	if other.Connection.Attempt == nil || other.Connection.Attempt.ID != attempt.ID || other.Connection.Attempt.UserCode != "" {
		t.Fatalf("another administrator's cancel: %+v", other.Connection.Attempt)
	}
	if mine := e.document(e.call("owner", http.MethodGet, docRoute, "")); mine.Connection.Attempt == nil || mine.Connection.Attempt.UserCode == "" {
		t.Fatal("the beginner lost the attempt to another administrator's cancel")
	}

	// A loop of connect and cancel is cut off before it hammers GitHub.
	e.document(e.call("owner", http.MethodPost, cancelRoute, `{"attempt_id":"`+attempt.ID+`"}`))
	var limited bool
	for i := 0; i < 20 && !limited; i++ {
		res := e.call("owner", http.MethodPost, connRoute, "")
		if res.Code == http.StatusTooManyRequests {
			if code := e.errorCode(res, http.StatusTooManyRequests); code != "rate_limited" || res.Header().Get("Retry-After") == "" || res.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("limit answer: %s, Retry-After %q", code, res.Header().Get("Retry-After"))
			}
			limited = true
			break
		}
		doc := e.document(res)
		e.document(e.call("owner", http.MethodPost, cancelRoute, `{"attempt_id":"`+doc.Connection.Attempt.ID+`"}`))
	}
	if !limited {
		t.Fatal("20 connects in a row were all accepted")
	}
	if calls := e.gh.read(func(g *connGitHub) int { return g.deviceCalls }); calls > 6 {
		t.Fatalf("GitHub was asked for %d device codes", calls)
	}
}
