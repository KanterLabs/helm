package githubconnect

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

// Failure list: failures.md, "Found by the independent review (WP7)".

func TestFailedRefreshBacksOff(t *testing.T) {
	e := newEnv(t)
	e.connect()
	old, _, _ := e.m.Current()
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = []string{"500", "500", "500", "500"} })
	e.due()
	for i := 0; i < 50; i++ {
		if token, _, err := e.fresh(); err != nil || token != old {
			t.Fatalf("read %d with a still-valid token: %v", i, err)
		}
	}
	if got := e.refreshes(); got != 1 {
		t.Fatalf("50 reads during a failing refresh made %d exchanges, want 1", got)
	}
	e.clock.advance(refreshBackoff + time.Second)
	e.fresh()
	e.fresh()
	if got := e.refreshes(); got != 2 {
		t.Fatalf("after the backoff: %d exchanges, want 2", got)
	}
	// A refused token must not defeat the backoff either.
	e.m.Rejected(old)
	for i := 0; i < 50; i++ {
		if _, _, err := e.fresh(); githubprovider.ErrorCode(err) != "unavailable" {
			t.Fatalf("refused token during backoff: %v", err)
		}
	}
	if got := e.refreshes(); got != 2 {
		t.Fatalf("a refused token during backoff made %d exchanges, want 2", got)
	}
	// Once GitHub answers again, one exchange renews and clears the backoff.
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = nil })
	e.clock.advance(refreshBackoff + time.Second)
	if token, _, err := e.fresh(); err != nil || token == old {
		t.Fatalf("recovery: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("state after recovery: %q", s.State)
	}
}

func TestRejectionAfterRefreshRetriesBeforeEnding(t *testing.T) {
	e := newEnv(t)
	e.connect()
	first, id, _ := e.m.Current()
	e.m.Rejected(first)
	second, _, err := e.fresh()
	if err != nil || second == first {
		t.Fatalf("first refresh: %v", err)
	}
	// Refused at once: not yet the end. Refresh once more.
	e.m.Rejected(second)
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("one 401 after a refresh ended the sign-in: %q", s.State)
	}
	third, id3, err := e.fresh()
	if err != nil || third == second || id3 != id || e.refreshes() != 2 {
		t.Fatalf("second refresh: %v, %d exchanges", err, e.refreshes())
	}
	// A token that then works for a while resets the count.
	e.clock.advance(2 * rejectWindow)
	e.m.Rejected(third)
	fourth, _, err := e.fresh()
	if err != nil || fourth == third {
		t.Fatalf("refresh after a late rejection: %v", err)
	}
	e.m.Rejected(fourth)
	fifth, _, err := e.fresh()
	if err != nil || fifth == fourth {
		t.Fatalf("retry after the first early rejection: %v", err)
	}
	// Two in a row: the authorization is gone.
	e.m.Rejected(fifth)
	if _, _, err := e.fresh(); githubprovider.ErrorCode(err) != "reconnect_required" {
		t.Fatalf("second 401 in a row after a refresh: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateReconnectRequired {
		t.Fatalf("state: %q", s.State)
	}
}

func TestClockExpiryNotifiesListeners(t *testing.T) {
	e := newEnv(t)
	e.connect()
	var mu sync.Mutex
	var seen [][]string
	e.m.OnRepositories(func(list []string) {
		mu.Lock()
		seen = append(seen, slices.Clone(list))
		mu.Unlock()
	})
	last := func() ([]string, int) {
		mu.Lock()
		defer mu.Unlock()
		return seen[len(seen)-1], len(seen)
	}
	if list, _ := last(); len(list) == 0 {
		t.Fatal("no repositories while connected")
	}
	e.clock.advance(200 * 24 * time.Hour) // past the refresh token's lifetime
	if _, _, err := e.m.Current(); githubprovider.ErrorCode(err) != "reconnect_required" {
		t.Fatalf("expired sign-in: %v", err)
	}
	list, calls := last()
	if len(list) != 0 {
		t.Fatalf("listeners still hold %v after the sign-in ended by the clock", list)
	}
	e.m.Current()
	e.fresh()
	if _, again := last(); again != calls {
		t.Fatalf("the ended state was announced %d times", again-calls+1)
	}
}

func TestCancelByAnotherAdministratorIsIgnored(t *testing.T) {
	e := newEnv(t)
	e.start()
	begun := e.begin(testActor)
	if begun.Attempt == nil || begun.Attempt.ID == "" {
		t.Fatal("no attempt")
	}
	if s := e.m.Cancel(otherActor, begun.Attempt.ID); s.Attempt == nil || s.Attempt.State != AttemptPending || s.Attempt.UserCode != "" {
		t.Fatalf("another administrator cancelled the attempt, or saw its code: %+v", s.Attempt)
	}
	if s := e.m.Status(testActor); s.Attempt == nil || s.Attempt.UserCode == "" {
		t.Fatal("the beginner lost the attempt")
	}
	if s := e.m.Cancel(testActor, begun.Attempt.ID); s.Attempt != nil {
		t.Fatalf("the beginner could not cancel: %+v", s.Attempt)
	}
}

