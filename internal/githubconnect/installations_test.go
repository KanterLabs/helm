package githubconnect

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func names(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s/Repo%03d", prefix, i)
	}
	return out
}

func TestSyncPaginationAndTruncation(t *testing.T) {
	many := make([]fakeInstallation, 25)
	for i := range many {
		many[i] = fakeInstallation{id: int64(100 + i), repos: []string{fmt.Sprintf("Org%02d/only", i)}}
	}
	cases := []struct {
		name      string
		installs  []fakeInstallation
		count     int
		truncated bool
		calls     int // API calls made by one Sync
	}{
		{"two pages", []fakeInstallation{{id: 1, repos: names("Big", 120)}}, 50, true, 3},
		{"page cap", []fakeInstallation{{id: 1, repos: names("Huge", 350)}}, 50, true, 4},
		{"installation cap", many, 20, true, 21},
		{"small", []fakeInstallation{{id: 1, repos: names("A", 10)}, {id: 2, repos: names("B", 10)}}, 20, false, 3},
		{"exact page", []fakeInstallation{{id: 1, repos: names("C", 100)}}, 50, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.connect()
			e.gh.set(func(g *fakeGitHub) { g.installations, g.apiCalls = c.installs, 0 })
			must(t, e.err(e.m.Sync(e.ctx())))
			repos := e.m.Repositories()
			if len(repos) != c.count || !slices.IsSorted(repos) {
				t.Fatalf("%d repositories (sorted=%v)", len(repos), slices.IsSorted(repos))
			}
			if got := e.m.Status(testActor).RepositoriesTruncated; got != c.truncated {
				t.Fatalf("truncated = %v", got)
			}
			if calls := e.gh.count(func(g *fakeGitHub) int { return g.apiCalls }); calls != c.calls {
				t.Fatalf("%d API calls, want %d", calls, c.calls)
			}
		})
	}
}

func TestSyncNormalizesAndAppliesCeiling(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Ceiling = []string{"KanterLabs/Helm", "kanterlabs/absent", "not a repo"} })
	e.gh.set(func(g *fakeGitHub) {
		g.installations = []fakeInstallation{
			{id: 1, repos: []string{"KanterLabs/Helm", "Other/Thing", "../escape"}},
			{id: 2, repos: []string{"kanterlabs/HELM"}},
		}
	})
	e.connect()
	if got := e.m.Repositories(); !slices.Equal(got, []string{"kanterlabs/helm"}) {
		t.Fatalf("repositories %v", got)
	}
	empty := newEnv(t, func(o *Options) { o.Ceiling = []string{"elsewhere/repo"} })
	empty.start()
	empty.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	empty.begin(testActor)
	empty.step()
	waitFor(t, "connected", func() bool { return empty.m.Status(testActor).Login != "" })
	if s := empty.m.Status(testActor); s.State != StateNoRepositories || len(empty.m.Repositories()) != 0 {
		t.Fatalf("empty intersection: %q %v", s.State, empty.m.Repositories())
	}
}

func TestNoInstallationsIsNoRepositories(t *testing.T) {
	e := newEnv(t)
	e.gh.set(func(g *fakeGitHub) { g.installations = nil })
	e.start()
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.begin(testActor)
	e.step()
	waitFor(t, "signed in", func() bool { return e.m.Status(testActor).Login != "" })
	if s := e.m.Status(testActor); s.State != StateNoRepositories {
		t.Fatalf("state %q", s.State)
	}
	e.gh.set(func(g *fakeGitHub) { g.installations = []fakeInstallation{{id: 3, repos: []string{"a/b"}}} })
	must(t, e.err(e.m.Sync(e.ctx())))
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("after install: %q", s.State)
	}
}

func TestRepositoriesFollowLoadSyncDisconnect(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.restart()
	var mu sync.Mutex
	var seen []string
	e.m.OnRepositories(func(list []string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, strings.Join(list, ","))
	})
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 {
			return "<none>"
		}
		return seen[len(seen)-1]
	}
	if last() != "kanterlabs/docs,kanterlabs/helm" {
		t.Fatalf("load: %q", last())
	}
	e.gh.set(func(g *fakeGitHub) { g.installations[0].repos = []string{"KanterLabs/docs"} })
	must(t, e.err(e.m.Sync(e.ctx())))
	if last() != "kanterlabs/docs" {
		t.Fatalf("sync: %q", last())
	}
	must(t, e.err(e.m.Disconnect(testActor)))
	if last() != "" {
		t.Fatalf("disconnect: %q", last())
	}
}

func TestSyncRetriesAfterRejectedToken(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.gh.set(func(g *fakeGitHub) { g.reject401 = 1 })
	must(t, e.err(e.m.Sync(e.ctx())))
	if e.refreshes() != 1 {
		t.Fatalf("%d refreshes", e.refreshes())
	}
	e.gh.set(func(g *fakeGitHub) { g.reject401 = 10 })
	wantCode(t, e.err(e.m.Sync(e.ctx())), CodeReconnectRequired)
}

func TestSyncErrors(t *testing.T) {
	e := newEnv(t)
	wantCode(t, e.err(e.m.Sync(e.ctx())), CodeNotConnected)
	e.connect()
	e.gh.Close()
	wantCode(t, e.err(e.m.Sync(e.ctx())), CodeGitHubUnreachable)
	if s := e.m.Status(testActor); s.State != StateConnected || len(e.m.Repositories()) != 2 {
		t.Fatalf("an unreachable GitHub changed the connection: %q", s.State)
	}
}
