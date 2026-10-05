package publicendpoint

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"

	"github.com/KanterLabs/helm/internal/store"
)

//go:embed email-worker.js
var emailWorkerSource string

// EmailWorkerVersion identifies the embedded Worker source.
var EmailWorkerVersion = func() string {
	sum := sha256.Sum256([]byte(emailWorkerSource))
	return hex.EncodeToString(sum[:6])
}()

// EmailPath is the hooks route the Worker posts to; it sits under the
// ticket-hook ingress, so existing tunnels need no change.
const EmailPath = "/api/v1/hooks/tickets/email/"

// EmailPermissions lists the Cloudflare token permissions email setup and
// removal need.
var EmailPermissions = []string{
	"Account › Workers Scripts › Edit",
	"Zone › Zone › Read (for the email domain's zone)",
	"Zone › Email Routing Rules › Edit",
	"Zone › Zone Settings › Read (Edit to turn on plus addressing)",
	"Account › Email Routing Addresses › Read (only with a fallback address)",
}

// ErrEmailPrerequisite is a setup or removal precondition an administrator
// must satisfy first; its message says how.
var ErrEmailPrerequisite = errors.New("email intake prerequisite")

// ErrSubaddressingConsent means plus addressing is off for the zone and the
// administrator has not agreed to Helm turning it on.
var ErrSubaddressingConsent = errors.New("plus addressing consent required")

