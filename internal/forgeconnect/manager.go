// Package forgeconnect connects Helm to one self-hosted Gitea or Forgejo by
// OAuth (authorization code with PKCE, public client, no client secret),
// keeps the sign-in alive, proves the access is limited and lists the
// repositories the account reaches. See docs/GITEA_OAUTH_PLAN.md (C1–C4).
package forgeconnect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	pendingLifetime  = 10 * time.Minute
	refreshMargin    = 5 * time.Minute
	refreshEvery     = 24 * time.Hour
	rejectWindow     = time.Minute
	defaultKeepAlive = time.Hour
)

// Connection states.
const (
	StateDisconnected      = "disconnected"
	StateConnected         = "connected"
	StateReconnectRequired = "reconnect_required"
)

// Options configures a Manager.
type Options struct {
	Enabled      bool         // HELM_GITEA_OAUTH
	PublicOrigin string       // empty makes sign-in unavailable
	StateDir     string       // holds gitea.json
	HTTPClient   *http.Client // constructor-only test seam
	Now          func() time.Time
	// KeepAliveInterval is a constructor-only test seam: how often the
	// keep-alive wakes. Zero means one hour.
	KeepAliveInterval time.Duration
}

// BeginInput starts a sign-in. In reconnect_required, empty fields reuse the
// stored server and client ID.
type BeginInput struct {
	BaseURL, ClientID string
	AllowInsecureHTTP bool
}

type pendingSignIn struct {
	state, verifier, clientID string
	server                    Server
	expires                   time.Time
}

// Manager owns the one installation-wide forge connection.
type Manager struct {
	enabled  bool
	origin   string
	redirect string
	http     *http.Client
	now      func() time.Time
	store    *fileStore
	interval time.Duration
	logf     func(format string, args ...any)

	mu          sync.Mutex
	rec         *record
	broken      bool   // the file exists but cannot be trusted
	gen         uint64 // bumped on connect and disconnect; fences writes
	pending     map[string]pendingSignIn
	repoCount   int
	check       CheckResult
	refreshSeq  uint64    // bumped on every successful refresh
	lastRefresh time.Time // when the current token came from a refresh
	rejected    bool      // the API refused the current token

	refreshMu sync.Mutex // single flight for refresh

	life    sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New loads the state file. It does no network I/O and starts no goroutines.
func New(o Options) (*Manager, error) {
	origin, err := normalizeOrigin(o.PublicOrigin)
	if err != nil {
		return nil, err
	}
	if o.Enabled && o.StateDir == "" {
		return nil, fail(CodeUnavailable)
	}
	m := &Manager{
		enabled: o.Enabled, origin: origin, redirect: RedirectURI(origin), now: o.Now,
		interval: o.KeepAliveInterval, logf: log.Printf, pending: map[string]pendingSignIn{},
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.interval <= 0 {
		m.interval = defaultKeepAlive
	}
	if o.HTTPClient != nil {
		m.http = injectedClient(o.HTTPClient)
	} else {
		m.http = newGuardedClient(loadRootPool(), requestTimeout)
	}
	if o.StateDir != "" {
		m.store = newFileStore(o.StateDir)
		m.store.cleanTemps()
		m.rec, m.broken = m.store.load()
	}
	return m, nil
}

// Start runs the keep-alive: it wakes on the interval and refreshes when the
// last refresh is over 24 hours old, so an idle refresh token never lapses.
func (m *Manager) Start(ctx context.Context) {
	m.life.Lock()
	defer m.life.Unlock()
	if m.started || m.closed || !m.enabled {
		return
	}
	m.started = true
	loop, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			m.keepAlive(loop)
			select {
			case <-loop.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Close stops the keep-alive and waits for it. It is safe to call twice.
func (m *Manager) Close() {
	m.life.Lock()
	m.closed = true
	cancel := m.cancel
	m.life.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

func (m *Manager) keepAlive(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	due := m.stateLocked() == StateConnected && m.rec.RefreshToken != "" && m.now().Sub(m.rec.RefreshedAt) > refreshEvery
	seq := m.refreshSeq
	m.mu.Unlock()
	if !due {
		return
	}
	if _, _, err := m.refresh(ctx, seq); err != nil {
		m.logf(`{"level":"warn","msg":"gitea keep-alive refresh failed","code":%q}`, Code(err))
	}
}

func (m *Manager) signInAvailable() bool { return m.enabled && m.origin != "" }

func (m *Manager) stateLocked() string {
	switch {
	case m.rec == nil && m.broken:
		return StateReconnectRequired
	case m.rec == nil:
		return StateDisconnected
	case m.rec.AccessToken == "":
		return StateReconnectRequired
	case m.rec.RefreshToken == "" && !m.now().Before(m.rec.AccessExpiresAt):
		return StateReconnectRequired
	}
	return StateConnected
}

// BaseURL is the connected forge's base URL, or "".
func (m *Manager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rec == nil {
		return ""
	}
	return m.rec.Server.BaseURL
}

func randomToken(size int) string {
	buf := make([]byte, size)
	_, _ = rand.Read(buf) // never fails on supported platforms (Go 1.24+)
	return base64.RawURLEncoding.EncodeToString(buf)
}
