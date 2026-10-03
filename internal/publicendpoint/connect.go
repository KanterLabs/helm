package publicendpoint

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Guided setup ("Connect Cloudflare"): an administrator either signs in
// with Cloudflare (OAuth 2.0 authorization code + PKCE through the relay)
// or pastes a token made from TokenLink. Either way Helm holds the
// credential in memory only, as a short session bound to that
// administrator, and forgets (and for OAuth revokes) it after setup.
// See docs/CLOUDFLARE_CONNECT_PLAN.md and docs/PUBLIC_ACCESS.md.

// OAuthScopes are requested at sign-in. The email scopes are optional on
// the consent screen, so a user may decline email.
var OAuthScopes = []string{"zone.read", "dns.write", "argotunnel.write", "workers-scripts.write", "email-routing-rule.write", "zone-settings.write", "email-routing-address.read"}

// TokenPermission is one entry of the pre-filled "Create token" link.
type TokenPermission struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

// TokenPermissions is everything public URLs and email addresses need; it
// must match RequiredPermissions and EmailPermissions (pinned by a test).
var TokenPermissions = []TokenPermission{
	{Key: "zone", Type: "read", Label: "Zone › Zone › Read"},
	{Key: "dns", Type: "edit", Label: "Zone › DNS › Edit"},
	{Key: "zone_settings", Type: "edit", Label: "Zone › Zone Settings › Edit"},
	{Key: "email_routing_rule", Type: "edit", Label: "Zone › Email Routing Rules › Edit"},
	{Key: "argotunnel", Type: "edit", Label: "Account › Cloudflare Tunnel › Edit"},
	{Key: "workers_scripts", Type: "edit", Label: "Account › Workers Scripts › Edit"},
	{Key: "email_routing_address", Type: "read", Label: "Account › Email Routing Addresses › Read"},
}

// TokenLink opens Cloudflare's account-token page with every permission
// pre-selected. The account and zone are chosen on that page.
func TokenLink() string {
	keys := make([]map[string]string, 0, len(TokenPermissions))
	for _, permission := range TokenPermissions {
		keys = append(keys, map[string]string{"key": permission.Key, "type": permission.Type})
	}
	encoded, _ := json.Marshal(keys)
	return "https://dash.cloudflare.com/?to=/:account/api-tokens&permissionGroupKeys=" + url.QueryEscape(string(encoded)) + "&name=" + url.QueryEscape("Helm public access")
}

// ErrNotConnected means the administrator has no live Cloudflare session.
var ErrNotConnected = errors.New("connect Cloudflare first")

// ErrOAuthUnavailable means sign-in is turned off or not configured.
var ErrOAuthUnavailable = errors.New("sign in with Cloudflare is not available on this server")

const (
	sessionLifetime = 30 * time.Minute
	pendingLifetime = 10 * time.Minute
)

type cloudflareSession struct {
	token   string
	oauth   bool
	expires time.Time
}

type pendingSignIn struct {
	verifier string
	actorID  string
	expires  time.Time
}

type connectState struct {
	mu       sync.Mutex
	sessions map[string]cloudflareSession
	pending  map[string]pendingSignIn
	runs     map[string]*SetupRun
}

// ConnectStatus is what an administrator's setup panel shows.
type ConnectStatus struct {
	OAuthAvailable   bool              `json:"oauth_available"`
	TokenLink        string            `json:"token_link"`
	TokenPermissions []TokenPermission `json:"token_permissions"`
	Connected        bool              `json:"connected"`
	ConnectedVia     string            `json:"connected_via,omitempty"`
	ExpiresAt        string            `json:"expires_at,omitempty"`
	Run              *SetupRun         `json:"run,omitempty"`
	// ActivePublicHostname is the public URL setup will keep, if any.
	ActivePublicHostname string `json:"active_public_hostname,omitempty"`
}

// OAuthAvailable reports whether "Sign in with Cloudflare" can be offered.
func (m *Manager) OAuthAvailable() bool {
	return m.cfg.OAuthClientID != "" && m.cfg.OAuthRelayURL != "" && m.cfg.PublicOrigin != ""
}

func (m *Manager) sessionToken(actorID string) (cloudflareSession, bool) {
	m.connect.mu.Lock()
	defer m.connect.mu.Unlock()
	session, ok := m.connect.sessions[actorID]
	if !ok || time.Now().After(session.expires) {
		delete(m.connect.sessions, actorID)
		return cloudflareSession{}, false
	}
	return session, true
}

