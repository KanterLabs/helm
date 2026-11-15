package githubconnect

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

// refreshCall is one refresh exchange that every concurrent caller shares.
type refreshCall struct {
	done  chan struct{}
	token string
	err   error
}

func sourceError(code string) error { return &githubprovider.Error{Code: code} }

// usableLocked maps the connection state to the provider's source errors.
func (m *Manager) usableLocked() error {
	switch m.stateLocked() {
	case StateDisconnected:
		return sourceError("not_configured")
	case StateReconnectRequired:
		return sourceError("reconnect_required")
	}
	return nil
}

// Current returns the stored access token and the connection's identity. It
// performs no network I/O. The identity is a random ID made at connect time,
// so a refresh never changes it.
func (m *Manager) Current() (token, identity string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usableLocked(); err != nil {
		return "", "", err
	}
	return m.rec.AccessToken, m.rec.ID, nil
}

// Fresh is Current, but first renews a token within 10 minutes of expiry or
// one GitHub refused. A failed renewal keeps a token that is still valid.
func (m *Manager) Fresh(ctx context.Context) (token, identity string, err error) {
	m.mu.Lock()
	if err := m.usableLocked(); err != nil {
		m.mu.Unlock()
		return "", "", err
	}
	if m.unsaved && m.saveLocked(m.rec) == nil {
		m.unsaved = false
	}
	rec, seq, due := *m.rec, m.refreshSeq, m.dueLocked()
	m.mu.Unlock()
	if !due {
		return rec.AccessToken, rec.ID, nil
	}
	token, err = m.refresh(ctx, seq)
	if err == nil {
		return token, rec.ID, nil
	}
	switch Code(err) {
	case CodeReconnectRequired:
		return "", "", sourceError("reconnect_required")
	case CodeNotConnected:
		return "", "", sourceError("not_configured")
	}
	if ctx.Err() != nil {
		return "", "", sourceError("unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	still := m.rec != nil && m.rec.ID == rec.ID && m.rec.AccessToken == rec.AccessToken && !m.rejected &&
		(rec.AccessExpiresAt.IsZero() || m.now().Before(rec.AccessExpiresAt))
	if still {
		return rec.AccessToken, rec.ID, nil
	}
	return "", "", sourceError("unavailable")
}

// Rejected hears that GitHub answered 401 for token. The next Fresh renews
// it. A token that cannot be renewed, or that came from a renewal moments
// ago, means the authorization is gone: the sign-in ends.
func (m *Manager) Rejected(token string) {
	m.mu.Lock()
	if m.rec == nil || m.rec.AccessToken == "" || subtle.ConstantTimeCompare([]byte(token), []byte(m.rec.AccessToken)) != 1 {
		m.mu.Unlock()
		return
	}
	if m.rec.RefreshToken != "" && (m.lastRefresh.IsZero() || m.now().Sub(m.lastRefresh) >= rejectWindow) {
		m.rejected = true
		m.mu.Unlock()
		return
	}
	m.endLocked()
	m.mu.Unlock()
	m.publish()
}

func (m *Manager) dueLocked() bool {
	r := m.rec
	if r.RefreshToken == "" {
		return false
	}
	return m.rejected || (!r.AccessExpiresAt.IsZero() && !m.now().Before(r.AccessExpiresAt.Add(-refreshMargin)))
}

// refresh renews the pair once for all concurrent callers. seen is the
// refresh sequence the caller read: if a refresh has finished since, its
// token is returned without another exchange. The exchange runs detached
// from the caller, so a caller that goes away cannot cost the rotated pair.
func (m *Manager) refresh(ctx context.Context, seen uint64) (string, error) {
	m.mu.Lock()
	if m.usableLocked() != nil {
		state := m.stateLocked()
		m.mu.Unlock()
		if state == StateDisconnected {
			return "", fail(CodeNotConnected)
		}
		return "", fail(CodeReconnectRequired)
	}
	if m.refreshSeq != seen {
		token := m.rec.AccessToken
		m.mu.Unlock()
		return token, nil
	}
	call := m.inflight
	if call == nil {
		if m.rec.RefreshToken == "" {
			m.mu.Unlock()
			return "", fail(CodeReconnectRequired)
		}
		call = &refreshCall{done: make(chan struct{})}
		m.inflight = call
		rec, gen := *m.rec, m.gen
		m.mu.Unlock()
		run := func() { m.exchange(ctx, call, rec, gen) }
		if !m.track(run) {
			run()
		}
	} else {
		m.mu.Unlock()
	}
	select {
	case <-call.done:
		return call.token, call.err
	case <-ctx.Done():
		return "", fail(CodeGitHubUnreachable)
	}
}

// exchange performs the refresh and persists the new pair before anyone can
// use it. If the write fails, the new pair is still the one in use (the old
// one is dead at GitHub) and the write is retried on the next Fresh.
func (m *Manager) exchange(ctx context.Context, call *refreshCall, rec record, gen uint64) {
	defer func() {
		m.mu.Lock()
		m.inflight = nil
		m.mu.Unlock()
		close(call.done)
	}()
	if m.clientID == "" {
		call.err = fail(CodeSigninUnavailable)
		return
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	r, err := m.postForm(detached, "/login/oauth/access_token", url.Values{
		"client_id": {m.clientID}, "grant_type": {"refresh_token"}, "refresh_token": {rec.RefreshToken},
	})
	if err != nil {
		call.err = err
		return
	}
	var oauth struct {
		Error string `json:"error"`
	}
	if (r.status != http.StatusOK && (r.status < 400 || r.status > 499)) || json.Unmarshal(r.body, &oauth) != nil {
		call.err = fail(CodeGitHubUnreachable)
		return
	}
	switch oauth.Error {
	case "":
	case "bad_refresh_token":
		m.mu.Lock()
		if m.gen == gen {
			m.endLocked()
		}
		m.mu.Unlock()
		m.publish()
		call.err = fail(CodeReconnectRequired)
		return
	default:
		call.err = fail(CodeGitHubRefused)
		return
	}
	tokens, ok := parseTokenReply(r.body)
	if !ok {
		call.err = fail(CodeGitHubUnreachable)
		return
	}
	now := m.now()
	next := rec
	next.AccessToken, next.AccessExpiresAt = tokens.access, time.Time{}
	if tokens.lifetime > 0 {
		next.AccessExpiresAt = now.Add(tokens.lifetime)
	}
	if tokens.refresh != "" { // no new refresh token means keep the old one
		next.RefreshToken = tokens.refresh
		if tokens.refreshLifetime > 0 {
			next.RefreshExpiresAt = now.Add(tokens.refreshLifetime)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gen != gen || m.rec == nil {
		call.err = fail(CodeNotConnected)
		return
	}
	// Keep what changed meanwhile (a sync's repositories), take the new pair.
	current := *m.rec
	current.AccessToken, current.AccessExpiresAt = next.AccessToken, next.AccessExpiresAt
	current.RefreshToken, current.RefreshExpiresAt = next.RefreshToken, next.RefreshExpiresAt
	err = m.saveLocked(&current)
	m.rec, m.rejected, m.lastRefresh, m.unsaved = &current, false, now, err != nil
	m.refreshSeq++
	if err != nil {
		m.logf(`{"level":"warn","msg":"github refresh not saved","code":%q}`, CodeStorageFailed)
	}
	call.token = current.AccessToken
}

// endLocked wipes both tokens and keeps the account, so the state survives a
// restart as reconnect_required. The generation bump fences late writes.
func (m *Manager) endLocked() {
	if m.rec == nil {
		return
	}
	next := *m.rec
	next.AccessToken, next.RefreshToken, next.AccessExpiresAt, next.RefreshExpiresAt = "", "", time.Time{}, time.Time{}
	if err := m.saveLocked(&next); err != nil {
		m.logf(`{"level":"warn","msg":"github sign-in end not saved","code":%q}`, CodeStorageFailed)
	}
	m.rec, m.rejected, m.unsaved, m.lastRefresh = &next, false, false, time.Time{}
	m.gen++
	m.logf(`{"level":"warn","msg":"github sign-in ended","code":%q}`, CodeReconnectRequired)
}

func (m *Manager) saveLocked(rec *record) error {
	err := m.store.save(rec)
	if err == nil {
		m.refusalLogged.Store(false)
	}
	return err
}
