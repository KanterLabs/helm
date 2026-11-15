package githubconnect

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Attempt states (C5).
const (
	AttemptPending = "pending"
	AttemptExpired = "expired"
	AttemptDenied  = "denied"
	AttemptFailed  = "failed"
)

const (
	minInterval      = 5 * time.Second
	maxInterval      = 60 * time.Second
	maxSlowDown      = 5 * time.Minute
	slowDownStep     = 5 * time.Second
	minAttemptLife   = time.Minute
	maxAttemptLife   = 30 * time.Minute
	maxPollFailures  = 5
	maxTokenLifetime = 30 * 24 * time.Hour
	minTokenLifetime = time.Minute
	maxRefreshLife   = 2 * 366 * 24 * time.Hour
	deviceGrantType  = "urn:ietf:params:oauth:grant-type:device_code"
)

var userCodePattern = regexp.MustCompile(`^[A-Z0-9]{4,8}(?:-[A-Z0-9]{4,8})?$`)

// attempt is one device-flow sign-in. The device code never leaves the
// process; the user code is shown only to the administrator who began it.
type attempt struct {
	id, actor            string
	deviceCode, userCode string
	verificationURI      string
	expires, next        time.Time
	interval             time.Duration
	state, reason        string
	failures             int
}

// Begin starts a sign-in for actorID, or returns the one that administrator
// already has pending. Another administrator's attempt is replaced.
func (m *Manager) Begin(ctx context.Context, actorID string) (Status, error) {
	if m.clientID == "" || m.isClosed() {
		return m.Status(actorID), fail(CodeSigninUnavailable)
	}
	if !validActor(actorID) {
		return m.Status(actorID), fail(CodeInvalidRequest)
	}
	m.beginMu.Lock()
	defer m.beginMu.Unlock()
	m.mu.Lock()
	if state := m.stateLocked(); state == StateConnected || state == StateNoRepositories {
		m.mu.Unlock()
		return m.Status(actorID), fail(CodeAlreadyConnected)
	}
	if a := m.attempt; a != nil && a.actor == actorID && a.state == AttemptPending && m.now().Before(a.expires) {
		s := m.statusLocked(actorID)
		m.mu.Unlock()
		return s, nil
	}
	now := m.now()
	// Forget requests older than a minute, and any stamped in the future (the
	// clock stepped back): those must not hold the cap shut.
	m.begins = slices.DeleteFunc(m.begins, func(at time.Time) bool { return now.Sub(at) >= time.Minute || at.After(now) })
	if len(m.begins) >= maxBeginsPerMinute {
		m.mu.Unlock()
		return m.Status(actorID), fail(CodeTooManyAttempts)
	}
	m.begins = append(m.begins, now)
	m.mu.Unlock()
	att, err := m.requestDeviceCode(ctx, actorID)
	if err != nil {
		m.logFailure(Code(err))
		return m.Status(actorID), err
	}
	m.mu.Lock()
	if state := m.stateLocked(); state == StateConnected || state == StateNoRepositories {
		m.mu.Unlock()
		return m.Status(actorID), fail(CodeAlreadyConnected)
	}
	m.attempt = att
	s := m.statusLocked(actorID)
	m.mu.Unlock()
	m.signal()
	return s, nil
}

// Cancel ends the attempt with attemptID if actorID began it. A stale ID
// changes nothing, so an old tab cannot cancel the attempt that replaced it,
// and another administrator can replace an attempt but not cancel it.
func (m *Manager) Cancel(actorID, attemptID string) Status {
	m.mu.Lock()
	if m.attempt != nil && attemptID != "" && m.attempt.id == attemptID && m.attempt.actor == actorID {
		m.attempt = nil
	}
	s := m.statusLocked(actorID)
	m.mu.Unlock()
	m.signal()
	return s
}

func (m *Manager) logFailure(code string) {
	m.logf(`{"level":"warn","msg":"github sign-in failed","code":%q}`, code)
}

// endAttemptLocked finishes att and logs the outcome once (amendment A4).
func (m *Manager) endAttemptLocked(att *attempt, state, code string) {
	att.state, att.reason, att.deviceCode, att.userCode = state, code, "", ""
	m.logFailure(code)
}

