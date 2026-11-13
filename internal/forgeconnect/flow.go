package forgeconnect

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxTokenLifetime   = 30 * 24 * time.Hour
	maxRepositoryCount = 1_000_000_000
)

// Begin starts a sign-in for one actor and returns the forge's authorize
// URL. A second Begin by the same actor replaces the first.
func (m *Manager) Begin(ctx context.Context, actorID string, in BeginInput) (string, error) {
	if !m.signInAvailable() {
		return "", fail(CodeUnavailable)
	}
	if actorID == "" {
		return "", fail(CodeStateInvalid)
	}
	m.mu.Lock()
	state := m.stateLocked()
	var stored *record
	if m.rec != nil {
		copied := *m.rec
		stored = &copied
	}
	m.mu.Unlock()
	if state == StateConnected {
		return "", fail(CodeAlreadyConnected)
	}
	base, clientID, allowHTTP := strings.TrimSpace(in.BaseURL), strings.TrimSpace(in.ClientID), in.AllowInsecureHTTP
	if state == StateReconnectRequired && stored != nil {
		if base == "" {
			base, allowHTTP = stored.Server.BaseURL, allowHTTP || stored.Server.InsecureHTTP
		}
		if clientID == "" {
			clientID = stored.ClientID
		}
	}
	if !validClientID(clientID) {
		return "", fail(CodeClientUnknown)
	}
	server, err := m.Probe(ctx, base, allowHTTP)
	if err != nil {
		return "", err
	}
	pending := pendingSignIn{state: randomToken(32), verifier: randomToken(48), clientID: clientID, server: server, expires: m.now().Add(pendingLifetime)}
	challenge := sha256.Sum256([]byte(pending.verifier))
	m.mu.Lock()
	now := m.now()
	for actor, item := range m.pending {
		if !now.Before(item.expires) {
			delete(m.pending, actor)
		}
	}
	m.pending[actorID] = pending
	m.mu.Unlock()
	query := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {m.redirect},
		"response_type":         {"code"},
		"state":                 {pending.state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"scope":                 {gitea.Scope},
	}
	return server.BaseURL + gitea.AuthorizePath + "?" + query.Encode(), nil
}

// Finish completes the sign-in the same actor started. It consumes the
// pending sign-in whatever the outcome.
func (m *Manager) Finish(ctx context.Context, actorID, state, code, upstreamError, upstreamDescription string) error {
	err := m.finish(ctx, actorID, state, code, upstreamError, upstreamDescription)
	if err != nil {
		// Code only: an operator can see a sign-in failed without any upstream text.
		m.logf(`{"level":"warn","msg":"gitea sign-in failed","code":%q}`, Code(err))
	}
	return err
}

func (m *Manager) finish(ctx context.Context, actorID, state, code, upstreamError, upstreamDescription string) error {
	if !m.signInAvailable() {
		return fail(CodeUnavailable)
	}
	m.mu.Lock()
	pending, ok := m.pending[actorID]
	delete(m.pending, actorID)
	m.mu.Unlock()
	if !ok || state == "" || !m.now().Before(pending.expires) || subtle.ConstantTimeCompare([]byte(state), []byte(pending.state)) != 1 {
		return fail(CodeStateInvalid)
	}
	if upstreamError != "" {
		return callbackError(upstreamError, upstreamDescription)
	}
	if code == "" || len(code) > 4096 {
		return fail(CodeAuthorizeFailed)
	}
	if m.Status().State == StateConnected {
		return fail(CodeAlreadyConnected)
	}
	base := pending.server.BaseURL
	r, err := m.postToken(ctx, base, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {pending.clientID}, "code": {code},
		"redirect_uri": {m.redirect}, "code_verifier": {pending.verifier},
	})
	if err != nil {
		return codeOr(err, CodeExchangeFailed)
	}
	if r.status != http.StatusOK {
		return exchangeError(r)
	}
	tokens, ok := parseToken(r.body)
	if !ok {
		return fail(CodeExchangeFailed)
	}
	r, err = m.get(ctx, base+gitea.UserPath, tokens.access, maxBody)
	if err != nil {
		return codeOr(err, CodeExchangeFailed)
	}
	account, ok := parseAccount(r)
	if !ok {
		return fail(CodeExchangeFailed)
	}
	server := pending.server
	if server.Version == "" {
		if version := m.version(ctx, base, tokens.access); version != "" {
			server.Version, server.Product = version, productFor(version)
		}
	}
	access := AccessUnknown
	if r, err := m.get(ctx, base+gitea.LimitCheckPath, tokens.access, maxBody); err == nil {
		access = limitStatus(r.status)
	}
	count, hasCount := -1, false
	if r, err := m.get(ctx, base+gitea.ReposPath+"?limit=1", tokens.access, maxBody); err == nil && r.status == http.StatusOK {
		count, hasCount = totalCount(r.header)
	}
	now := m.now()
	rec := &record{
		Version: recordVersion, ID: randomToken(16), Kind: gitea.Kind, Server: server, ClientID: pending.clientID,
		AccessToken: tokens.access, RefreshToken: tokens.refresh, AccessExpiresAt: now.Add(tokens.lifetime),
		RefreshedAt: now, Account: account, Access: access, ConnectedBy: actorID, ConnectedAt: now, CheckedAt: now,
		Check: CheckResult{State: CheckOK},
	}
	if hasCount {
		rec.RepositoryCount = count
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stateLocked() == StateConnected {
		return fail(CodeAlreadyConnected)
	}
	if err := m.store.save(rec); err != nil {
		return err
	}
	m.rec, m.broken, m.rejected, m.lastRefresh = rec, false, false, time.Time{}
	m.gen++
	m.check, m.repoCount = rec.Check, rec.RepositoryCount
	m.logf(`{"level":"info","msg":"gitea connected","access":%q}`, access)
	return nil
}