// ConnectStatus reports the administrator's session and latest setup run.
func (m *Manager) ConnectStatus(actorID string) ConnectStatus {
	status := ConnectStatus{OAuthAvailable: m.OAuthAvailable(), TokenLink: TokenLink(), TokenPermissions: TokenPermissions}
	if session, ok := m.sessionToken(actorID); ok {
		status.Connected, status.ExpiresAt = true, session.expires.UTC().Format(time.RFC3339)
		status.ConnectedVia = "token"
		if session.oauth {
			status.ConnectedVia = "cloudflare"
		}
	}
	status.Run = m.latestRun(actorID)
	if endpoint, active, err := m.store.ActivePublicEndpoint(context.Background()); err == nil && active {
		status.ActivePublicHostname = endpoint.Hostname
	}
	return status
}

func (m *Manager) storeSession(actorID string, session cloudflareSession) {
	m.connect.mu.Lock()
	defer m.connect.mu.Unlock()
	if m.connect.sessions == nil {
		m.connect.sessions = map[string]cloudflareSession{}
	}
	m.connect.sessions[actorID] = session
}

// ConnectToken validates a pasted token by listing its zones, then keeps it
// as the administrator's session.
func (m *Manager) ConnectToken(ctx context.Context, actorID, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("%w: paste the token Cloudflare showed after you clicked Create", ErrCloudflare)
	}
	if _, err := listZones(ctx, newCloudflareClient(m.cfg.APIBase, token)); err != nil {
		return err
	}
	m.storeSession(actorID, cloudflareSession{token: token, expires: time.Now().Add(sessionLifetime)})
	return nil
}

// Disconnect forgets the administrator's Cloudflare session, revoking an
// OAuth token so it cannot be used again.
func (m *Manager) Disconnect(ctx context.Context, actorID string) {
	m.connect.mu.Lock()
	session, ok := m.connect.sessions[actorID]
	delete(m.connect.sessions, actorID)
	m.connect.mu.Unlock()
	if ok && session.oauth {
		m.revoke(ctx, session.token)
	}
}