func (m *Manager) requestDeviceCode(ctx context.Context, actorID string) (*attempt, error) {
	r, err := m.postForm(ctx, "/login/device/code", url.Values{"client_id": {m.clientID}})
	if err != nil {
		return nil, err
	}
	if r.status >= 400 && r.status < 500 {
		return nil, fail(CodeGitHubRefused)
	}
	if r.status != http.StatusOK {
		return nil, fail(CodeGitHubUnreachable)
	}
	var raw struct {
		Error           string      `json:"error"`
		DeviceCode      string      `json:"device_code"`
		UserCode        string      `json:"user_code"`
		VerificationURI string      `json:"verification_uri"`
		ExpiresIn       json.Number `json:"expires_in"`
		Interval        json.Number `json:"interval"`
	}
	if json.Unmarshal(r.body, &raw) != nil {
		return nil, fail(CodeGitHubUnreachable)
	}
	if raw.Error != "" {
		return nil, fail(CodeGitHubRefused)
	}
	life, okLife := seconds(raw.ExpiresIn, minAttemptLife, maxAttemptLife)
	interval, okInterval := minInterval, true
	if raw.Interval != "" {
		interval, okInterval = seconds(raw.Interval, 0, maxInterval)
		interval = max(interval, minInterval)
	}
	if !okLife || !okInterval || !validToken(raw.DeviceCode) || len(raw.DeviceCode) > 256 ||
		!userCodePattern.MatchString(raw.UserCode) || raw.VerificationURI != m.web+"/login/device" {
		return nil, fail(CodeGitHubUnreachable)
	}
	now := m.now()
	return &attempt{
		id: randomID(18), actor: actorID, deviceCode: raw.DeviceCode, userCode: raw.UserCode,
		verificationURI: raw.VerificationURI, expires: now.Add(life), next: now.Add(interval),
		interval: interval, state: AttemptPending,
	}, nil
}

// seconds parses a whole number of seconds within [lo, hi].
func seconds(n json.Number, lo, hi time.Duration) (time.Duration, bool) {
	v, err := strconv.ParseInt(n.String(), 10, 64)
	if err != nil || v < 0 || v > int64(hi/time.Second) {
		return 0, false
	}
	d := time.Duration(v) * time.Second
	return d, d >= lo
}

// pollLoop is the one polling goroutine. It sleeps until the pending
// attempt's next poll (or its expiry) and wakes early on Begin, Cancel or
// Disconnect.
func (m *Manager) pollLoop(ctx context.Context) {
	for {
		m.mu.Lock()
		att := m.attempt
		pending := att != nil && att.state == AttemptPending
		var wait time.Duration
		if pending {
			now := m.now()
			wait = max(min(att.next.Sub(now), att.expires.Sub(now)), 0)
		}
		m.mu.Unlock()
		if !pending {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
			continue
		case <-m.after(wait):
		}
		m.pollOnce(ctx, att)
	}
}

