package githubprovider

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSetRepositoriesNormalizesDedupesSortsAndCaps(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { t.Error("allowlist changes contacted GitHub") })
	c.SetRepositories([]string{"Zeta/Repo", "zeta/repo", "alpha/repo", "../evil", "https://github.com/x/y", "", "bad"})
	if got := c.AllowedRepositories(); !reflect.DeepEqual(got, []string{"alpha/repo", "zeta/repo"}) {
		t.Fatalf("got %v", got)
	}
	var many []string
	for i := 59; i >= 0; i-- {
		many = append(many, fmt.Sprintf("org/repo-%02d", i))
	}
	c.SetRepositories(many)
	got := c.AllowedRepositories()
	if len(got) != 50 || got[0] != "org/repo-00" || got[49] != "org/repo-49" {
		t.Fatalf("cap did not keep the first 50 in sorted order: %d %v", len(got), got)
	}
	got[0] = "mutated/copy"
	if c.AllowedRepositories()[0] != "org/repo-00" {
		t.Fatal("AllowedRepositories exposed internal state")
	}
	reversed := append([]string{}, many...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	before := c.Generation()
	c.SetRepositories(reversed)
	if c.Generation() != before {
		t.Fatal("the same set in another order bumped the generation")
	}
	c.SetRepositories(nil)
	if d := c.CachedDiagnostics(); d.Configured || d.State != "not_configured" || len(d.AllowedRepositories) != 0 {
		t.Fatalf("empty allowlist still configured: %+v", d)
	}
}

func TestSetRepositoriesSwapDeniesAndPurges(t *testing.T) {
	var requests atomic.Int32
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, repoMetadataJSON)
	})
	var metadata map[string]any
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); err != nil {
		t.Fatal(err)
	}
	c.CachedDiagnostics()
	before := c.Generation()
	c.SetRepositories([]string{"example/repo"})
	if c.Generation() != before {
		t.Fatal("an unchanged set bumped the generation")
	}
	c.SetRepositories([]string{"other/repo"})
	if c.Generation() == before {
		t.Fatal("a changed set kept the generation")
	}
	c.mu.Lock()
	cached := len(c.cache)
	c.mu.Unlock()
	if cached != 0 {
		t.Fatal("a changed set kept cached data")
	}
	if c.AccessAllowed("example/repo") || !c.AccessAllowed("other/repo") {
		t.Fatal("swap did not move local access")
	}
	if _, err := c.Resolve(context.Background(), "example/repo", "main"); ErrorCode(err) != "repository_not_allowed" {
		t.Fatal(err)
	}
	// A caller that passed its scope check before the swap reaches fetch with
	// a repository that is no longer allowed: no token may be sent for it.
	sent := requests.Load()
	if _, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata); ErrorCode(err) != "repository_not_allowed" {
		t.Fatal(err)
	}
	if requests.Load() != sent {
		t.Fatal("token sent for a removed repository")
	}
	if d := c.CachedDiagnostics(); d.State != "unavailable" || !reflect.DeepEqual(d.AllowedRepositories, []string{"other/repo"}) {
		t.Fatalf("diagnostics kept the old set: %+v", d)
	}
}

func TestSetRepositoriesDuringInFlightFetch(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		w.Header().Set("ETag", `"synthetic"`)
		fmt.Fprint(w, repoMetadataJSON)
	})
	result := make(chan error, 1)
	go func() {
		var metadata map[string]any
		_, err := c.get(context.Background(), "example/repo", "/repos/example/repo", &metadata)
		result <- err
	}()
	<-arrived
	c.SetRepositories([]string{"other/repo"})
	close(release)
	if err := <-result; ErrorCode(err) != "unavailable" {
		t.Fatalf("in-flight fetch for a removed repository succeeded: %v", err)
	}
	c.mu.Lock()
	cached := len(c.cache)
	c.mu.Unlock()
	if cached != 0 {
		t.Fatal("in-flight fetch cached data for a removed repository")
	}
}

func TestSetRepositoriesConcurrentWithReads(t *testing.T) {
	src := &fakeSource{token: sourceTokenA, identity: sourceIdentity1}
	c := sourceFixture(t, src, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	ctx := context.Background()
	stop := make(chan struct{})
	writer := make(chan struct{})
	go func() {
		defer close(writer)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				c.SetRepositories([]string{"example/repo", "other/repo"})
			} else {
				c.SetRepositories([]string{"example/repo"})
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.AllowedRepositories()
				c.AccessAllowed("example/repo")
				c.CachedDiagnostics()
				c.Generation()
				_, _ = c.Resolve(ctx, "example/repo", "main")
				_ = c.Diagnostics(ctx)
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-writer
}

func TestOperatorModeIdentityIsTokenFile(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, repoMetadataJSON) })
	if _, ok := c.source.(fileSource); !ok {
		t.Fatalf("nil Options.Source did not select the token file: %T", c.source)
	}
	_, identity, err := c.source.Current()
	if err != nil || identity == "" || identity == sentinel {
		t.Fatal("file identity must be a non-secret fingerprint of the token")
	}
}
