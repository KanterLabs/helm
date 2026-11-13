package forgeconnect

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func (e *testEnv) stored() *record {
	e.t.Helper()
	rec, broken := newFileStore(e.dir).load()
	if broken {
		e.t.Fatal("state file is broken")
	}
	return rec
}

func TestRefreshErrorMapping(t *testing.T) {
	cases := map[string]func(*fakeForge){
		"invalid_grant (revoked)":           func(f *fakeForge) { f.revoked = true },
		"invalid_client (deleted)":          func(f *fakeForge) { f.appDeleted = true },
		"invalid_client (confidential)":     func(f *fakeForge) { f.confidential = true },
		"unauthorized_client (unparseable)": func(f *fakeForge) { f.refresh = map[string]bool{} },
		"unauthorized_client (reused)":      func(f *fakeForge) { f.oneTime = true; f.used[f.current[1]] = true },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.connect()
			before := e.m.Status()
			e.forge.set(change)
			e.clock.Advance(56 * time.Minute)
			_, _, err := e.m.Fresh(e.ctx())
			wantCode(t, e.err(err), CodeReconnectRequired)
			after := e.m.Status()
			if after.State != StateReconnectRequired || after.Server == nil || *after.Server != *before.Server ||
				after.ClientID != testClientID || after.Account == nil || *after.Account != *before.Account {
				t.Fatalf("reconnect_required status = %+v", after)
			}
			rec := e.stored()
			if rec.AccessToken != "" || rec.RefreshToken != "" || rec.ClientID != testClientID || rec.Account.Login != "helm-bot" {
				t.Fatalf("tokens not wiped or details lost: %+v", rec)
			}
			if restarted := e.open(); restarted.Status().State != StateReconnectRequired {
				t.Fatal("reconnect_required did not survive a restart")
			}
			if _, _, err := e.m.Current(); Code(err) != CodeReconnectRequired {
				t.Fatalf("Current = %v", err)
			}
		})
	}
	t.Run("malformed refresh response is transient", func(t *testing.T) {
		e := newEnv(t)
		e.connect()
		old, _, _ := e.m.Current()
		e.forge.set(func(f *fakeForge) { f.tokenOverride = `{"access_token":"","token_type":"bearer"}` })
		e.clock.Advance(56 * time.Minute)
		token, _, err := e.m.Fresh(e.ctx())
		if e.err(err) != nil || token != old || e.m.Status().State != StateConnected {
			t.Fatalf("malformed refresh: %v", err)
		}
	})
}

func TestRefreshNetworkFailure(t *testing.T) {
	e := newEnv(t)
	e.connect()
	old, _, _ := e.m.Current()
	e.forge.set(func(f *fakeForge) { f.tokenStatus = http.StatusBadGateway })
	e.clock.Advance(56 * time.Minute)
	if token, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil || token != old {
		t.Fatalf("502 during refresh must keep the token: %v", err)
	}
	e.forge.srv.Close()
	if token, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil || token != old {
		t.Fatalf("network failure during refresh must keep the token: %v", err)
	}
	e.clock.Advance(5 * time.Minute)
	_, _, err := e.m.Fresh(e.ctx())
	wantCode(t, e.err(err), CodeServerUnreachable)
	if e.m.Status().State != StateConnected || e.stored().RefreshToken == "" {
		t.Fatal("a network failure must not end the connection")
	}
}

func TestRefreshIdenticalBytes(t *testing.T) {
	e := newEnv(t)
	e.connect()
	token, identity, _ := e.m.Current()
	e.forge.set(func(f *fakeForge) { f.sameBytes = true })
	e.clock.Advance(56 * time.Minute)
	got, gotIdentity, err := e.m.Fresh(e.ctx())
	if e.err(err) != nil || got != token || gotIdentity != identity || e.forge.refreshes() != 1 {
		t.Fatalf("identical refresh = %v (refreshes %d)", err, e.forge.refreshes())
	}
	if rec := e.stored(); !rec.RefreshedAt.Equal(e.clock.Now()) || !rec.AccessExpiresAt.Equal(e.clock.Now().Add(time.Hour)) {
		t.Fatalf("identical refresh not recorded: %+v", rec.RefreshedAt)
	}
	e.forge.set(func(f *fakeForge) { f.sameBytes = false })
	e.clock.Advance(56 * time.Minute)
	got, gotIdentity, err = e.m.Fresh(e.ctx())
	if e.err(err) != nil || got == token || gotIdentity != identity {
		t.Fatal("identity must not change on refresh")
	}
}

