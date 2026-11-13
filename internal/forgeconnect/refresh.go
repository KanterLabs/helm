package forgeconnect

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Current returns the stored access token and the connection identity
// without network I/O. The token may be close to expiry; use Fresh when a
// request is about to be made.
func (m *Manager) Current() (token, identity string, err error) {
	if !m.enabled {
		return "", "", fail(CodeUnavailable)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.connectedLocked(); err != nil {
		return "", "", err
	}
	return m.rec.AccessToken, m.rec.ID, nil
}

// Fresh returns a token that is not due for refresh, refreshing first when
// it is within 5 minutes of expiry, over 24 hours since the last refresh, or
// was rejected. A network failure keeps a token that has not expired.
func (m *Manager) Fresh(ctx context.Context) (token, identity string, err error) {
	token, identity, _, _, err = m.fresh(ctx)
	return token, identity, err
}

// Rejected reports that the API answered 401 for token. A token that came
// from a refresh less than a minute ago ends the connection; otherwise the
// next Fresh refreshes. Stale tokens are ignored.
func (m *Manager) Rejected(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token == "" || m.connectedLocked() != nil || m.rec.AccessToken != token {
		return
	}
	if m.recentlyRefreshedLocked() {
		m.endLocked(m.gen)
		return
	}
	m.rejected = true
}

func (m *Manager) connectedLocked() error {
	switch m.stateLocked() {
	case StateDisconnected:
		return fail(CodeUnavailable)
	case StateReconnectRequired:
		return fail(CodeReconnectRequired)
	}
	return nil
}

func (m *Manager) recentlyRefreshedLocked() bool {
	return !m.lastRefresh.IsZero() && m.now().Sub(m.lastRefresh) < rejectWindow
}

func (m *Manager) dueLocked() bool {
	if m.rec.RefreshToken == "" {
		return false
	}
	now := m.now()
	return m.rejected || !now.Before(m.rec.AccessExpiresAt.Add(-refreshMargin)) || now.Sub(m.rec.RefreshedAt) > refreshEvery
}

// fresh returns the token together with the server it belongs to, read under
// one lock, so a token is never sent to a server connected a moment later.
func (m *Manager) fresh(ctx context.Context) (token, identity, base string, seq uint64, err error) {
	if !m.enabled {
		return "", "", "", 0, fail(CodeUnavailable)
	}
	m.mu.Lock()
	if err := m.connectedLocked(); err != nil {
		m.mu.Unlock()
		return "", "", "", 0, err
	}
	rec, seen, due := *m.rec, m.refreshSeq, m.dueLocked()
	m.mu.Unlock()
	base = rec.Server.BaseURL
	if !due {
		return rec.AccessToken, rec.ID, base, seen, nil
	}
	renewed, next, err := m.refresh(ctx, seen)
	if err == nil {
		return renewed, rec.ID, base, next, nil
	}
	if Code(err) == CodeReconnectRequired || Code(err) == CodeUnavailable {
		return "", "", "", 0, err
	}
	if m.now().Before(rec.AccessExpiresAt) {
		return rec.AccessToken, rec.ID, base, seen, nil
	}
	return "", "", "", 0, err
}

// refresh exchanges the refresh token once. Callers pass the refresh
// sequence they saw; if another caller has refreshed since, its result is
// returned without a second upstream call. The new pair is persisted before
// it is used.
func (m *Manager) refresh(ctx context.Context, seen uint64) (string, uint64, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.mu.Lock()
	if err := m.connectedLocked(); err != nil {
		m.mu.Unlock()
		return "", 0, err
	}
	if m.refreshSeq != seen {
		token, seq := m.rec.AccessToken, m.refreshSeq
		m.mu.Unlock()
		return token, seq, nil
	}
	rec, gen := *m.rec, m.gen
	if rec.RefreshToken == "" {
		m.endLocked(gen)
		m.mu.Unlock()
		return "", 0, fail(CodeReconnectRequired)
	}
	m.mu.Unlock()
	// The forge may rotate the pair the moment it answers. A caller that goes
	// away must not cost us the reply, so the exchange outlives its context.
	exchange, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	defer cancel()
	r, err := m.postToken(exchange, rec.Server.BaseURL, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {rec.ClientID}, "refresh_token": {rec.RefreshToken},
	})
	if err != nil {
		return "", 0, codeOr(err, CodeServerUnreachable)
	}
	if e, ok := readTokenError(r); ok && (e.Error == "invalid_grant" || e.Error == "invalid_client" || e.Error == "unauthorized_client") {
		m.mu.Lock()
		m.endLocked(gen)
		m.mu.Unlock()
		return "", 0, fail(CodeReconnectRequired)
	}
	if r.status != http.StatusOK {
		return "", 0, fail(CodeServerUnreachable)
	}
	tokens, ok := parseToken(r.body)
	if !ok {
		return "", 0, fail(CodeExchangeFailed)
	}
	now := m.now()
	next := rec
	next.AccessToken = tokens.access
	if tokens.refresh != "" { // RFC 6749 section 6: no new refresh token means keep the old one
		next.RefreshToken = tokens.refresh
	}
	next.AccessExpiresAt, next.RefreshedAt = now.Add(tokens.lifetime), now
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gen != gen || m.rec == nil {
		return "", 0, fail(CodeReconnectRequired)
	}
	if err := m.store.save(&next); err != nil {
		m.logf(`{"level":"warn","msg":"gitea refresh not saved","code":%q}`, Code(err))
		return "", 0, err
	}
	m.rec, m.rejected, m.lastRefresh = &next, false, now
	m.refreshSeq++
	return next.AccessToken, m.refreshSeq, nil
}

// endLocked wipes the tokens and keeps server, client ID and account, so
// the administrator can sign in again with one click.
func (m *Manager) endLocked(gen uint64) {
	if m.gen != gen || m.rec == nil {
		return
	}
	next := *m.rec
	next.AccessToken, next.RefreshToken, next.AccessExpiresAt = "", "", time.Time{}
	if err := m.store.save(&next); err != nil {
		m.logf(`{"level":"warn","msg":"gitea sign-in end not saved","code":%q}`, Code(err))
	}
	m.rec, m.rejected, m.lastRefresh = &next, false, time.Time{}
	m.logf(`{"level":"warn","msg":"gitea sign-in ended","code":%q}`, CodeReconnectRequired)
}

// apiGet calls the forge API with a fresh token. On 401 it refreshes once
// and retries; a refreshed token rejected again within a minute ends the
// connection.
func (m *Manager) apiGet(ctx context.Context, path string, limit int64) (*reply, error) {
	token, _, base, seq, err := m.fresh(ctx)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		r, err := m.get(ctx, base+path, token, limit)
		if err != nil {
			return nil, codeOr(err, CodeServerUnreachable)
		}
		if r.status != http.StatusUnauthorized {
			return r, nil
		}
		m.mu.Lock()
		if m.connectedLocked() == nil && m.refreshSeq == seq && m.recentlyRefreshedLocked() {
			m.endLocked(m.gen)
		}
		ended := m.connectedLocked()
		m.mu.Unlock()
		if ended != nil {
			return nil, fail(CodeReconnectRequired)
		}
		if token, seq, err = m.refresh(ctx, seq); err != nil {
			return nil, err
		}
	}
	return nil, fail(CodeServerUnreachable)
}
