package githubprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Distinct synthetic secrets so a leak of any one can be traced.
const (
	sourceTokenA     = "SYNTHETIC_SOURCE_TOKEN_A_NEVER_DISCLOSE"
	sourceTokenB     = "SYNTHETIC_SOURCE_TOKEN_B_NEVER_DISCLOSE"
	sourceFileToken  = "SYNTHETIC_SOURCE_FILE_TOKEN_NEVER_DISCLOSE"
	sourceErrorText  = "SYNTHETIC_SOURCE_ERROR_TEXT_NEVER_DISCLOSE"
	sourceIdentity1  = "connection-1"
	sourceIdentity2  = "connection-2"
	repoMetadataJSON = `{"id":11,"default_branch":"main"}`
)

type fakeSource struct {
	mu         sync.Mutex
	token      string
	identity   string
	err        error
	currents   atomic.Int32
	freshes    atomic.Int32
	rejected   []string
	onFresh    func()
	onCurrent  func()
	onRejected func(string)
}

func (s *fakeSource) set(token, identity string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token, s.identity, s.err = token, identity, err
}

func (s *fakeSource) read() (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, s.identity, s.err
}

func (s *fakeSource) Current() (string, string, error) {
	s.currents.Add(1)
	if s.onCurrent != nil {
		s.onCurrent()
	}
	return s.read()
}

func (s *fakeSource) Fresh(ctx context.Context) (string, string, error) {
	s.freshes.Add(1)
	if s.onFresh != nil {
		s.onFresh()
	}
	return s.read()
}

func (s *fakeSource) Rejected(token string) {
	s.mu.Lock()
	s.rejected = append(s.rejected, token)
	hook := s.onRejected
	s.mu.Unlock()
	if hook != nil {
		hook(token)
	}
}

func (s *fakeSource) rejections() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.rejected...)
}

func sourceFixture(t *testing.T, src CredentialSource, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return New(Options{Enabled: true, Source: src, Repositories: []string{"example/repo"}, HTTPClient: s.Client(), Endpoint: s.URL})
}

func assertNoSecret(t *testing.T, values ...any) {
	t.Helper()
	for _, v := range values {
		b, _ := json.Marshal(v)
		text := fmt.Sprint(v) + string(b)
		for _, secret := range []string{sourceTokenA, sourceTokenB, sourceFileToken, sourceErrorText} {
			if strings.Contains(text, secret) {
				t.Fatalf("secret leaked: %s", text)
			}
		}
	}
}

func TestSourceRefreshKeepsGenerationAndIdentityChangeBumpsIt(t *testing.T) {
	var auth sync.Map
	var requests atomic.Int32
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		auth.Store(r.Header.Get("Authorization"), true)
		w.Header().Set("ETag", `"synthetic"`)
		fmt.Fprint(w, repoMetadataJSON)
	})
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	before := c.Generation()
	src.set(sourceTokenB, sourceIdentity1, nil) // refresh: new token, same grant
	if c.Generation() != before {
		t.Fatal("a refresh with a stable identity bumped the generation")
	}
	c.mu.Lock()
	cached := len(c.cache)
	c.mu.Unlock()
	if cached != 1 {
		t.Fatal("a refresh purged the cache")
	}
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.Load("Bearer " + sourceTokenB); !ok {
		t.Fatal("refreshed token was not used")
	}
	if c.Generation() != before {
		t.Fatal("fetching with a refreshed token bumped the generation")
	}
	src.set(sourceTokenB, sourceIdentity2, nil) // reconnect: new grant
	if c.Generation() == before {
		t.Fatal("an identity change kept the generation")
	}
	c.mu.Lock()
	cached = len(c.cache)
	c.mu.Unlock()
	if cached != 0 {
		t.Fatal("an identity change kept the cache")
	}
	connected := c.Generation()
	src.set("", "", providerError("not_configured")) // disconnect
	if c.Generation() == connected || c.AccessAllowed("example/repo") {
		t.Fatal("disconnect kept the generation or access")
	}
}

func TestSourceLocalReadsUseCurrentOnly(t *testing.T) {
	var requests atomic.Int32
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, repoMetadataJSON)
	})
	c.Generation()
	c.AccessAllowed("example/repo")
	c.CachedDiagnostics()
	c.AllowedRepositories()
	if src.freshes.Load() != 0 || src.currents.Load() == 0 || requests.Load() != 0 {
		t.Fatalf("local reads: fresh=%d current=%d requests=%d", src.freshes.Load(), src.currents.Load(), requests.Load())
	}
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	if src.freshes.Load() != 1 {
		t.Fatalf("fetch called Fresh %d times", src.freshes.Load())
	}
}

func TestSourceInvalidTokenNeverSent(t *testing.T) {
	for name, token := range map[string]string{
		"empty":      "",
		"whitespace": "   ",
		"padded":     " " + sourceTokenA + " ",
		"newline":    sourceTokenA + "\n",
		"inner":      "SYNTHETIC SPLIT",
		"nul":        "SYNTHETIC\x00NUL",
		"control":    "SYNTHETIC\x01CTRL",
		"delete":     "SYNTHETIC\x7fDEL",
		"nbsp":       "SYNTHETIC\u00a0NBSP",
		"oversized":  strings.Repeat("S", 4097),
	} {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			src := &fakeSource{token: token, identity: sourceIdentity1}
			c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
			_, err := c.Resolve(context.Background(), "example/repo", "main")
			if ErrorCode(err) != "unavailable" {
				t.Fatalf("got %v", err)
			}
			d := c.CachedDiagnostics()
			if d.Configured || d.State != "unavailable" || c.AccessAllowed("example/repo") {
				t.Fatalf("%+v", d)
			}
			if requests.Load() != 0 {
				t.Fatal("invalid token was sent")
			}
			assertNoSecret(t, err, d)
		})
	}
	var requests atomic.Int32
	src := &fakeSource{token: sourceTokenA, identity: ""}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
	if _, err := c.Resolve(context.Background(), "example/repo", "main"); ErrorCode(err) != "unavailable" || requests.Load() != 0 {
		t.Fatalf("empty identity accepted: %v", err)
	}
}