func TestOneTimeRefreshMode(t *testing.T) {
	e := newEnv(t)
	e.forge.set(func(f *fakeForge) { f.oneTime = true })
	e.connect()
	for i := 0; i < 3; i++ {
		e.clock.Advance(56 * time.Minute)
		token, _, err := e.m.Fresh(e.ctx())
		e.forge.mu.Lock()
		current := e.forge.current
		e.forge.mu.Unlock()
		if e.err(err) != nil || token != current[0] || e.stored().RefreshToken != current[1] {
			t.Fatalf("round %d: %v", i, err)
		}
	}
	if e.forge.refreshes() != 3 {
		t.Fatalf("refreshes = %d", e.forge.refreshes())
	}
}

func TestConcurrentRefreshSingleFlight(t *testing.T) {
	e := newEnv(t)
	e.connect()
	gate, seen := make(chan struct{}), make(chan struct{}, 8)
	e.forge.set(func(f *fakeForge) { f.refreshGate, f.refreshSeen = gate, seen })
	e.clock.Advance(56 * time.Minute)
	var wg sync.WaitGroup
	tokens := make([]string, 5)
	errs := make([]error, 5)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], _, errs[i] = e.m.Fresh(e.ctx())
		}(i)
	}
	<-seen
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	e.forge.mu.Lock()
	current := e.forge.current[0]
	e.forge.mu.Unlock()
	for i := range tokens {
		if e.err(errs[i]) != nil || tokens[i] != current {
			t.Fatalf("caller %d got %v", i, errs[i])
		}
	}
	if e.forge.refreshes() != 1 {
		t.Fatalf("refreshes = %d, want 1", e.forge.refreshes())
	}
}

func TestRefreshPersistFailure(t *testing.T) {
	for _, oneTime := range []bool{false, true} {
		e := newEnv(t)
		e.forge.set(func(f *fakeForge) { f.oneTime = oneTime })
		e.connect()
		old := e.stored()
		e.m.store.beforeRename = func() error { return errors.New("disk full") }
		e.clock.Advance(56 * time.Minute)
		token, _, err := e.m.Fresh(e.ctx())
		if e.err(err) != nil || token != old.AccessToken {
			t.Fatalf("a refresh that cannot be saved must not be used: %q %v", token, err)
		}
		if got := e.stored(); got.RefreshToken != old.RefreshToken {
			t.Fatal("file changed")
		}
		// Restart, as after a crash between refresh and persist.
		restarted := e.open()
		restarted.logf = e.m.logf
		_, _, err = restarted.Fresh(e.ctx())
		if oneTime {
			wantCode(t, e.err(err), CodeReconnectRequired)
		} else if e.err(err) != nil {
			t.Fatalf("default mode accepts the previous refresh token: %v", err)
		}
	}
}

func TestAPI401RefreshAndRetry(t *testing.T) {
	e := newEnv(t)
	e.connect()
	e.forge.expireAccess()
	list, _, err := e.m.Repositories(e.ctx())
	if e.err(err) != nil || len(list) != 12 || e.forge.refreshes() != 1 {
		t.Fatalf("401 then refresh: %d %v (refreshes %d)", len(list), err, e.forge.refreshes())
	}
	e.clock.Advance(2 * time.Minute)
	e.forge.expireAccess()
	if _, _, err := e.m.Repositories(e.ctx()); e.err(err) != nil || e.forge.refreshes() != 2 {
		t.Fatalf("second 401 after a minute: %v", err)
	}
	e.clock.Advance(30 * time.Second)
	e.forge.expireAccess()
	_, _, err = e.m.Repositories(e.ctx())
	wantCode(t, e.err(err), CodeReconnectRequired)
	if e.forge.refreshes() != 2 || e.m.Status().State != StateReconnectRequired {
		t.Fatal("a refreshed token rejected within a minute must end the connection without another refresh")
	}
}