var (
	localPartPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,30})$`)
	domainPattern    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// EmailSetup is an administrator's request to turn on email addresses.
type EmailSetup struct {
	Domain              string
	LocalPart           string
	FallbackAddress     string
	EnableSubaddressing bool
	APIToken            string
}

// EmailView is the administrator-facing email intake state.
type EmailView struct {
	Active              *store.EmailIntake   `json:"active,omitempty"`
	History             []store.EmailIntake  `json:"history"`
	Recent              []store.EmailReceipt `json:"recent"`
	RequiredPermissions []string             `json:"required_permissions"`
	// PublicHostname is the active Public URL the Worker posts through.
	PublicHostname string `json:"public_hostname,omitempty"`
	// SuggestedDomain is the Public URL's parent domain.
	SuggestedDomain string `json:"suggested_domain,omitempty"`
	WorkerVersion   string `json:"worker_version"`
	MaxMessageBytes int    `json:"max_message_bytes"`
}

// EmailView reports the active intake, history and recent receipts.
func (m *Manager) EmailView(ctx context.Context) (EmailView, error) {
	history, err := m.store.ListEmailIntakes(ctx)
	if err != nil {
		return EmailView{}, err
	}
	view := EmailView{History: history, Recent: []store.EmailReceipt{}, RequiredPermissions: EmailPermissions, WorkerVersion: EmailWorkerVersion, MaxMessageBytes: 1 << 20}
	for index := range history {
		if history[index].Status == "active" {
			active := history[index]
			view.Active = &active
			if view.Recent, err = m.store.RecentEmailReceipts(ctx, active.ID, 20); err != nil {
				return EmailView{}, err
			}
		}
	}
	if endpoint, ok, err := m.store.ActivePublicEndpoint(ctx); err != nil {
		return EmailView{}, err
	} else if ok {
		view.PublicHostname = endpoint.Hostname
		if dot := strings.IndexByte(endpoint.Hostname, '.'); dot > 0 {
			view.SuggestedDomain = endpoint.Hostname[dot+1:]
		}
	}
	return view, nil
}

func newIntakeSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable")
	}
	return "em_" + base64.RawURLEncoding.EncodeToString(buf)
}

// ProvisionEmail deploys the Worker and routing rule. Any failure removes
// what was created and restores plus addressing.
func (m *Manager) ProvisionEmail(ctx context.Context, setup EmailSetup, actorID string) (store.EmailIntake, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	setup.Domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(setup.Domain)), ".")
	setup.LocalPart = strings.ToLower(strings.TrimSpace(setup.LocalPart))
	setup.FallbackAddress = strings.TrimSpace(setup.FallbackAddress)
	if !domainPattern.MatchString(setup.Domain) || len(setup.Domain) > 253 {
		return store.EmailIntake{}, fmt.Errorf("%w: domain must look like example.com", ErrEmailPrerequisite)
	}
	if !localPartPattern.MatchString(setup.LocalPart) {
		return store.EmailIntake{}, fmt.Errorf("%w: address name must be 1-31 lowercase letters, digits, dots, dashes or underscores (no +)", ErrEmailPrerequisite)
	}
	if setup.FallbackAddress != "" {
		parsed, err := mail.ParseAddress(setup.FallbackAddress)
		if err != nil || parsed.Address != setup.FallbackAddress || strings.EqualFold(parsed.Address[strings.LastIndexByte(parsed.Address, '@')+1:], setup.Domain) && strings.HasPrefix(strings.ToLower(parsed.Address), setup.LocalPart+"+") {
			return store.EmailIntake{}, fmt.Errorf("%w: fallback must be a plain email address outside Helm's own addresses", ErrEmailPrerequisite)
		}
	}
	if strings.TrimSpace(setup.APIToken) == "" {
		return store.EmailIntake{}, fmt.Errorf("%w: a Cloudflare API token is required", ErrCloudflare)
	}
	endpoint, ok, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil {
		return store.EmailIntake{}, err
	}
	if !ok {
		return store.EmailIntake{}, fmt.Errorf("%w: create a Public URL first; the email Worker delivers mail to Helm through it", ErrEmailPrerequisite)
	}
	if _, active, err := m.store.ActiveEmailIntake(ctx); err != nil {
		return store.EmailIntake{}, err
	} else if active {
		return store.EmailIntake{}, fmt.Errorf("%w: email addresses are already set up; remove them first", ErrEmailPrerequisite)
	}
	client := newCloudflareClient(m.cfg.APIBase, strings.TrimSpace(setup.APIToken))
	zone, err := client.findZoneFor(ctx, setup.Domain)
	if err != nil {
		return store.EmailIntake{}, err
	}
	settings, err := client.emailRouting(ctx, zone.ID)
	if err != nil {
		return store.EmailIntake{}, err
	}
	if !settings.Enabled {
		return store.EmailIntake{}, fmt.Errorf("%w: Email Routing is not enabled for %s; turn it on in Cloudflare (Email › Email Routing) first", ErrEmailPrerequisite, zone.Name)
	}
	if !settings.SupportSubaddress && !setup.EnableSubaddressing {
		return store.EmailIntake{}, fmt.Errorf("%w: plus addressing is off for %s. Helm needs it so one rule can serve every webhook (name+tag@%s). Turning it on also lets mail to any you+anything@%s reach you@%s", ErrSubaddressingConsent, zone.Name, setup.Domain, zone.Name, zone.Name)
	}
	if setup.FallbackAddress != "" {
		verified, err := client.verifiedDestination(ctx, zone.Account.ID, setup.FallbackAddress)
		if err != nil {
			return store.EmailIntake{}, err
		}
		if !verified {
			return store.EmailIntake{}, fmt.Errorf("%w: %s is not a verified Email Routing destination; add and verify it in Cloudflare first", ErrEmailPrerequisite, setup.FallbackAddress)
		}
	}
	address := setup.LocalPart + "@" + setup.Domain
	rules, err := client.emailRules(ctx, zone.ID)
	if err != nil {
		return store.EmailIntake{}, err
	}
	for _, rule := range rules {
		for _, matcher := range rule.Matchers {
			if matcher.Type == "literal" && strings.EqualFold(matcher.Value, address) {
				return store.EmailIntake{}, fmt.Errorf("%w: Cloudflare already routes %s; choose another address name", ErrEmailPrerequisite, address)
			}
		}
	}

	id := randomID()
	secret := newIntakeSecret()
	worker := "helm-email-" + id[:8]
	bindings := []workerBinding{{Type: "secret_text", Name: "HELM_INTAKE_URL", Text: "https://" + endpoint.Hostname + EmailPath + secret}}
	if setup.FallbackAddress != "" {
		bindings = append(bindings, workerBinding{Type: "plain_text", Name: "FALLBACK_TO", Text: setup.FallbackAddress})
	}
	enabledSubaddress, ruleID := false, ""
	undo := func() {
		background := context.Background()
		if ruleID != "" {
			_ = client.deleteRule(background, zone.ID, ruleID)
		}
		_ = client.deleteWorker(background, zone.Account.ID, worker)
		if enabledSubaddress {
			_ = client.setSubaddressing(background, zone.ID, false)
		}
	}
	if err := client.uploadWorker(ctx, zone.Account.ID, worker, emailWorkerSource, bindings); err != nil {
		undo()
		return store.EmailIntake{}, err
	}
	if !settings.SupportSubaddress {
		if err := client.setSubaddressing(ctx, zone.ID, true); err != nil {
			undo()
			return store.EmailIntake{}, err
		}
		enabledSubaddress = true
	}
	if ruleID, err = client.createWorkerRule(ctx, zone.ID, address, worker); err != nil {
		undo()
		return store.EmailIntake{}, err
	}
	intake, err := m.store.CreateEmailIntake(ctx, store.EmailIntake{
		ID: id, Provider: "cloudflare", Domain: setup.Domain, LocalPart: setup.LocalPart,
		AccountID: zone.Account.ID, ZoneID: zone.ID, WorkerName: worker, RuleID: ruleID,
		FallbackAddress: setup.FallbackAddress, SubaddressEnabledByHelm: enabledSubaddress, PublicEndpointID: endpoint.ID,
	}, secret, actorID)
	if err != nil {
		undo()
		return store.EmailIntake{}, err
	}
	return intake, nil
}

// DisableEmail stops accepting mail immediately. With an API token it also
// deletes the routing rule and Worker; otherwise the intake is marked for
// cleanup. Plus addressing is never turned back off: other addresses may
// rely on it by now.
func (m *Manager) DisableEmail(ctx context.Context, id, apiToken, actorID string) (store.EmailIntake, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intake, err := m.store.GetEmailIntake(ctx, id)
	if err != nil {
		return store.EmailIntake{}, err
	}
	apiToken = strings.TrimSpace(apiToken)
	if intake.Status != "active" && (!intake.CleanupPending || apiToken == "") {
		return intake, nil
	}
	var cleanupErr error
	if apiToken != "" {
		client := newCloudflareClient(m.cfg.APIBase, apiToken)
		cleanupErr = client.deleteRule(ctx, intake.ZoneID, intake.RuleID)
		if err := client.deleteWorker(ctx, intake.AccountID, intake.WorkerName); err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	updated, err := m.store.DisableEmailIntake(ctx, id, actorID, apiToken == "" || cleanupErr != nil)
	if err != nil {
		return store.EmailIntake{}, err
	}
	return updated, cleanupErr
}
