package githubprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sentinel = "SYNTHETIC_PROVIDER_SENTINEL_NEVER_DISCLOSE"
const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func fixture(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte(sentinel), 0600); err != nil {
		t.Fatal(err)
	}
	return New(Options{Enabled: true, TokenFile: token, Repositories: []string{"example/repo"}, HTTPClient: s.Client(), Endpoint: s.URL})
}

func TestURLAndScope(t *testing.T) {
	for _, raw := range []string{"http://github.com/example/repo/pull/1", "https://github.com.evil.invalid/example/repo/pull/1", "https://user@github.com/example/repo/pull/1", "https://github.com/example/../pull/1", "https://github.com/example/repo/pull/1%2f2"} {
		if _, err := ParsePullRequestURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	p, err := ParsePullRequestURL("https://GitHub.com/Example/Repo/pull/42/files?diff=split#x")
	if err != nil || p.Repository != "example/repo" || p.Number != 42 || p.URL != "https://github.com/example/repo/pull/42" {
		t.Fatalf("canonical identity: %+v %v", p, err)
	}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("out-of-scope request") })
	if _, err := c.Resolve(context.Background(), "other/repo", "main"); ErrorCode(err) != "repository_not_allowed" {
		t.Fatalf("%v", err)
	}
	if _, err := c.ReadFile(context.Background(), "example/repo", testSHA, "../secret"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestCredentialsAndSanitizedFailures(t *testing.T) {
	c := New(Options{Enabled: true, Repositories: []string{"example/repo"}})
	if d := c.Diagnostics(context.Background()); d.State != "not_configured" || d.Configured {
		t.Fatalf("%+v", d)
	}
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, sentinel) })
			_, err := c.Resolve(context.Background(), "example/repo", "main")
			if err == nil || strings.Contains(err.Error(), sentinel) {
				t.Fatalf("unsafe failure: %v", err)
			}
			b, _ := json.Marshal(c.Diagnostics(context.Background()))
			if strings.Contains(string(b), sentinel) {
				t.Fatal("credential in diagnostics")
			}
		})
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"sha":"`+testSHA+`"}`) })
	if err := os.Chmod(c.tokenFile, 0644); err != nil {
		t.Fatal(err)
	}
	if d := c.Diagnostics(context.Background()); d.Configured {
		t.Fatalf("insecure token accepted: %+v", d)
	}
}

func prReply(w http.ResponseWriter, sha string) {
	fmt.Fprintf(w, `{"id":3,"title":"Synthetic PR","state":"open","head":{"sha":%q},"base":{"sha":%q,"repo":{"id":11}},"html_url":"https://github.com/example/repo/pull/7"}`, sha, testSHA)
}

func TestPullRequestExactHeadAndForcePush(t *testing.T) {
	var reads atomic.Int32
	force := atomic.Bool{}
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			n := reads.Add(1)
			sha := testSHA
			if force.Load() && n%2 == 0 {
				sha = strings.Repeat("b", 40)
			}
			prReply(w, sha)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			fmt.Fprintf(w, `{"check_runs":[{"name":"tests","head_sha":%q,"status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/1"}],"total_count":1}`, testSHA)
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			fmt.Fprintf(w, `[{"context":"legacy","sha":%q,"state":"failure","target_url":"https://ci.example.invalid/1"}]`, testSHA)
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			fmt.Fprint(w, `[{"id":1,"state":"APPROVED","user":{"id":1}}]`)
		default:
			http.NotFound(w, r)
		}
	})
	p, err := c.FetchPullRequest(context.Background(), "example/repo", 7)
	if err != nil || p.ChecksState != "failing" || p.ReviewState != "approved" || len(p.Checks) != 2 {
		t.Fatalf("%+v %v", p, err)
	}
	force.Store(true)
	p, err = c.FetchPullRequest(context.Background(), "example/repo", 7)
	if ErrorCode(err) != "head_changed" || p.HeadSHA != "" {
		t.Fatalf("mixed result: %+v %v", p, err)
	}
}

func TestPartialUnknownAndPermissionCache(t *testing.T) {
	var denied atomic.Bool
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if denied.Load() {
			w.WriteHeader(403)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			prReply(w, testSHA)
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			w.Header().Set("Link", `<https://attacker.invalid/steal>; rel="next"`)
			fmt.Fprint(w, `{"check_runs":[],"total_count":500}`)
		case strings.HasSuffix(r.URL.Path, "/statuses"), strings.HasSuffix(r.URL.Path, "/reviews"):
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	})
	p, err := c.FetchPullRequest(context.Background(), "example/repo", 7)
	if err != nil || !p.Partial || p.ChecksState == "passing" {
		t.Fatalf("%+v %v", p, err)
	}
	denied.Store(true)
	p, err = c.FetchPullRequest(context.Background(), "example/repo", 7)
	if ErrorCode(err) != "permission_required" || p.Title != "" {
		t.Fatalf("private cache disclosed: %+v %v", p, err)
	}
}

func TestTimeoutRedirectAndRateBackoff(t *testing.T) {
	var hits atomic.Int32
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(429) })
	for i := 0; i < 2; i++ {
		if _, err := c.Resolve(context.Background(), "example/repo", "main"); ErrorCode(err) != "rate_limited" {
			t.Fatalf("%v", err)
		}
	}
	if hits.Load() != 1 {
		t.Fatal("rate limit did not back off")
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://attacker.invalid/token", 302)
	})
	if _, err := c.Resolve(context.Background(), "example/repo", "main"); err == nil {
		t.Fatal("followed redirect")
	}
	c = fixture(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Resolve(ctx, "example/repo", "main"); ErrorCode(err) != "unavailable" {
		t.Fatalf("%v", err)
	}
}

func TestPinnedSourceAndConditionalCache(t *testing.T) {
	var conditional atomic.Bool
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+sentinel {
			t.Error("missing provider credential")
		}
		switch {
		case r.URL.Path == "/repos/example/repo":
			fmt.Fprint(w, `{"id":11,"default_branch":"main"}`)
		case strings.Contains(r.URL.Path, "/commits/"):
			if r.Header.Get("If-None-Match") == `"synthetic"` {
				conditional.Store(true)
				w.WriteHeader(304)
				return
			}
			w.Header().Set("ETag", `"synthetic"`)
			fmt.Fprintf(w, `{"sha":%q}`, testSHA)
		case strings.Contains(r.URL.Path, "/git/trees/"):
			fmt.Fprintf(w, `{"truncated":true,"tree":[{"path":"README.md","mode":"100644","type":"blob","sha":%q,"size":4},{"path":"link","mode":"120000","type":"blob","sha":%q}]}`, testSHA, testSHA)
		case strings.Contains(r.URL.Path, "/contents/"):
			fmt.Fprintf(w, `{"type":"file","sha":%q,"encoding":"base64","content":"dGVzdA==","size":4}`, testSHA)
		default:
			http.NotFound(w, r)
		}
	})
	for i := 0; i < 2; i++ {
		s, err := c.Resolve(context.Background(), "example/repo", "main")
		if err != nil || s.SHA != testSHA {
			t.Fatalf("%+v %v", s, err)
		}
	}
	if !conditional.Load() {
		t.Fatal("conditional request missing")
	}
	entries, partial, err := c.ListFiles(context.Background(), "example/repo", testSHA)
	if err != nil || !partial || len(entries) != 1 {
		t.Fatalf("%+v %v %v", entries, partial, err)
	}
	f, err := c.ReadFile(context.Background(), "example/repo", testSHA, "README.md")
	if err != nil || f.Text != "test" || f.SHA != testSHA {
		t.Fatalf("%+v %v", f, err)
	}
}
