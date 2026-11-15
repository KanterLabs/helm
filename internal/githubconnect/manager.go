// Package githubconnect holds Helm's one installation-wide GitHub connection,
// made by GitHub's device flow (docs/GITHUB_DEVICE_FLOW_PLAN.md, C2–C4). It
// keeps the user access token fresh, lists the repositories the GitHub App's
// installations reach, and serves the token to githubprovider as its
// CredentialSource. Only GitHub's fixed web and API origins are contacted.
package githubconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

const (
	refreshMargin  = 10 * time.Minute
	rejectWindow   = time.Minute
	refreshTimeout = 10 * time.Second
	refreshBackoff = 30 * time.Second // after a failed exchange
	// maxBeginsPerMinute bounds device-code requests to GitHub. It stops a
	// runaway loop; it is well beyond what people connecting need.
	maxBeginsPerMinute = 20
)

// Connection states (C5).
const (
	ModeDeviceFlow    = "device_flow"
	ModeOperatorToken = "operator_token"

	StateDisconnected      = "disconnected"
	StateConnected         = "connected"
	StateNoRepositories    = "no_repositories"
	StateReconnectRequired = "reconnect_required"
)

var (
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	slugPattern     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// Options configures a Manager (contract C2).
type Options struct {
	ClientID, AppSlug string   // empty ClientID turns sign-in off
	StateDir          string   // holds connection.json; required
	Ceiling           []string // optional HELM_GITHUB_REPOSITORIES
	// Constructor-only test seams. Runtime configuration never sets them.
	HTTPClient           *http.Client
	WebOrigin, APIOrigin string // default https://github.com, https://api.github.com
	Now                  func() time.Time
}

// Manager owns the connection. All methods are safe for concurrent use.
type Manager struct {
	clientID, slug string
	web, api       string
	ceiling        map[string]bool // nil: no ceiling
	http           *http.Client
	now            func() time.Time
	store          *fileStore
	logf           func(format string, args ...any)
	after          func(time.Duration) <-chan time.Time
	refusalLogged  atomic.Bool

	mu          sync.Mutex
	rec         *record
	broken      bool   // a file exists that cannot be trusted
	gen         uint64 // bumped on connect and disconnect; fences late writes
	attempt     *attempt
	rejected    bool      // GitHub refused the current token
	lastRefresh time.Time // when the current token came from a refresh
	refreshSeq  uint64    // bumped on every successful refresh
	inflight    *refreshCall
	unsaved     bool // a refreshed pair is held in memory only
	// A failed exchange is not repeated for refreshBackoff; refreshFailedGen
	// ties it to the connection it failed for.
	refreshFailedAt  time.Time
	refreshFailedGen uint64
	// Consecutive refusals of tokens fresh from a refresh, for rejectGen.
	rejectStreak int
	rejectGen    uint64
	announced    string // the state the listeners were last told about
	listeners    []func([]string)
	begins       []time.Time // recent device-code requests
	syncing      *syncCall   // the sync every concurrent caller shares

	beginMu  sync.Mutex // one Begin at a time: a double click asks GitHub once
	notifyMu sync.Mutex // orders allowlist notifications

	life    sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wake    chan struct{}
	wg      sync.WaitGroup
}

// New loads the credential file. It does no network I/O and starts no
// goroutines.
func New(o Options) (*Manager, error) { return newManager(o, log.Printf) }

func newManager(o Options, logf func(string, ...any)) (*Manager, error) {
	web, ok := normalizeOrigin(o.WebOrigin, defaultWebOrigin)
	if !ok {
		return nil, fail(CodeInvalidRequest)
	}
	api, ok := normalizeOrigin(o.APIOrigin, defaultAPIOrigin)
	if !ok {
		return nil, fail(CodeInvalidRequest)
	}
	if o.ClientID != "" && !clientIDPattern.MatchString(o.ClientID) || o.AppSlug != "" && !slugPattern.MatchString(o.AppSlug) || strings.TrimSpace(o.StateDir) == "" {
		return nil, fail(CodeInvalidRequest)
	}
	m := &Manager{clientID: o.ClientID, slug: o.AppSlug, web: web, api: api, now: o.Now, logf: logf, after: time.After, wake: make(chan struct{}, 1)}
	if m.now == nil {
		m.now = time.Now
	}
	if o.HTTPClient != nil {
		m.http = confine(o.HTTPClient)
	} else {
		m.http = newHTTPClient(proxyFromEnvironment)
	}
	if len(o.Ceiling) > 0 {
		m.ceiling = map[string]bool{}
		for _, raw := range o.Ceiling {
			if repository, err := githubprovider.NormalizeRepository(strings.TrimSpace(raw)); err == nil {
				m.ceiling[repository] = true
			}
		}
	}
	m.store = newFileStore(o.StateDir)
	m.store.refused = func(reason string) {
		if m.refusalLogged.CompareAndSwap(false, true) {
			m.logf(`{"level":"warn","msg":"github state directory refused","reason":%q}`, reason)
		}
	}
	m.store.cleanTemps()
	m.rec, m.broken = m.store.load()
	if m.rec != nil {
		m.rec.Repositories = m.capped(m.rec.Repositories)
	}
	return m, nil
}

// Start runs the polling goroutine until ctx ends or Close is called. It
// does nothing after Close or a second time.
func (m *Manager) Start(ctx context.Context) {
	m.life.Lock()
	defer m.life.Unlock()
	if m.started || m.closed {
		return
	}
	m.started = true
	loop, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.pollLoop(loop)
	}()
}

