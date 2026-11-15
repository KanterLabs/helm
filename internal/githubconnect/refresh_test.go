package githubconnect

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

var _ githubprovider.CredentialSource = (*Manager)(nil)
var _ githubprovider.CredentialRejecter = (*Manager)(nil)

// due moves the clock into the 10-minute refresh window of an 8-hour token.
func (e *env) due() { e.clock.advance(8*time.Hour - 5*time.Minute) }

func (e *env) stored() map[string]any {
	e.t.Helper()
	fields := map[string]any{}
	if err := json.Unmarshal(e.read(), &fields); err != nil {
		e.t.Fatal(err)
	}
	return fields
}

func (e *env) fresh() (string, string, error) {
	token, identity, err := e.m.Fresh(e.ctx())
	return token, identity, e.err(err)
}

func (e *env) refreshes() int { return e.gh.count(func(g *fakeGitHub) int { return g.refreshCalls }) }

func TestRefreshKeepsIdentity(t *testing.T) {
	e := newEnv(t)
	e.connect()
	first, id1, _ := e.m.Current()
	if token, _, _ := e.fresh(); token != first || e.refreshes() != 0 {
		t.Fatal("a token far from expiry was refreshed")
	}
	e.due()
	second, id2, err := e.fresh()
	if err != nil || second == first || id2 != id1 {
		t.Fatalf("refresh: token changed=%v identity kept=%v err=%v", second != first, id1 == id2, err)
	}
	if e.stored()["access_token"] != second {
		t.Fatal("the new pair was not persisted")
	}
}

func TestConcurrentRefreshIsSingleFlight(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.due()
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.refreshGate = gate })
	var wg sync.WaitGroup
	tokens := make([]string, 8)
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i], _, _ = e.fresh()
		}()
	}
	waitFor(t, "a refresh", func() bool { return e.refreshes() == 1 })
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	for _, token := range tokens {
		if token == "" || token != tokens[0] {
			t.Fatalf("callers got different tokens")
		}
	}
	if e.refreshes() != 1 {
		t.Fatalf("%d refreshes", e.refreshes())
	}
}

func TestRefreshSurvivesCancelledCaller(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.due()
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.refreshGate = gate })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := e.m.Fresh(ctx)
		done <- e.err(err)
	}()
	waitFor(t, "a refresh", func() bool { return e.refreshes() == 1 })
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled caller got a token")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled caller kept waiting")
	}
	close(gate)
	waitFor(t, "the rotated pair on disk", func() bool {
		token, _, _ := e.m.Current()
		return e.stored()["access_token"] == token && strings.Contains(token, "002")
	})
	if _, _, err := e.fresh(); err != nil || e.refreshes() != 1 {
		t.Fatalf("after the abandoned refresh: %v, %d refreshes", err, e.refreshes())
	}
}

func TestRefreshStorageFailureKeepsNewPair(t *testing.T) {
	e := newEnv(t)
	e.connect()
	old := e.stored()["access_token"]
	e.due()
	e.m.store.beforeRename = func() error { return errors.New("disk full") }
	token, _, err := e.fresh()
	if err != nil || token == old {
		t.Fatalf("refresh with a failed write: %v", err)
	}
	if current, _, _ := e.m.Current(); current != token {
		t.Fatal("fell back to the dead pair")
	}
	if e.stored()["access_token"] != old {
		t.Fatal("file changed despite the failure")
	}
	if !strings.Contains(strings.Join(e.logLines(), "\n"), `"code":"storage_failed"`) {
		t.Fatalf("storage failure not reported: %v", e.logLines())
	}
	e.m.store.beforeRename = nil
	if again, _, err := e.fresh(); err != nil || again != token || e.refreshes() != 1 {
		t.Fatalf("retry: %v", err)
	}
	if e.stored()["access_token"] != token {
		t.Fatal("the write was not retried")
	}
	e.due()
	if _, _, err := e.fresh(); err != nil || e.refreshes() != 2 {
		t.Fatalf("the next refresh did not use the new pair: %v", err)
	}
}

func TestCrashAfterRefreshReconnects(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.due()
	e.m.store.beforeRename = func() error { return errors.New("power cut") }
	if _, _, err := e.fresh(); err != nil {
		t.Fatal(err)
	}
	e.restart() // the rotated pair was never written
	_, _, err := e.fresh()
	if githubprovider.ErrorCode(err) != "reconnect_required" {
		t.Fatalf("dead pair: %v", err)
	}
	e.restart()
	if s := e.m.Status(testActor); s.State != StateReconnectRequired {
		t.Fatalf("after restart: %q", s.State)
	}
	if strings.Contains(string(e.read()), accessSentinel) {
		t.Fatal("ended connection kept its tokens")
	}
	e.gh.set(func(g *fakeGitHub) { g.pollScript = []string{"success"} })
	e.start()
	e.begin(testActor)
	e.step()
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("reconnect: %q", s.State)
	}
}

func TestRefreshWithoutRefreshTokenKeepsOldOne(t *testing.T) {
	e := newEnv(t)
	e.connect()
	refresh := e.stored()["refresh_token"]
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = []string{"no_refresh"} })
	e.due()
	if _, _, err := e.fresh(); err != nil {
		t.Fatal(err)
	}
	if got := e.stored()["refresh_token"]; got != refresh {
		t.Fatal("refresh token replaced or wiped")
	}
	e.due()
	if _, _, err := e.fresh(); err != nil || e.refreshes() != 2 {
		t.Fatalf("second refresh: %v", err)
	}
}