// pollOnce asks GitHub once whether att was approved. It does nothing for
// an attempt that was replaced, cancelled or ended meanwhile.
func (m *Manager) pollOnce(ctx context.Context, att *attempt) {
	m.mu.Lock()
	if m.attempt != att || att.state != AttemptPending {
		m.mu.Unlock()
		return
	}
	if !m.now().Before(att.expires) {
		m.endAttemptLocked(att, AttemptExpired, "expired")
		m.mu.Unlock()
		return
	}
	deviceCode := att.deviceCode
	m.mu.Unlock()
	r, err := m.postForm(ctx, "/login/oauth/access_token", url.Values{
		"client_id": {m.clientID}, "device_code": {deviceCode}, "grant_type": {deviceGrantType},
	})
	if ctx.Err() != nil {
		return
	}
	var oauth struct {
		Error    string      `json:"error"`
		Interval json.Number `json:"interval"`
	}
	if err != nil || (r.status != http.StatusOK && (r.status < 400 || r.status > 499)) || json.Unmarshal(r.body, &oauth) != nil {
		m.pollFailed(att)
		return
	}
	if oauth.Error == "" {
		tokens, ok := parseTokenReply(r.body)
		if !ok {
			m.pollFailed(att)
			return
		}
		m.completeSignIn(ctx, att, tokens)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempt != att || att.state != AttemptPending {
		return
	}
	att.failures = 0
	switch oauth.Error {
	case "authorization_pending":
	case "slow_down":
		att.interval = min(att.interval+slowDownStep, maxSlowDown)
		if asked, ok := seconds(oauth.Interval, 0, maxSlowDown); ok && asked > att.interval {
			att.interval = asked
		}
	case "expired_token":
		m.endAttemptLocked(att, AttemptExpired, "expired")
		return
	case "access_denied":
		m.endAttemptLocked(att, AttemptDenied, "denied")
		return
	default:
		m.endAttemptLocked(att, AttemptFailed, CodeGitHubRefused)
		return
	}
	att.next = m.now().Add(att.interval)
}

// pollFailed counts a poll GitHub did not answer properly. A few in a row
// end the attempt rather than leave the code on screen until it expires.
func (m *Manager) pollFailed(att *attempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempt != att || att.state != AttemptPending {
		return
	}
	att.failures++
	if att.failures >= maxPollFailures {
		m.endAttemptLocked(att, AttemptFailed, CodeGitHubUnreachable)
		return
	}
	att.next = m.now().Add(att.interval)
}

// completeSignIn turns approved tokens into the connection, unless the
// attempt was replaced, cancelled or ended while GitHub answered. Tokens of
// a discarded attempt are dropped; Helm cannot revoke them without a secret.
func (m *Manager) completeSignIn(ctx context.Context, att *attempt, tokens tokenSet) {
	end := func(code string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ctx.Err() == nil && m.attempt == att && att.state == AttemptPending {
			m.endAttemptLocked(att, AttemptFailed, code)
		}
	}
	login, userID, err := m.lookupUser(ctx, tokens.access)
	if err != nil {
		end(Code(err))
		return
	}
	now := m.now()
	rec := &record{
		Version: recordVersion, ID: randomID(24), AccessToken: tokens.access, RefreshToken: tokens.refresh,
		Login: login, UserID: userID, ConnectedBy: att.actor, ConnectedAt: now, Repositories: []string{},
	}
	if tokens.lifetime > 0 {
		rec.AccessExpiresAt = now.Add(tokens.lifetime)
	}
	if tokens.refresh != "" && tokens.refreshLifetime > 0 {
		rec.RefreshExpiresAt = now.Add(tokens.refreshLifetime)
	}
	// Best effort: a failure here leaves no_repositories until the next Sync.
	if list, truncated, err := m.listRepositories(ctx, tokens.access); err == nil {
		rec.Repositories, rec.RepositoriesTruncated, rec.RepositoriesCheckedAt = list, truncated, now
	}
	m.mu.Lock()
	if ctx.Err() != nil || m.attempt != att || att.state != AttemptPending {
		m.mu.Unlock()
		return
	}
	if err := m.saveLocked(rec); err != nil {
		m.endAttemptLocked(att, AttemptFailed, CodeStorageFailed)
		m.mu.Unlock()
		return
	}
	m.rec, m.broken, m.rejected, m.unsaved, m.lastRefresh = rec, false, false, false, time.Time{}
	m.gen++
	m.attempt = nil
	m.mu.Unlock()
	m.logf(`{"level":"info","msg":"github connected"}`)
	m.publish()
}

func (m *Manager) lookupUser(ctx context.Context, token string) (string, int64, error) {
	r, err := m.getAPI(ctx, "/user", token, maxBody)
	if err != nil {
		return "", 0, err
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if r.status != http.StatusOK || json.Unmarshal(r.body, &user) != nil || !loginPattern.MatchString(user.Login) || user.ID < 1 {
		return "", 0, fail(CodeGitHubUnreachable)
	}
	return user.Login, user.ID, nil
}

type tokenSet struct {
	access, refresh           string
	lifetime, refreshLifetime time.Duration
}

// parseTokenReply validates a token reply. An App without token expiry
// sends neither expires_in nor refresh_token: lifetime 0 means no expiry.
func parseTokenReply(body []byte) (tokenSet, bool) {
	var raw struct {
		Error                 string      `json:"error"`
		AccessToken           string      `json:"access_token"`
		TokenType             string      `json:"token_type"`
		ExpiresIn             json.Number `json:"expires_in"`
		RefreshToken          string      `json:"refresh_token"`
		RefreshTokenExpiresIn json.Number `json:"refresh_token_expires_in"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Error != "" || !strings.EqualFold(raw.TokenType, "bearer") || !validToken(raw.AccessToken) {
		return tokenSet{}, false
	}
	if raw.RefreshToken != "" && !validToken(raw.RefreshToken) {
		return tokenSet{}, false
	}
	set := tokenSet{access: raw.AccessToken, refresh: raw.RefreshToken}
	if raw.ExpiresIn != "" || raw.RefreshToken != "" {
		lifetime, ok := seconds(raw.ExpiresIn, minTokenLifetime, maxTokenLifetime)
		if !ok {
			return tokenSet{}, false
		}
		set.lifetime = lifetime
	}
	if raw.RefreshTokenExpiresIn != "" {
		lifetime, ok := seconds(raw.RefreshTokenExpiresIn, minTokenLifetime, maxRefreshLife)
		if !ok {
			return tokenSet{}, false
		}
		set.refreshLifetime = lifetime
	}
	return set, true
}