// Close stops polling, waits for it and for any refresh in flight, and ends
// a pending attempt. It is safe to call twice.
func (m *Manager) Close() {
	m.life.Lock()
	m.closed = true
	cancel := m.cancel
	m.life.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
	m.mu.Lock()
	if m.attempt != nil && m.attempt.state == AttemptPending {
		m.attempt = nil
	}
	m.mu.Unlock()
}

func (m *Manager) isClosed() bool {
	m.life.Lock()
	defer m.life.Unlock()
	return m.closed
}

// track runs f as a goroutine Close waits for, unless the manager is closed.
func (m *Manager) track(f func()) bool {
	m.life.Lock()
	defer m.life.Unlock()
	if m.closed {
		return false
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		f()
	}()
	return true
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// OnRepositories registers fn and calls it at once with the current
// allowlist, then after every load, sync, connect, end and disconnect. fn is
// never called while the manager holds a lock it could need.
func (m *Manager) OnRepositories(fn func([]string)) {
	if fn == nil {
		return
	}
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
	m.publish()
}

// Repositories is the effective allowlist: empty unless connected.
func (m *Manager) Repositories() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repositoriesLocked()
}

func (m *Manager) repositoriesLocked() []string {
	if m.stateLocked() != StateConnected {
		return []string{}
	}
	return slices.Clone(m.rec.Repositories)
}

// publish tells every listener the current allowlist. Notifications are
// serialized and always carry the latest state, so they cannot arrive out
// of order.
func (m *Manager) publish() {
	m.notifyMu.Lock()
	defer m.notifyMu.Unlock()
	m.mu.Lock()
	list, listeners := m.repositoriesLocked(), slices.Clone(m.listeners)
	m.announced = m.stateLocked() // before the calls: a listener may read us
	m.mu.Unlock()
	for _, fn := range listeners {
		fn(slices.Clone(list))
	}
}

// capped applies the ceiling to a stored list (it may have narrowed since).
func (m *Manager) capped(list []string) []string {
	out := []string{}
	for _, repository := range list {
		if m.ceiling == nil || m.ceiling[repository] {
			out = append(out, repository)
		}
	}
	return out
}

func (m *Manager) stateLocked() string {
	switch {
	case m.rec == nil && m.broken:
		return StateReconnectRequired
	case m.rec == nil:
		return StateDisconnected
	case m.endedLocked():
		return StateReconnectRequired
	case len(m.rec.Repositories) == 0:
		return StateNoRepositories
	}
	return StateConnected
}

// endedLocked: no token, or an expired token that cannot be renewed.
func (m *Manager) endedLocked() bool {
	r, now := m.rec, m.now()
	if r.AccessToken == "" {
		return true
	}
	if r.AccessExpiresAt.IsZero() || now.Before(r.AccessExpiresAt) {
		return false
	}
	return r.RefreshToken == "" || (!r.RefreshExpiresAt.IsZero() && !now.Before(r.RefreshExpiresAt))
}

func randomID(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf) // never fails on supported platforms
	return base64.RawURLEncoding.EncodeToString(buf)
}