func callbackError(upstream, description string) error {
	switch {
	case upstream == "access_denied":
		return fail(CodeDenied)
	case upstream == "server_error" && strings.Contains(strings.ToLower(description), "different scope"):
		return fail(CodeScopeConflict)
	}
	return fail(CodeAuthorizeFailed)
}

type tokenError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

func readTokenError(r *reply) (tokenError, bool) {
	var e tokenError
	if r.status < 400 || r.status > 499 || json.Unmarshal(r.body, &e) != nil || e.Error == "" {
		return e, false
	}
	return e, true
}

func exchangeError(r *reply) error {
	if e, ok := readTokenError(r); ok {
		if strings.Contains(strings.ToLower(e.Description), "client secret") {
			return fail(CodeClientConfidential)
		}
		if e.Error == "invalid_client" {
			return fail(CodeClientUnknown)
		}
	}
	return fail(CodeExchangeFailed)
}

type tokenSet struct {
	access, refresh string
	lifetime        time.Duration
}

// parseToken validates a token response: bearer, a sane access token, a
// lifetime between one second and 30 days, an optional refresh token.
func parseToken(body []byte) (tokenSet, bool) {
	var raw struct {
		AccessToken  string      `json:"access_token"`
		TokenType    string      `json:"token_type"`
		ExpiresIn    json.Number `json:"expires_in"`
		RefreshToken string      `json:"refresh_token"`
	}
	if json.Unmarshal(body, &raw) != nil || !strings.EqualFold(raw.TokenType, "bearer") || !validToken(raw.AccessToken) {
		return tokenSet{}, false
	}
	if raw.RefreshToken != "" && !validToken(raw.RefreshToken) {
		return tokenSet{}, false
	}
	seconds, err := strconv.ParseInt(raw.ExpiresIn.String(), 10, 64)
	if err != nil || seconds < 1 || time.Duration(seconds)*time.Second > maxTokenLifetime {
		return tokenSet{}, false
	}
	return tokenSet{access: raw.AccessToken, refresh: raw.RefreshToken, lifetime: time.Duration(seconds) * time.Second}, true
}

func parseAccount(r *reply) (Account, bool) {
	var account Account
	if r.status != http.StatusOK || json.Unmarshal(r.body, &account) != nil || account.ID < 1 || account.Login == "" || len(account.Login) > 255 {
		return Account{}, false
	}
	return Account{Login: account.Login, ID: account.ID}, true
}

func limitStatus(status int) string {
	switch status {
	case http.StatusForbidden:
		return AccessScoped
	case http.StatusOK:
		return AccessFull
	}
	return AccessUnknown
}

func totalCount(header http.Header) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(header.Get("X-Total-Count")))
	if err != nil || n < 0 || n > maxRepositoryCount {
		return 0, false
	}
	return n, true
}

func (m *Manager) postToken(ctx context.Context, base string, form url.Values) (*reply, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+gitea.TokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fail(CodeInvalidServerURL)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return m.send(request, maxBody)
}
