package publicendpoint

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// RequiredPermissions lists the Cloudflare API token permissions an
// administrator must grant. The token is used only during setup/teardown.
var RequiredPermissions = []string{
	"Account › Cloudflare Tunnel › Edit",
	"Zone › Zone › Read (for the hostname's zone)",
	"Zone › DNS › Edit (for the hostname's zone)",
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?){2,}$`)

// ErrInvalidHostname rejects names that are not a subdomain of a zone.
var ErrInvalidHostname = errors.New("hostname must be a subdomain such as hooks.example.com")

// Config pins where the manager talks to Cloudflare and what it exposes.
type Config struct {
	APIBase string
	Binary  string
	// HookService is the loopback URL of Helm's hooks-only listener.
	HookService string
	// TokenDir holds owner-only tunnel run token files.
	TokenDir string
	// ProbeOrigin, when set, receives the self-test request with the public
	// Host header instead of resolving the hostname (tests only).
	ProbeOrigin string
}

// Manager provisions, runs and tears down the single public endpoint.
type Manager struct {
	cfg    Config
	store  *store.Store
	runner *runner
	mu     sync.Mutex
	probes probeRegistry
	// lastTest is the most recent self-test of the active endpoint.
	testMu   sync.Mutex
	lastTest *TestResult
}

// TestResult reports one round trip from Helm, through the public hostname,
// back to Helm's hooks listener.
type TestResult struct {
	EndpointID    string `json:"endpoint_id"`
	OK            bool   `json:"ok"`
	CheckedAt     string `json:"checked_at"`
	LatencyMS     int64  `json:"latency_ms"`
	StatusCode    int    `json:"status_code,omitempty"`
	ViaCloudflare bool   `json:"via_cloudflare"`
	CFRay         string `json:"cf_ray,omitempty"`
	Message       string `json:"message"`
}

// ErrNoActiveEndpoint means there is nothing to test.
var ErrNoActiveEndpoint = errors.New("no public URL is active")

// ProbePath is the hooks-listener prefix that answers self-test nonces. It
// sits under the exposed ticket-hook path, so it travels the same route as
// real webhooks.
const ProbePath = "/api/v1/hooks/tickets/probe/"

type probeRegistry struct {
	mu      sync.Mutex
	pending map[string]probeEntry
}

// probeEntry is one pending self-test. A non-empty webhookID makes it a
// test-ticket probe for that webhook; otherwise it is a reachability probe.
type probeEntry struct {
	expires   time.Time
	webhookID string
}

func (p *probeRegistry) add(nonce string, ttl time.Duration, webhookID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]probeEntry{}
	}
	now := time.Now()
	for key, entry := range p.pending {
		if now.After(entry.expires) {
			delete(p.pending, key)
		}
	}
	p.pending[nonce] = probeEntry{expires: now.Add(ttl), webhookID: webhookID}
}

// take consumes a nonce of the requested kind once and returns its webhook.
// Unknown, expired or other-kind nonces are refused (and not consumed).
func (p *probeRegistry) take(nonce string, ticket bool) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.pending[nonce]
	if !ok || (entry.webhookID != "") != ticket {
		return "", false
	}
	delete(p.pending, nonce)
	return entry.webhookID, time.Now().Before(entry.expires)
}

func (p *probeRegistry) drop(nonce string) {
	p.mu.Lock()
	delete(p.pending, nonce)
	p.mu.Unlock()
}

// View is the administrator-facing state.
type View struct {
	Active              *store.PublicEndpoint  `json:"active,omitempty"`
	Connector           RunnerStatus           `json:"connector"`
	PublicHookBase      string                 `json:"public_hook_base,omitempty"`
	History             []store.PublicEndpoint `json:"history"`
	RequiredPermissions []string               `json:"required_permissions"`
	ExposedPaths        string                 `json:"exposed_paths"`
	// ConnectorAvailable reports whether the cloudflared binary can run here.
	ConnectorAvailable bool `json:"connector_available"`
	// LastTest is the latest self-test of the active endpoint, if any.
	LastTest *TestResult `json:"last_test,omitempty"`
}

func NewManager(cfg Config, st *store.Store) *Manager {
	if cfg.APIBase == "" {
		cfg.APIBase = DefaultCloudflareAPI
	}
	if cfg.Binary == "" {
		cfg.Binary = "cloudflared"
	}
	return &Manager{cfg: cfg, store: st, runner: newRunner(cfg.Binary)}
}

func (m *Manager) tokenPath(id string) string {
	return filepath.Join(m.cfg.TokenDir, id+".token")
}

// Resume restarts the connector for an endpoint that was active at shutdown.
func (m *Manager) Resume(ctx context.Context) error {
	endpoint, ok, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil || !ok {
		return err
	}
	token, err := os.ReadFile(m.tokenPath(endpoint.ID))
	if err != nil {
		return fmt.Errorf("public endpoint token file unavailable: %w", err)
	}
	m.runner.Start(strings.TrimSpace(string(token)))
	return nil
}

// Shutdown stops the connector.
func (m *Manager) Shutdown() { m.runner.Stop() }

// PublicHookBase is the public URL prefix for ticket webhooks, or "".
func (m *Manager) PublicHookBase(ctx context.Context) string {
	if m == nil {
		return ""
	}
	endpoint, ok, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil || !ok {
		return ""
	}
	return "https://" + endpoint.Hostname + "/api/v1/hooks/tickets/"
}

func (m *Manager) View(ctx context.Context) (View, error) {
	history, err := m.store.ListPublicEndpoints(ctx)
	if err != nil {
		return View{}, err
	}
	view := View{History: history, Connector: m.runner.Status(), RequiredPermissions: RequiredPermissions, ExposedPaths: HookPathPattern, ConnectorAvailable: m.connectorAvailable()}
	for index := range history {
		if history[index].Status == "active" {
			active := history[index]
			view.Active = &active
			view.PublicHookBase = "https://" + active.Hostname + "/api/v1/hooks/tickets/"
		}
	}
	if view.Active != nil {
		m.testMu.Lock()
		if m.lastTest != nil && m.lastTest.EndpointID == view.Active.ID {
			result := *m.lastTest
			view.LastTest = &result
		}
		m.testMu.Unlock()
	}
	return view, nil
}

// AnswerProbe reports whether nonce is a pending reachability self-test,
// consuming it.
func (m *Manager) AnswerProbe(nonce string) bool {
	if m == nil || len(nonce) != 32 {
		return false
	}
	_, ok := m.probes.take(nonce, false)
	return ok
}

// TakeTicketProbe consumes a pending test-ticket nonce and returns the
// webhook the test ticket belongs to.
func (m *Manager) TakeTicketProbe(nonce string) (string, bool) {
	if m == nil || len(nonce) != 32 {
		return "", false
	}
	return m.probes.take(nonce, true)
}

// probeResponse is one request from Helm to its own public hostname.
type probeResponse struct {
	status        int
	body          []byte
	cfRay         string
	viaCloudflare bool
	latencyMS     int64
}

// roundTrip sends method to the public hostname's probe path for nonce.
func (m *Manager) roundTrip(ctx context.Context, hostname, method, nonce string, body []byte, contentType string) (probeResponse, error) {
	target := "https://" + hostname + ProbePath + nonce
	if m.cfg.ProbeOrigin != "" {
		target = m.cfg.ProbeOrigin + ProbePath + nonce
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return probeResponse{}, err
	}
	request.Host = hostname
	request.Header.Set("User-Agent", "Helm public URL self-test")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := time.Now()
	response, err := client.Do(request)
	result := probeResponse{latencyMS: time.Since(started).Milliseconds()}
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.body, _ = io.ReadAll(io.LimitReader(response.Body, 8192))
	result.status = response.StatusCode
	result.cfRay = response.Header.Get("Cf-Ray")
	result.viaCloudflare = result.cfRay != "" || strings.EqualFold(response.Header.Get("Server"), "cloudflare")
	return result, nil
}

// failureMessage explains a non-success probe status.
func failureMessage(status int) string {
	switch {
	case status == 530 || status == 502 || status == 503:
		return fmt.Sprintf("Cloudflare answered HTTP %d: it cannot reach the connector. Check that the connector is connected.", status)
	case status == http.StatusNotFound:
		return "HTTP 404: the request did not reach this Helm's hooks listener. The tunnel's routing may have been changed in Cloudflare."
	default:
		return fmt.Sprintf("Unexpected HTTP %d from the public URL.", status)
	}
}

// Test sends one request from Helm to its own public hostname and expects
// the hooks listener to echo a one-time nonce back. Success proves DNS,
// Cloudflare's edge, the tunnel and the ingress rules all reach this Helm.
func (m *Manager) Test(ctx context.Context, id string) (TestResult, error) {
	endpoint, active, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil {
		return TestResult{}, err
	}
	if !active || endpoint.ID != id {
		return TestResult{}, ErrNoActiveEndpoint
	}
	nonce := randomID()
	m.probes.add(nonce, 30*time.Second, "")
	defer m.probes.drop(nonce)
	result := TestResult{EndpointID: endpoint.ID, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	response, err := m.roundTrip(ctx, endpoint.Hostname, http.MethodGet, nonce, nil, "")
	result.LatencyMS = response.latencyMS
	if err != nil {
		result.Message = probeTransportMessage(endpoint.Hostname, err)
		return m.recordTest(result), nil
	}
	result.StatusCode, result.CFRay, result.ViaCloudflare = response.status, response.cfRay, response.viaCloudflare
	if response.status == http.StatusOK && strings.Contains(string(response.body), nonce) {
		result.OK, result.Message = true, "Reached this Helm through the public URL."
	} else {
		result.Message = failureMessage(response.status)
	}
	return m.recordTest(result), nil
}

// TestTicketDedupeKey keeps repeated test tickets on one open ticket.
const TestTicketDedupeKey = "helm-public-url-test"

// TicketTestResult reports a test ticket sent through the public URL.
type TicketTestResult struct {
	OK              bool   `json:"ok"`
	Hostname        string `json:"hostname"`
	CheckedAt       string `json:"checked_at"`
	LatencyMS       int64  `json:"latency_ms"`
	StatusCode      int    `json:"status_code,omitempty"`
	ViaCloudflare   bool   `json:"via_cloudflare"`
	CFRay           string `json:"cf_ray,omitempty"`
	Disposition     string `json:"disposition,omitempty"`
	TicketKey       string `json:"ticket_key,omitempty"`
	TicketURL       string `json:"ticket_url,omitempty"`
	OccurrenceCount int    `json:"occurrence_count,omitempty"`
	Message         string `json:"message"`
}

// SendTestTicket posts a generic test ticket for webhookID from Helm to its
// own public hostname. The hooks listener files it under that webhook with
// the same code real deliveries use, so success proves an outside app can
// open tickets through Cloudflare, the tunnel and the ingress rules.
func (m *Manager) SendTestTicket(ctx context.Context, webhookID string) (TicketTestResult, error) {
	endpoint, active, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil {
		return TicketTestResult{}, err
	}
	if !active {
		return TicketTestResult{}, ErrNoActiveEndpoint
	}
	payload, err := json.Marshal(map[string]any{
		"title":       "Test ticket via public URL",
		"description": "Helm sent this through https://" + endpoint.Hostname + " to check that outside apps can open tickets with this webhook. Repeated tests count on this ticket; complete it when you are done.",
		"priority":    "low",
		"dedupe_key":  TestTicketDedupeKey,
		"source":      "helm-test",
		"fields":      map[string]string{"public_url": "https://" + endpoint.Hostname},
	})
	if err != nil {
		return TicketTestResult{}, err
	}
	nonce := randomID()
	m.probes.add(nonce, 30*time.Second, webhookID)
	defer m.probes.drop(nonce)
	result := TicketTestResult{Hostname: endpoint.Hostname, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	response, err := m.roundTrip(ctx, endpoint.Hostname, http.MethodPost, nonce, payload, "application/json")
	result.LatencyMS = response.latencyMS
	if err != nil {
		result.Message = probeTransportMessage(endpoint.Hostname, err)
		return result, nil
	}
	result.StatusCode, result.CFRay, result.ViaCloudflare = response.status, response.cfRay, response.viaCloudflare
	var hook struct {
		Disposition     string `json:"disposition"`
		OccurrenceCount int    `json:"occurrence_count"`
		Ticket          struct {
			Key string `json:"key"`
			URL string `json:"url"`
		} `json:"ticket"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(response.body, &hook)
	switch {
	case (response.status == http.StatusCreated || response.status == http.StatusOK) && hook.Ticket.Key != "":
		result.OK, result.Disposition, result.OccurrenceCount = true, hook.Disposition, hook.OccurrenceCount
		result.TicketKey, result.TicketURL = hook.Ticket.Key, hook.Ticket.URL
		result.Message = "Opened " + hook.Ticket.Key + " through the public URL."
		if hook.Disposition == "repeated" {
			result.Message = fmt.Sprintf("Counted repeat %d on %s through the public URL.", hook.OccurrenceCount, hook.Ticket.Key)
		}
	case response.status == http.StatusServiceUnavailable && hook.Error.Message != "":
		result.Message = "The request reached Helm, but the webhook cannot file tickets: " + hook.Error.Message
	default:
		result.Message = failureMessage(response.status)
	}
	return result, nil
}