func TestSourceErrorsAreSanitized(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{providerError("not_configured"), "not_configured"},
		{providerError("unavailable"), "unavailable"},
		{providerError("reconnect_required"), "reconnect_required"},
		{providerError("permission_required"), "unavailable"},
		{&Error{Code: sourceErrorText}, "unavailable"},
		{errors.New(sourceErrorText), "unavailable"},
	}
	for _, tc := range cases {
		var requests atomic.Int32
		src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1, err: tc.err}
		c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
		_, err := c.Resolve(context.Background(), "example/repo", "main")
		if ErrorCode(err) != tc.code {
			t.Fatalf("%v: got %v", tc.err, err)
		}
		cached := c.CachedDiagnostics()
		checked := c.Diagnostics(context.Background())
		for _, d := range []Diagnostics{cached, checked} {
			if d.Configured || d.State != tc.code || d.Message != (&Error{Code: tc.code}).Error() {
				t.Fatalf("%v: %+v", tc.err, d)
			}
		}
		if requests.Load() != 0 {
			t.Fatal("token sent despite source error")
		}
		assertNoSecret(t, err, cached, checked)
	}
	if got := (&Error{Code: "reconnect_required"}).Error(); got != "GitHub sign-in expired; reconnect GitHub." {
		t.Fatal(got)
	}
}

func TestSourceRejectedOncePer401(t *testing.T) {
	var status atomic.Int32
	status.Store(401)
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) })
	// The hook calls back into the client: it must not run under the lock.
	src.onRejected = func(string) { c.SetRepositories(c.AllowedRepositories()); _ = c.Generation() }
	if _, err := c.FetchPullRequest(context.Background(), "example/repo", 7); ErrorCode(err) != "permission_required" {
		t.Fatal(err)
	}
	if got := src.rejections(); len(got) != 1 || got[0] != sourceTokenA {
		t.Fatalf("rejections after one 401: %d", len(got))
	}
	if c.AccessAllowed("example/repo") {
		t.Fatal("401 did not take the existing deny path")
	}
	if _, err := c.Resolve(context.Background(), "example/repo", "main"); ErrorCode(err) != "permission_required" {
		t.Fatal(err)
	}
	if got := src.rejections(); len(got) != 1 {
		t.Fatalf("same token rejected %d times", len(got))
	}
	src.set(sourceTokenB, sourceIdentity1, nil)
	_, _ = c.Resolve(context.Background(), "example/repo", "main")
	if got := src.rejections(); len(got) != 2 || got[1] != sourceTokenB {
		t.Fatalf("new token rejection not reported: %d", len(got))
	}
	for _, code := range []int32{403, 404} {
		status.Store(code)
		src.set(fmt.Sprintf("SYNTHETIC_TOKEN_FOR_%d", code), sourceIdentity1, nil)
		_, _ = c.Resolve(context.Background(), "example/repo", "main")
	}
	if got := src.rejections(); len(got) != 2 {
		t.Fatalf("403/404 reported as rejection: %d", len(got))
	}
}

func TestSourceReentrantCallsDoNotDeadlock(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, repoMetadataJSON) })
	src.onCurrent = func() { _ = c.AllowedRepositories() }
	src.onFresh = func() { c.SetRepositories([]string{"example/repo"}); _ = c.AllowedRepositories() }
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	_ = c.Generation()
	_ = c.CachedDiagnostics()
	_ = c.Diagnostics(context.Background())
}

func TestSourceTakesPrecedenceOverTokenFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(sourceFileToken), 0600); err != nil {
		t.Fatal(err)
	}
	var seen atomic.Value
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		fmt.Fprint(w, repoMetadataJSON)
	}))
	t.Cleanup(s.Close)
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := New(Options{Enabled: true, TokenFile: file, Source: src, Repositories: []string{"example/repo"}, HTTPClient: s.Client(), Endpoint: s.URL})
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != "Bearer "+sourceTokenA {
		t.Fatal("source did not take precedence over the token file")
	}
}

func TestReadFileRefusesTokenRefreshedMidRead(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	content := "prefix " + sourceTokenA + " suffix"
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			fmt.Fprintf(w, `{"tree":[{"path":"README.md","mode":"100644","type":"blob","sha":%q,"size":%d}]}`, testSHA, len(content))
		case strings.Contains(r.URL.Path, "/contents/"):
			src.set(sourceTokenB, sourceIdentity1, nil) // refreshed while the read is in flight
			fmt.Fprintf(w, `{"type":"file","sha":%q,"encoding":"base64","content":%q,"size":%d}`, testSHA, base64.StdEncoding.EncodeToString([]byte(content)), len(content))
		default:
			http.NotFound(w, r)
		}
	})
	f, err := c.ReadFile(context.Background(), "example/repo", testSHA, "README.md")
	if ErrorCode(err) != "file_unavailable" {
		t.Fatalf("file holding the fetch token was returned: %v", err)
	}
	assertNoSecret(t, f, err)
}