func TestExpiredRefreshTokenIsReconnectRequired(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = []string{"bad_refresh_token"} })
	e.due()
	for range 3 {
		_, _, err := e.fresh()
		if githubprovider.ErrorCode(err) != "reconnect_required" {
			t.Fatalf("bad refresh token: %v", err)
		}
	}
	if e.refreshes() != 1 {
		t.Fatalf("refresh retried %d times", e.refreshes())
	}
	if len(e.m.Repositories()) != 0 {
		t.Fatal("allowlist kept after the sign-in ended")
	}

	local := newEnv(t)
	local.connect()
	local.clock.advance(185 * 24 * time.Hour)
	_, _, err := local.fresh()
	if githubprovider.ErrorCode(err) != "reconnect_required" || local.refreshes() != 0 {
		t.Fatalf("lapsed refresh token: %v, %d calls", err, local.refreshes())
	}
}

func TestRefreshErrorIn200(t *testing.T) {
	e := newEnv(t)
	e.connect()
	old, _, _ := e.m.Current()
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = []string{"refused"} })
	e.due()
	if token, _, err := e.fresh(); err != nil || token != old {
		t.Fatalf("refused refresh with a valid token: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("state %q", s.State)
	}
}

func TestRefreshNetworkFailureKeepsValidToken(t *testing.T) {
	e := newEnv(t)
	e.connect()
	old, _, _ := e.m.Current()
	e.gh.set(func(g *fakeGitHub) { g.refreshScript = []string{"500", "500"} })
	e.due()
	if token, _, err := e.fresh(); err != nil || token != old {
		t.Fatalf("network failure with a valid token: %v", err)
	}
	e.clock.advance(10 * time.Minute)
	_, _, err := e.fresh()
	if githubprovider.ErrorCode(err) != "unavailable" {
		t.Fatalf("expired token after a failed refresh: %v", err)
	}
	if s := e.m.Status(testActor); s.State != StateConnected {
		t.Fatalf("a network failure ended the connection: %q", s.State)
	}
}

func TestRejectedForcesOneRefresh(t *testing.T) {
	e := newEnv(t)
	e.connect()
	first, _, _ := e.m.Current()
	e.m.Rejected("ghu_not_ours")
	if token, _, _ := e.fresh(); token != first || e.refreshes() != 0 {
		t.Fatal("a stale rejection caused a refresh")
	}
	e.m.Rejected(first)
	second, _, err := e.fresh()
	if err != nil || second == first || e.refreshes() != 1 {
		t.Fatalf("rejection: %v, %d refreshes", err, e.refreshes())
	}
	e.m.Rejected(first)
	if token, _, _ := e.fresh(); token != second || e.refreshes() != 1 {
		t.Fatal("a rejection of the old token caused another refresh")
	}
	e.m.Rejected(second) // a token fresh from a refresh, refused at once: renew once more
	third, _, err := e.fresh()
	if err != nil || third == second || e.refreshes() != 2 {
		t.Fatalf("refused fresh token: %v, %d refreshes", err, e.refreshes())
	}
	e.m.Rejected(third) // and again: the authorization is gone
	_, _, err = e.fresh()
	if githubprovider.ErrorCode(err) != "reconnect_required" {
		t.Fatalf("second refused fresh token: %v", err)
	}
}

func TestAppWithoutExpiryConnectsAndNeverRefreshes(t *testing.T) {
	e := newEnv(t)
	e.gh.set(func(g *fakeGitHub) { g.noExpiry = true })
	e.connect()
	token, _, _ := e.m.Current()
	e.clock.advance(400 * 24 * time.Hour)
	if got, _, err := e.fresh(); err != nil || got != token || e.refreshes() != 0 {
		t.Fatalf("non-expiring token: %v, %d refreshes", err, e.refreshes())
	}
	e.m.Rejected(token)
	_, _, err := e.fresh()
	if githubprovider.ErrorCode(err) != "reconnect_required" {
		t.Fatalf("revoked non-expiring token: %v", err)
	}
}

func TestDisconnectDuringRefreshDoesNotResurrect(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.due()
	gate := make(chan struct{})
	e.gh.set(func(g *fakeGitHub) { g.refreshGate = gate })
	done := make(chan error, 1)
	go func() {
		_, _, err := e.m.Fresh(context.Background())
		done <- e.err(err)
	}()
	waitFor(t, "a refresh", func() bool { return e.refreshes() == 1 })
	must(t, e.err(e.m.Disconnect(testActor)))
	close(gate)
	if err := <-done; err == nil {
		t.Fatal("refresh returned a token after disconnect")
	}
	if s := e.m.Status(testActor); s.State != StateDisconnected {
		t.Fatalf("state %q", s.State)
	}
	if _, err := e.m.store.load(); err {
		t.Fatal("refresh wrote a broken file")
	}
	if rec, _ := e.m.store.load(); rec != nil {
		t.Fatal("refresh resurrected the credential")
	}
}
