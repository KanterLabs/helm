package forgeconnect

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func closes(t *testing.T, m *Manager) {
	t.Helper()
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
}

func TestKeepAlive(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.KeepAliveInterval = 10 * time.Millisecond })
	e.forge.set(func(f *fakeForge) { f.expiresIn = 2592000 })
	e.connect()
	e.m.Start(context.Background())
	time.Sleep(50 * time.Millisecond)
	if e.forge.refreshes() != 0 {
		t.Fatal("keep-alive refreshed a fresh sign-in")
	}
	e.clock.Advance(24*time.Hour + time.Minute)
	// Wait for the save, not only the forge's counter: the fsync can lag it.
	waitFor(t, "a saved keep-alive refresh", func() bool {
		return e.forge.refreshes() == 1 && e.stored().RefreshedAt.Equal(e.clock.Now())
	})
	time.Sleep(50 * time.Millisecond)
	if e.forge.refreshes() != 1 {
		t.Fatalf("refreshes = %d", e.forge.refreshes())
	}
	// A revoked grant found by the keep-alive ends the connection and logs
	// only the code.
	e.forge.set(func(f *fakeForge) { f.revoked = true })
	e.clock.Advance(25 * time.Hour)
	waitFor(t, "reconnect_required", func() bool { return e.m.Status().State == StateReconnectRequired })
	closes(t, e.m)
	e.mu.Lock()
	logs := strings.Join(e.logs, "\n")
	e.mu.Unlock()
	if !strings.Contains(logs, CodeReconnectRequired) {
		t.Fatalf("keep-alive did not log the outcome: %q", logs)
	}
}

func TestKeepAliveLifetime(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.KeepAliveInterval = 5 * time.Millisecond })
	e.connect()
	ctx, cancel := context.WithCancel(context.Background())
	e.m.Start(ctx)
	e.m.Start(ctx)
	closes(t, e.m)
	closes(t, e.m)
	e.clock.Advance(25 * time.Hour)
	time.Sleep(50 * time.Millisecond)
	if e.forge.refreshes() != 0 {
		t.Fatal("keep-alive ran after Close")
	}
	e.m.Start(ctx)
	time.Sleep(50 * time.Millisecond)
	if e.forge.refreshes() != 0 {
		t.Fatal("Start after Close started a loop")
	}
	cancel()

	// A cancelled parent context stops the loop; Close still returns.
	other := newEnv(t, func(o *Options) { o.KeepAliveInterval = 5 * time.Millisecond })
	parent, stop := context.WithCancel(context.Background())
	other.m.Start(parent)
	stop()
	closes(t, other.m)

	// Close without Start returns at once.
	closes(t, newEnv(t).m)
}

var errorShape = regexp.MustCompile(`^gitea connection: [a-z_]+$`)

// TestNoSecretLeaks drives every path that sees a secret or an upstream
// message and scans errors, logs and status for each sentinel. Every other
// test scans its own errors and logs the same way when it ends.
func TestNoSecretLeaks(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.KeepAliveInterval = 5 * time.Millisecond })
	try := func(err error) {
		if e.err(err) != nil && !errorShape.MatchString(err.Error()) {
			t.Errorf("error string carries more than its code: %q", err.Error())
		}
	}
	for _, change := range []func(*fakeForge){
		func(f *fakeForge) { f.confidential = true },
		func(f *fakeForge) { f.appDeleted = true },
		func(f *fakeForge) {
			f.tokenOverride = `{"access_token":"ACCESS-SENTINEL x","token_type":"bearer","expires_in":1}`
		},
		func(f *fakeForge) { f.userStatus = http.StatusInternalServerError },
	} {
		code, state := e.forge.approve(e.begin(testActor))
		e.forge.set(change)
		try(e.m.Finish(e.ctx(), testActor, state, code, "", ""))
		e.forge.set(func(f *fakeForge) { f.confidential, f.appDeleted, f.tokenOverride, f.userStatus = false, false, "", 0 })
	}
	for _, upstream := range []string{"access_denied", "server_error", "invalid_request"} {
		code, state := e.forge.approve(e.begin(testActor))
		try(e.m.Finish(e.ctx(), testActor, state, code, upstream, "a grant exists with different scope "+upstreamSentinel+" "+code))
	}
	_, err := e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base(), ClientID: accessSentinel + " x", AllowInsecureHTTP: true})
	try(err)
	_, err = e.m.Begin(e.ctx(), testActor, BeginInput{BaseURL: e.forge.base() + "/?code=" + codeSentinel, ClientID: testClientID, AllowInsecureHTTP: true})
	try(err)
	try(e.m.Finish(e.ctx(), testActor, "forged-"+codeSentinel, codeSentinel, "", ""))
	e.connect()
	token, _, _ := e.m.Current()
	e.forge.expireAccess()
	_, _, err = e.m.Repositories(e.ctx())
	try(err)
	e.forge.set(func(f *fakeForge) { f.orgsStatus = http.StatusOK })
	_ = e.m.Check(e.ctx())
	e.m.Rejected(token)
	_, _, err = e.m.Fresh(e.ctx())
	try(err)
	e.forge.set(func(f *fakeForge) { f.revoked = true })
	e.clock.Advance(25 * time.Hour)
	e.m.Start(context.Background())
	waitFor(t, "reconnect_required", func() bool { return e.m.Status().State == StateReconnectRequired })
	closes(t, e.m)
	_, _, err = e.m.Fresh(e.ctx())
	try(err)
	_, err = e.m.Probe(e.ctx(), e.forge.base()+"/x?token="+token, true)
	try(err)
	if len(e.forge.secrets()) < 10 {
		t.Fatal("the scenario did not exercise enough secrets for the scan to mean anything")
	}
	e.mu.Lock()
	if len(e.errs) < 10 || len(e.logs) == 0 {
		t.Errorf("scenario produced %d errors and %d log lines", len(e.errs), len(e.logs))
	}
	e.mu.Unlock()
	// e.scan runs at cleanup over the status document, every log line and
	// every error string recorded above.
}