func (m *Manager) recordTest(result TestResult) TestResult {
	m.testMu.Lock()
	stored := result
	m.lastTest = &stored
	m.testMu.Unlock()
	return result
}

func probeTransportMessage(hostname string, err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "no such host"):
		return "DNS for " + hostname + " does not resolve yet. New records can take a minute to appear."
	case strings.Contains(text, "Client.Timeout") || strings.Contains(text, "deadline exceeded"):
		return "The public URL did not answer within 15 seconds."
	case strings.Contains(text, "certificate"):
		return "TLS failed for " + hostname + ". Cloudflare may still be issuing the certificate."
	default:
		return "The public URL could not be reached from this server."
	}
}

// Provision creates the tunnel, ingress, DNS record and token file, then
// starts the connector. Any failure removes what was already created.
func (m *Manager) Provision(ctx context.Context, apiToken, hostname, actorID string) (store.PublicEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if !hostnamePattern.MatchString(hostname) || len(hostname) > 253 {
		return store.PublicEndpoint{}, ErrInvalidHostname
	}
	if strings.TrimSpace(apiToken) == "" {
		return store.PublicEndpoint{}, fmt.Errorf("%w: a Cloudflare API token is required", ErrCloudflare)
	}
	// Refuse before creating public resources that nothing could serve.
	if !m.connectorAvailable() {
		return store.PublicEndpoint{}, fmt.Errorf("%w: cloudflared is not installed on this server (set HELM_CLOUDFLARED_BINARY)", ErrCloudflare)
	}
	if _, active, err := m.store.ActivePublicEndpoint(ctx); err != nil {
		return store.PublicEndpoint{}, err
	} else if active {
		return store.PublicEndpoint{}, fmt.Errorf("%w: a public endpoint is already active; disable it first", ErrCloudflare)
	}
	client := newCloudflareClient(m.cfg.APIBase, strings.TrimSpace(apiToken))
	zone, err := client.findZone(ctx, hostname)
	if err != nil {
		return store.PublicEndpoint{}, err
	}
	tunnelID, err := client.createTunnel(ctx, zone.Account.ID, "helm-"+hostname)
	if err != nil {
		return store.PublicEndpoint{}, err
	}
	undo := func(recordID string) {
		if recordID != "" {
			_ = client.deleteDNS(context.Background(), zone.ID, recordID)
		}
		_ = client.deleteTunnel(context.Background(), zone.Account.ID, tunnelID)
	}
	if err := client.putIngress(ctx, zone.Account.ID, tunnelID, hostname, m.cfg.HookService); err != nil {
		undo("")
		return store.PublicEndpoint{}, err
	}
	runToken, err := client.tunnelToken(ctx, zone.Account.ID, tunnelID)
	if err != nil {
		undo("")
		return store.PublicEndpoint{}, err
	}
	recordID, err := client.createCNAME(ctx, zone.ID, hostname, tunnelID+".cfargotunnel.com")
	if err != nil {
		undo("")
		return store.PublicEndpoint{}, err
	}
	id := randomID()
	if err := m.writeToken(id, runToken); err != nil {
		undo(recordID)
		return store.PublicEndpoint{}, err
	}
	endpoint, err := m.store.CreatePublicEndpoint(ctx, store.PublicEndpoint{ID: id, Provider: "cloudflare", Hostname: hostname, AccountID: zone.Account.ID, ZoneID: zone.ID, TunnelID: tunnelID, DNSRecordID: recordID}, actorID)
	if err != nil {
		_ = os.Remove(m.tokenPath(id))
		undo(recordID)
		return store.PublicEndpoint{}, err
	}
	m.runner.Start(runToken)
	return endpoint, nil
}

