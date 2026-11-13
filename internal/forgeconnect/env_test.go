package forgeconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEnv is one Helm installation talking to one fake forge. Every error a
// test passes through e.err and every log line is scanned for secrets when
// the test ends.
type testEnv struct {
	t     *testing.T
	forge *fakeForge
	clock *fakeClock
	dir   string
	m     *Manager

	mu   sync.Mutex
	logs []string
	errs []string
}

func newEnv(t *testing.T, change ...func(*Options)) *testEnv {
	t.Helper()
	e := &testEnv{t: t, forge: newFakeForge(t, ""), clock: newFakeClock(), dir: t.TempDir() + "/forge"}
	e.m = e.open(change...)
	t.Cleanup(e.scan)
	return e
}

func (e *testEnv) options(change ...func(*Options)) Options {
	options := Options{Enabled: true, PublicOrigin: testOrigin, StateDir: e.dir, Now: e.clock.Now, KeepAliveInterval: time.Hour}
	for _, c := range change {
		c(&options)
	}
	return options
}

// open builds a fresh Manager over the same state directory, as a restart
// would.
func (e *testEnv) open(change ...func(*Options)) *Manager {
	e.t.Helper()
	m, err := New(e.options(change...))
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	m.logf = func(format string, args ...any) {
		e.mu.Lock()
		e.logs = append(e.logs, fmt.Sprintf(format, args...))
		e.mu.Unlock()
	}
	e.t.Cleanup(m.Close)
	return m
}

func (e *testEnv) err(err error) error {
	if err != nil {
		e.mu.Lock()
		e.errs = append(e.errs, err.Error())
		e.mu.Unlock()
	}
	return err
}

func (e *testEnv) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	e.t.Cleanup(cancel)
	return ctx
}

func (e *testEnv) begin(actor string) string {
	e.t.Helper()
	authorize, err := e.m.Begin(e.ctx(), actor, BeginInput{BaseURL: e.forge.base(), ClientID: testClientID, AllowInsecureHTTP: true})
	if e.err(err) != nil {
		e.t.Fatalf("Begin: %v", err)
	}
	return authorize
}

// connect runs the whole sign-in and fails the test unless it succeeds.
func (e *testEnv) connect() {
	e.t.Helper()
	code, state := e.forge.approve(e.begin(testActor))
	if err := e.err(e.m.Finish(e.ctx(), testActor, state, code, "", "")); err != nil {
		e.t.Fatalf("Finish: %v", err)
	}
	if got := e.m.Status().State; got != StateConnected {
		e.t.Fatalf("state after sign-in = %q", got)
	}
}

func (e *testEnv) scan() {
	e.t.Helper()
	status, _ := json.Marshal(e.m.Status())
	e.mu.Lock()
	haystacks := append(append([]string{string(status)}, e.logs...), e.errs...)
	e.mu.Unlock()
	for _, secret := range e.forge.secrets() {
		for _, text := range haystacks {
			if secret != "" && strings.Contains(text, secret) {
				e.t.Errorf("secret %.12q… leaked into %q", secret, text)
			}
		}
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if Code(err) != code {
		t.Fatalf("code = %q (err %v), want %q", Code(err), err, code)
	}
}