func TestRejected(t *testing.T) {
	e := newEnv(t)
	e.connect()
	token, _, _ := e.m.Current()
	e.m.Rejected("not-the-current-token")
	e.m.Rejected("")
	if fresh, _, err := e.m.Fresh(e.ctx()); e.err(err) != nil || fresh != token || e.forge.refreshes() != 0 {
		t.Fatal("a stale rejection must be ignored")
	}
	e.m.Rejected(token)
	fresh, _, err := e.m.Fresh(e.ctx())
	if e.err(err) != nil || fresh == token || e.forge.refreshes() != 1 {
		t.Fatalf("a rejected token must be refreshed: %v", err)
	}
	e.clock.Advance(20 * time.Second)
	e.m.Rejected(fresh)
	if e.m.Status().State != StateReconnectRequired || e.stored().AccessToken != "" {
		t.Fatal("a refreshed token rejected within a minute must end the connection")
	}
	_, _, err = e.m.Fresh(e.ctx())
	wantCode(t, e.err(err), CodeReconnectRequired)
}

func TestFreshRefreshPolicy(t *testing.T) {
	t.Run("near expiry", func(t *testing.T) {
		e := newEnv(t)
		e.connect()
		e.clock.Advance(54*time.Minute + 59*time.Second)
		_, _, _ = e.m.Fresh(e.ctx())
		if e.forge.refreshes() != 0 {
			t.Fatal("refreshed too early")
		}
		e.clock.Advance(time.Second)
		_, _, _ = e.m.Fresh(e.ctx())
		if e.forge.refreshes() != 1 {
			t.Fatal("did not refresh within 5 minutes of expiry")
		}
	})
	t.Run("daily", func(t *testing.T) {
		e := newEnv(t)
		e.forge.set(func(f *fakeForge) { f.expiresIn = 2592000 })
		e.connect()
		e.clock.Advance(24 * time.Hour)
		_, _, _ = e.m.Fresh(e.ctx())
		if e.forge.refreshes() != 0 {
			t.Fatal("refreshed before 24 hours")
		}
		e.clock.Advance(time.Second)
		_, _, _ = e.m.Fresh(e.ctx())
		if e.forge.refreshes() != 1 {
			t.Fatal("did not refresh after 24 hours")
		}
	})
	t.Run("Current never refreshes", func(t *testing.T) {
		e := newEnv(t)
		e.connect()
		e.clock.Advance(59 * time.Minute)
		if token, identity, err := e.m.Current(); e.err(err) != nil || !strings.HasPrefix(token, accessSentinel) || identity == "" {
			t.Fatal("Current failed")
		}
		if e.forge.refreshes() != 0 {
			t.Fatal("Current did network I/O")
		}
	})
}

func TestDisconnectDuringRefresh(t *testing.T) {
	e := newEnv(t)
	e.connect()
	gate, seen := make(chan struct{}), make(chan struct{}, 1)
	e.forge.set(func(f *fakeForge) { f.refreshGate, f.refreshSeen = gate, seen })
	e.clock.Advance(56 * time.Minute)
	done := make(chan error, 1)
	go func() {
		_, _, err := e.m.Fresh(e.ctx())
		done <- err
	}()
	<-seen
	if err := e.m.Disconnect(testActor); err != nil {
		t.Fatal(err)
	}
	close(gate)
	e.err(<-done)
	if _, err := os.Stat(e.dir + "/gitea.json"); !os.IsNotExist(err) {
		t.Fatal("a refresh finishing after Disconnect wrote the file back")
	}
	if e.m.Status().State != StateDisconnected {
		t.Fatal("disconnect undone")
	}
}