func TestFreshAnswersForTheConnectionHeldNow(t *testing.T) {
	e := newEnv(t)
	e.connect()
	_, oldID, _ := e.m.Current()
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.refreshGate = gate })
	e.due()
	type answer struct {
		token, id string
		err       error
	}
	got := make(chan answer, 1)
	go func() {
		token, id, err := e.m.Fresh(e.ctx())
		got <- answer{token, id, err}
	}()
	waitFor(t, "refresh in flight", func() bool { return e.refreshes() == 1 })
	// Disconnect and sign in again while the old exchange waits on GitHub.
	if err := e.m.Disconnect(testActor); err != nil {
		t.Fatal(err)
	}
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.begin(testActor)
	e.step()
	waitFor(t, "reconnected", func() bool { return e.m.Status(testActor).State == StateConnected })
	newToken, newID, _ := e.m.Current()
	if newID == oldID {
		t.Fatal("a new sign-in kept the old identity")
	}
	close(gate)
	e.gh.set(func(g *fakeGitHub) { g.refreshGate = nil })
	a := <-got
	if a.err != nil || a.id != newID || a.token != newToken {
		t.Fatalf("Fresh across a reconnect answered identity %q (new is %q), err %v", a.id, newID, a.err)
	}
	if token, id, err := e.m.Current(); err != nil || token != newToken || id != newID {
		t.Fatalf("the late exchange changed the new connection: %v", err)
	}
}

func TestConcurrentSyncsShareOneRun(t *testing.T) {
	e := newEnv(t)
	e.connect()
	before := e.gh.count(func(g *fakeGitHub) int { return g.apiCalls })
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.apiGate = gate })
	const callers = 12
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() { errs <- e.m.Sync(e.ctx()) }()
	}
	waitFor(t, "a sync in flight", func() bool { return e.gh.count(func(g *fakeGitHub) int { return g.apiCalls }) > before })
	time.Sleep(50 * time.Millisecond) // let the other callers arrive behind it
	one := e.gh.count(func(g *fakeGitHub) int { return g.apiCalls }) - before
	close(gate)
	e.gh.set(func(g *fakeGitHub) { g.apiGate = nil })
	for i := 0; i < callers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("sync: %v", err)
		}
	}
	if one != 1 {
		t.Fatalf("%d GitHub calls were in flight for %d concurrent syncs, want 1", one, callers)
	}
	shared := e.gh.count(func(g *fakeGitHub) int { return g.apiCalls }) - before
	if err := e.m.Sync(e.ctx()); err != nil {
		t.Fatal(err)
	}
	alone := e.gh.count(func(g *fakeGitHub) int { return g.apiCalls }) - before - shared
	if shared > 2*alone {
		t.Fatalf("%d concurrent syncs made %d GitHub calls; one sync makes %d", callers, shared, alone)
	}
}

func TestBeginBudget(t *testing.T) {
	e := newEnv(t)
	e.start()
	for i := 0; i < maxBeginsPerMinute; i++ {
		s, err := e.m.Begin(e.ctx(), testActor)
		if err != nil || s.Attempt == nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		e.m.Cancel(testActor, s.Attempt.ID)
	}
	calls := e.gh.count(func(g *fakeGitHub) int { return g.deviceCalls })
	if _, err := e.m.Begin(e.ctx(), testActor); Code(err) != CodeTooManyAttempts {
		t.Fatalf("attempt beyond the budget: %v", err)
	}
	if _, err := e.m.Begin(e.ctx(), otherActor); Code(err) != CodeTooManyAttempts {
		t.Fatalf("another administrator beyond the budget: %v", err)
	}
	if got := e.gh.count(func(g *fakeGitHub) int { return g.deviceCalls }); got != calls {
		t.Fatalf("a refused attempt still asked GitHub (%d calls, was %d)", got, calls)
	}
	e.clock.advance(time.Minute + time.Second)
	s, err := e.m.Begin(e.ctx(), testActor)
	if err != nil || s.Attempt == nil {
		t.Fatalf("after a minute: %v", err)
	}
	e.m.Cancel(testActor, s.Attempt.ID)
	if maxBeginsPerMinute < 12 {
		t.Fatalf("a cap of %d a minute is within reach of people connecting", maxBeginsPerMinute)
	}
	// A clock that steps backwards leaves requests stamped in the future.
	// They must not count against the cap until the clock catches up.
	for i := 1; i < maxBeginsPerMinute; i++ {
		s, err := e.m.Begin(e.ctx(), testActor)
		if err != nil {
			t.Fatalf("refill %d: %v", i, err)
		}
		e.m.Cancel(testActor, s.Attempt.ID)
	}
	e.clock.advance(-2 * time.Hour)
	if s, err := e.m.Begin(e.ctx(), testActor); err != nil || s.Attempt == nil {
		t.Fatalf("after the clock stepped back: %v", err)
	}
}
