package githubprovider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Failure inventory: search grammar injection can escape repository scope;
// foreign repos/traversal can appear in results; search is default-branch hints,
// never pinned proof; Link/redirects can exfiltrate tokens; partial result pages
// need a bound; PR paths must be tied to a rechecked head and scoped origin.
type pathDiscovery interface {
	FindPaths(context.Context, string, string) ([]string, bool, error)
	PullRequestPaths(context.Context, string, int, string) ([]string, bool, error)
}

// An unavailable optional search capability must not revoke independently
// verified Contents access. A credential rejection still fences everything,
// and a real Contents permission rejection must retain the original behavior.
func TestGithubDiscoveryPermissionIsSeparateFromContents(t *testing.T) {
	for _, searchStatus := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(searchStatus), func(t *testing.T) {
			var contentsDenied atomic.Bool
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/search/code":
					w.WriteHeader(searchStatus)
					fmt.Fprint(w, `{"message":"synthetic optional capability denied"}`)
				case strings.Contains(r.URL.Path, "/git/trees/"):
					fmt.Fprint(w, `{"tree":[{"path":"main.go","type":"blob","mode":"100644","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":16}],"truncated":false}`)
				case strings.Contains(r.URL.Path, "/contents/"):
					if contentsDenied.Load() {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					fmt.Fprint(w, `{"type":"file","encoding":"base64","content":"c3ludGhldGljIHNvdXJjZQ==","size":16,"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
				default:
					http.NotFound(w, r)
				}
			})
			before := c.Generation()
			if _, _, err := c.FindPaths(context.Background(), "example/repo", "/healthz"); ErrorCode(err) != "permission_required" {
				t.Fatalf("expected bounded discovery denial: %v", err)
			}
			if searchStatus == http.StatusUnauthorized {
				if c.Generation() == before || c.AccessAllowed("example/repo") {
					t.Fatal("credential rejection did not fence source access")
				}
				return
			}
			if c.Generation() != before || !c.AccessAllowed("example/repo") {
				t.Fatal("optional search permission revoked Contents access")
			}
			if _, err := c.ReadFile(context.Background(), "example/repo", testSHA, "main.go"); err != nil {
				t.Fatalf("authorized pinned Contents read failed: %v", err)
			}
			contentsDenied.Store(true)
			if _, err := c.ReadFile(context.Background(), "example/repo", testSHA, "main.go"); ErrorCode(err) != "permission_required" {
				t.Fatalf("Contents denial was ignored: %v", err)
			}
			if c.Generation() == before || c.AccessAllowed("example/repo") {
				t.Fatal("real Contents denial did not fence access")
			}
		})
	}
}

func TestGithubDiscoveryScopedHintsAndPRHead(t *testing.T) {
	searches := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/code":
			searches++
			if r.URL.Query().Get("q") != `repo:example/repo "/healthz"` {
				t.Errorf("unbounded search scope %q", r.URL.Query().Get("q"))
			}
			w.Header().Set("Link", `<https://attacker.invalid/next>; rel="next"`)
			fmt.Fprint(w, `{"total_count":200,"items":[{"path":"internal/opaque.go","repository":{"full_name":"example/repo"}},{"path":"../secret","repository":{"full_name":"example/repo"}},{"path":"foreign.go","repository":{"full_name":"other/repo"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7/files"):
			fmt.Fprint(w, `[{"filename":"internal/opaque.go"},{"filename":"../secret"}]`)
		case strings.HasSuffix(r.URL.Path, "/pulls/7"):
			prReply(w, testSHA)
		default:
			http.NotFound(w, r)
		}
	})
	d, ok := any(c).(pathDiscovery)
	if !ok {
		t.Fatal("provider lacks bounded discovery capability")
	}
	paths, partial, err := d.FindPaths(context.Background(), "example/repo", "/healthz")
	if err != nil || !partial || len(paths) != 1 || paths[0] != "internal/opaque.go" || searches != 1 {
		t.Fatalf("hints=%v partial=%v err=%v requests=%d", paths, partial, err, searches)
	}
	for _, bad := range []string{`/healthz" repo:other/repo`, "a\nb", "", strings.Repeat("a", 201)} {
		if _, _, err = d.FindPaths(context.Background(), "example/repo", bad); err == nil {
			t.Errorf("accepted search expression %q", bad)
		}
	}
	if _, _, err = d.FindPaths(context.Background(), "other/repo", "/healthz"); ErrorCode(err) != "repository_not_allowed" {
		t.Errorf("scope escape %v", err)
	}
	paths, _, err = d.PullRequestPaths(context.Background(), "example/repo", 7, testSHA)
	if err != nil || len(paths) != 1 || paths[0] != "internal/opaque.go" {
		t.Fatalf("PR paths %v %v", paths, err)
	}
	if _, _, err = d.PullRequestPaths(context.Background(), "example/repo", 7, strings.Repeat("c", 40)); ErrorCode(err) != "head_changed" {
		t.Fatalf("unbound PR paths %v", err)
	}
}