func randomURLToken(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// StartOAuth returns Cloudflare's consent URL. The state is a one-time
// nonce plus this Helm's origin (which the relay sends the browser back
// to); the PKCE verifier never leaves this server.
func (m *Manager) StartOAuth(actorID string) (string, error) {
	if !m.OAuthAvailable() {
		return "", ErrOAuthUnavailable
	}
	verifier := randomURLToken(48)
	challenge := sha256.Sum256([]byte(verifier))
	nonce := randomID()
	m.connect.mu.Lock()
	if m.connect.pending == nil {
		m.connect.pending = map[string]pendingSignIn{}
	}
	now := time.Now()
	for key, item := range m.connect.pending {
		if now.After(item.expires) {
			delete(m.connect.pending, key)
		}
	}
	m.connect.pending[nonce] = pendingSignIn{verifier: verifier, actorID: actorID, expires: now.Add(pendingLifetime)}
	m.connect.mu.Unlock()
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {m.cfg.OAuthClientID},
		"redirect_uri":          {m.cfg.OAuthRelayURL},
		"scope":                 {strings.Join(OAuthScopes, " ")},
		"state":                 {nonce + "." + base64.RawURLEncoding.EncodeToString([]byte(m.cfg.PublicOrigin))},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	return m.cfg.DashboardURL + "/oauth2/auth?" + query.Encode(), nil
}

// FinishOAuth redeems the authorization code for the administrator who
// started sign-in. Unknown, reused, expired or foreign states are refused.
func (m *Manager) FinishOAuth(ctx context.Context, actorID, state, code string) error {
	nonce, _, _ := strings.Cut(state, ".")
	m.connect.mu.Lock()
	pending, ok := m.connect.pending[nonce]
	delete(m.connect.pending, nonce)
	m.connect.mu.Unlock()
	if !ok || time.Now().After(pending.expires) || pending.actorID != actorID {
		return fmt.Errorf("%w: this sign-in link expired or was started by someone else; start again", ErrCloudflare)
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("%w: Cloudflare returned no authorization code", ErrCloudflare)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {m.cfg.OAuthRelayURL},
		"client_id":     {m.cfg.OAuthClientID},
		"code_verifier": {pending.verifier},
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := m.postForm(ctx, "/oauth2/token", form, &token); err != nil {
		return err
	}
	if token.AccessToken == "" {
		message := token.Description
		if message == "" {
			message = token.Error
		}
		return fmt.Errorf("%w: Cloudflare did not issue a token (%s)", ErrCloudflare, message)
	}
	lifetime := sessionLifetime
	if token.ExpiresIn > 0 && time.Duration(token.ExpiresIn)*time.Second < lifetime {
		lifetime = time.Duration(token.ExpiresIn) * time.Second
	}
	m.storeSession(actorID, cloudflareSession{token: token.AccessToken, oauth: true, expires: time.Now().Add(lifetime)})
	return nil
}

func (m *Manager) revoke(ctx context.Context, token string) {
	_ = m.postForm(ctx, "/oauth2/revoke", url.Values{"token": {token}, "client_id": {m.cfg.OAuthClientID}}, nil)
}

func (m *Manager) postForm(ctx context.Context, path string, form url.Values, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.DashboardURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("%w: Cloudflare sign-in is unreachable", ErrCloudflare)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if result != nil {
		_ = json.Unmarshal(payload, result)
	}
	if response.StatusCode >= 500 {
		return fmt.Errorf("%w: Cloudflare sign-in failed (HTTP %d)", ErrCloudflare, response.StatusCode)
	}
	return nil
}

// ZoneOption is a domain the connected credential can set up.
type ZoneOption struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	AccountName       string `json:"account_name,omitempty"`
	EmailRouting      string `json:"email_routing"` // ready, off or unknown
	PlusAddressing    bool   `json:"plus_addressing"`
	SuggestedHostname string `json:"suggested_hostname,omitempty"`
	// Usable is false when the credential cannot manage DNS in the zone
	// (Cloudflare may still list it); Reason says why.
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}

func listZones(ctx context.Context, client *cloudflareClient) ([]cfZone, error) {
	var zones []cfZone
	if err := client.call(ctx, http.MethodGet, "/zones?per_page=50&status=active", nil, &zones); err != nil {
		return nil, friendly(err, "list your domains")
	}
	if len(zones) == 0 {
		return nil, fmt.Errorf("%w: The credential is not allowed to see any domains. On Cloudflare's token page, under Zone resources, include your domain", ErrCloudflare)
	}
	return zones, nil
}

// suggestHostname picks the first unused name for the public URL. An error
// means the credential cannot read DNS in the zone.
func suggestHostname(ctx context.Context, client *cloudflareClient, zone cfZone) (string, error) {
	candidates := []string{"hooks." + zone.Name, "helm-hooks." + zone.Name}
	for index := 0; index < 3; index++ {
		candidates = append(candidates, "hooks-"+randomID()[:6]+"."+zone.Name)
	}
	for _, candidate := range candidates {
		var existing []cfRecord
		if err := client.call(ctx, http.MethodGet, "/zones/"+zone.ID+"/dns_records?name="+url.QueryEscape(candidate), nil, &existing); err != nil {
			return "", err
		}
		if len(existing) == 0 {
			return candidate, nil
		}
	}
	return "", nil
}

// CloudflareZones lists the domains the session can configure, with Email
// Routing status and a free hostname for each.
func (m *Manager) CloudflareZones(ctx context.Context, actorID string) ([]ZoneOption, error) {
	session, ok := m.sessionToken(actorID)
	if !ok {
		return nil, ErrNotConnected
	}
	client := newCloudflareClient(m.cfg.APIBase, session.token)
	zones, err := listZones(ctx, client)
	if err != nil {
		return nil, err
	}
	options := make([]ZoneOption, 0, len(zones))
	for index, zone := range zones {
		option := ZoneOption{ID: zone.ID, Name: zone.Name, AccountName: zone.Account.Name, EmailRouting: "unknown", Usable: true}
		if settings, err := client.emailRouting(ctx, zone.ID); err == nil {
			option.EmailRouting, option.PlusAddressing = "off", settings.SupportSubaddress
			if settings.Enabled {
				option.EmailRouting = "ready"
			}
		}
		if index < 20 {
			suggested, err := suggestHostname(ctx, client, zone)
			if err != nil {
				option.Usable, option.Reason = false, "This credential cannot manage DNS here. Include this domain under Zone resources when creating the token."
			}
			option.SuggestedHostname = suggested
		}
		options = append(options, option)
	}
	return options, nil
}

// friendly rewrites a Cloudflare permission failure into the fix.
func friendly(err error, action string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	if strings.Contains(text, "Authentication error") || strings.Contains(text, "(code 10000)") || strings.Contains(text, "(code 9109)") || strings.Contains(text, "Unauthorized") {
		return fmt.Errorf("%w: This credential is not allowed to %s. Create the token with the Create token button (it selects every permission Helm needs), or sign in with Cloudflare and keep the requested permissions.", ErrCloudflare, action)
	}
	return err
}
