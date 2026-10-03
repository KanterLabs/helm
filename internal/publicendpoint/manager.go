package publicendpoint

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

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
}

// Manager provisions, runs and tears down the single public endpoint.
type Manager struct {
	cfg    Config
	store  *store.Store
	runner *runner
	mu     sync.Mutex
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
	return view, nil
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
	if endpoint.Status == "active" {
		m.runner.Stop()
	}
	_ = os.Remove(m.tokenPath(id))
	var cleanupErr error
	apiToken = strings.TrimSpace(apiToken)
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