// Disable stops the connector and forgets its token. With an API token it
// also deletes the DNS record and tunnel; otherwise the endpoint is marked
// cleanup_pending so an administrator can finish later.
func (m *Manager) Disable(ctx context.Context, id, apiToken, actorID string) (store.PublicEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, err := m.store.GetPublicEndpoint(ctx, id)
	if err != nil {
		return store.PublicEndpoint{}, err
	}
	apiToken = strings.TrimSpace(apiToken)
	if endpoint.Status == "active" {
		if _, emailActive, err := m.store.ActiveEmailIntake(ctx); err != nil {
			return store.PublicEndpoint{}, err
		} else if emailActive {
			return store.PublicEndpoint{}, fmt.Errorf("%w: email addresses deliver mail through this Public URL; remove them first", ErrEmailPrerequisite)
		}
	}
	// A removed endpoint only changes when a token finishes its cleanup;
	// otherwise a tokenless retry would mark finished cleanup as pending.
	if endpoint.Status != "active" && (!endpoint.CleanupPending || apiToken == "") {
		return endpoint, nil
	}
	if endpoint.Status == "active" {
		m.runner.Stop()
	}
	_ = os.Remove(m.tokenPath(id))
	var cleanupErr error
	if apiToken != "" {
		client := newCloudflareClient(m.cfg.APIBase, apiToken)
		if err := client.deleteDNS(ctx, endpoint.ZoneID, endpoint.DNSRecordID); err != nil && !strings.Contains(err.Error(), "(code 81044)") {
			cleanupErr = err
		}
		if err := client.deleteTunnel(ctx, endpoint.AccountID, endpoint.TunnelID); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	updated, err := m.store.DisablePublicEndpoint(ctx, id, actorID, apiToken == "" || cleanupErr != nil)
	if err != nil {
		return store.PublicEndpoint{}, err
	}
	return updated, cleanupErr
}

func (m *Manager) connectorAvailable() bool {
	_, err := exec.LookPath(m.cfg.Binary)
	return err == nil
}

func (m *Manager) writeToken(id, token string) error {
	if err := os.MkdirAll(m.cfg.TokenDir, 0o700); err != nil {
		return err
	}
	temporary := m.tokenPath(id) + ".tmp"
	if err := os.WriteFile(temporary, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, m.tokenPath(id))
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	return hex.EncodeToString(buf)
}
