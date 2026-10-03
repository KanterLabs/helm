package publicendpoint

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Setup step statuses.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepSkipped = "skipped"
	StepFailed  = "failed"
)

// SetupStep is one line of the guided setup checklist.
type SetupStep struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// SetupRun is one guided setup. It never contains a credential.
type SetupRun struct {
	ID          string      `json:"id"`
	Status      string      `json:"status"` // running, done or failed
	Zone        string      `json:"zone"`
	Hostname    string      `json:"hostname"`
	Steps       []SetupStep `json:"steps"`
	PublicURL   string      `json:"public_url,omitempty"`
	EmailBase   string      `json:"email_base,omitempty"`
	StartedAt   string      `json:"started_at"`
	FinishedAt  string      `json:"finished_at,omitempty"`
	actorID     string
	createdURL  string
	startedTime time.Time
}

// SetupEmail requests email addresses as part of guided setup.
type SetupEmail struct {
	LocalPart           string
	FallbackAddress     string
	EnableSubaddressing bool
}

// SetupRequest is what the administrator chose in the guided panel.
type SetupRequest struct {
	ZoneID   string
	Hostname string
	Email    *SetupEmail
}

// ErrSetupRunning means a guided setup is already in progress.
var ErrSetupRunning = errors.New("a guided setup is already running")

// Timing for the connector and reachability steps; tests shorten them.
var (
	setupConnectWait   = 45 * time.Second
	setupReachableWait = 90 * time.Second
	setupPollInterval  = 2 * time.Second
)

func (m *Manager) latestRun(actorID string) *SetupRun {
	m.connect.mu.Lock()
	defer m.connect.mu.Unlock()
	var latest *SetupRun
	for _, run := range m.connect.runs {
		if run.actorID == actorID && (latest == nil || run.startedTime.After(latest.startedTime)) {
			latest = run
		}
	}
	if latest == nil {
		return nil
	}
	copied := latest.snapshot()
	return &copied
}

func (r *SetupRun) snapshot() SetupRun {
	copied := *r
	copied.Steps = append([]SetupStep(nil), r.Steps...)
	return copied
}

// SetupRun returns a run started by actorID.
func (m *Manager) SetupRun(actorID, id string) (SetupRun, bool) {
	m.connect.mu.Lock()
	defer m.connect.mu.Unlock()
	run, ok := m.connect.runs[id]
	if !ok || run.actorID != actorID {
		return SetupRun{}, false
	}
	return run.snapshot(), true
}

func (m *Manager) setStep(run *SetupRun, id, status, detail string) {
	m.connect.mu.Lock()
	defer m.connect.mu.Unlock()
	for index := range run.Steps {
		if run.Steps[index].ID == id {
			run.Steps[index].Status, run.Steps[index].Detail = status, detail
		}
	}
}

// StartSetup checks the request and runs the checklist in the background.
func (m *Manager) StartSetup(ctx context.Context, actorID string, request SetupRequest) (SetupRun, error) {
	session, ok := m.sessionToken(actorID)
	if !ok {
		return SetupRun{}, ErrNotConnected
	}
	zones, err := m.CloudflareZones(ctx, actorID)
	if err != nil {
		return SetupRun{}, err
	}
	var zone *ZoneOption
	for index := range zones {
		if zones[index].ID == request.ZoneID {
			zone = &zones[index]
		}
	}
	if zone == nil {
		return SetupRun{}, fmt.Errorf("%w: choose one of the listed domains", ErrCloudflare)
	}
	request.Hostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(request.Hostname)), ".")
	if request.Hostname == "" {
		request.Hostname = zone.SuggestedHostname
	}
	if !strings.HasSuffix(request.Hostname, "."+zone.Name) || !hostnamePattern.MatchString(request.Hostname) {
		return SetupRun{}, fmt.Errorf("%w (try hooks.%s)", ErrInvalidHostname, zone.Name)
	}
	run := &SetupRun{ID: randomID(), Status: "running", Zone: zone.Name, Hostname: request.Hostname, actorID: actorID, startedTime: time.Now()}
	run.StartedAt = run.startedTime.UTC().Format(time.RFC3339)
	run.Steps = []SetupStep{
		{ID: "public_url", Label: "Create the public URL " + request.Hostname, Status: StepPending},
		{ID: "connector", Label: "Connect the tunnel to Cloudflare", Status: StepPending},
		{ID: "reachable", Label: "Reach Helm from the internet", Status: StepPending},
		{ID: "email", Label: "Give webhooks email addresses", Status: StepPending},
		{ID: "forget", Label: "Forget the Cloudflare credential", Status: StepPending},
	}
	m.connect.mu.Lock()
	if m.connect.runs == nil {
		m.connect.runs = map[string]*SetupRun{}
	}
	for _, existing := range m.connect.runs {
		if existing.Status == "running" {
			m.connect.mu.Unlock()
			return SetupRun{}, ErrSetupRunning
		}
	}
	for key, existing := range m.connect.runs {
		if time.Since(existing.startedTime) > 24*time.Hour {
			delete(m.connect.runs, key)
		}
	}
	m.connect.runs[run.ID] = run
	snapshot := run.snapshot()
	m.connect.mu.Unlock()
	go m.executeSetup(run, session.token, *zone, request)
	return snapshot, nil
}

func (m *Manager) finishRun(run *SetupRun, status string) {
	m.connect.mu.Lock()
	run.Status, run.FinishedAt = status, time.Now().UTC().Format(time.RFC3339)
	m.connect.mu.Unlock()
}

func (m *Manager) executeSetup(run *SetupRun, token string, zone ZoneOption, request SetupRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	failed := false
	defer func() {
		m.setStep(run, "forget", StepRunning, "")
		m.Disconnect(ctx, run.actorID)
		m.setStep(run, "forget", StepDone, "Helm no longer holds the token or sign-in.")
		status := "done"
		if failed {
			status = "failed"
		}
		m.finishRun(run, status)
	}()
	skipRest := func(from string) {
		skipping := false
		for _, step := range run.Steps {
			if step.ID == from {
				skipping = true
			}
			if skipping && step.ID != "forget" && step.Status == StepPending {
				m.setStep(run, step.ID, StepSkipped, "Skipped because an earlier step failed.")
			}
		}
	}

	// 1. Public URL: reuse an active one, otherwise create it.
	m.setStep(run, "public_url", StepRunning, "")
	endpoint, active, err := m.store.ActivePublicEndpoint(ctx)
	if err != nil {
		m.setStep(run, "public_url", StepFailed, err.Error())
		failed = true
		skipRest("connector")
		return
	}
	if active {
		m.setStep(run, "public_url", StepDone, "Already set up: https://"+endpoint.Hostname+" (kept as is).")
	} else {
		endpoint, err = m.Provision(ctx, token, request.Hostname, run.actorID)
		if err != nil {
			m.setStep(run, "public_url", StepFailed, cleanMessage(friendly(err, "create a tunnel and DNS record")))
			failed = true
			skipRest("connector")
			return
		}
		run.createdURL = endpoint.ID
		m.setStep(run, "public_url", StepDone, "Created https://"+endpoint.Hostname+": a Cloudflare Tunnel and DNS record that expose only webhook paths.")
	}
	m.connect.mu.Lock()
	run.PublicURL, run.Hostname = "https://"+endpoint.Hostname, endpoint.Hostname
	m.connect.mu.Unlock()
	rollback := func(step, message string) {
		failed = true
		if run.createdURL != "" {
			if _, err := m.Disable(ctx, run.createdURL, token, run.actorID); err == nil {
				message += " Helm removed the public URL it had just created; nothing is left in Cloudflare."
			} else {
				message += " Helm stopped the public URL; see Earlier public URLs to finish cleanup."
			}
			m.connect.mu.Lock()
			run.PublicURL = ""
			m.connect.mu.Unlock()
		}
		m.setStep(run, step, StepFailed, message)
	}

	// 2. Connector registers with Cloudflare's edge.
	m.setStep(run, "connector", StepRunning, "")
	deadline := time.Now().Add(setupConnectWait)
	for {
		status := m.runner.Status()
		if status.State == "connected" {
			detail := fmt.Sprintf("%d edge connection", status.Connections)
			if status.Connections != 1 {
				detail += "s"
			}
			if len(status.Locations) > 0 {
				detail += " in " + strings.Join(status.Locations, ", ")
			}
			m.setStep(run, "connector", StepDone, detail+".")
			break
		}
		if time.Now().After(deadline) {
			reason := "The tunnel did not connect within 45 seconds. This server needs outbound access to Cloudflare on port 7844."
			if status.Message != "" {
				reason += " Last connector message: " + status.Message
			}
			rollback("connector", reason)
			skipRest("reachable")
			return
		}
		time.Sleep(setupPollInterval)
	}

	// 3. Round trip from Helm through the public hostname (DNS can take a
	// minute to appear, so keep trying).
	m.setStep(run, "reachable", StepRunning, "Waiting for DNS and Cloudflare…")
	deadline = time.Now().Add(setupReachableWait)
	for {
		result, err := m.Test(ctx, endpoint.ID)
		if err == nil && result.OK {
			detail := fmt.Sprintf("Round trip in %d ms", result.LatencyMS)
			if result.LatencyMS < 1 {
				detail = "Round trip in under 1 ms"
			}
			if result.ViaCloudflare {
				detail += " through Cloudflare"
			}
			m.setStep(run, "reachable", StepDone, detail+".")
			break
		}
		if time.Now().After(deadline) {
			message := "The public URL did not answer."
			if err == nil {
				message = result.Message
			}
			rollback("reachable", message)
			skipRest("email")
			return
		}
		time.Sleep(setupPollInterval)
	}

	// 4. Email addresses, when asked for.
	switch intake, emailActive, err := m.store.ActiveEmailIntake(ctx); {
	case err != nil:
		m.setStep(run, "email", StepFailed, err.Error())
		failed = true
	case request.Email == nil:
		m.setStep(run, "email", StepSkipped, "Not requested. Run setup again any time to add it.")
	case emailActive:
		m.setStep(run, "email", StepDone, "Already on: "+intake.LocalPart+"+…@"+intake.Domain+".")
		m.connect.mu.Lock()
		run.EmailBase = intake.LocalPart + "+…@" + intake.Domain
		m.connect.mu.Unlock()
	case zone.EmailRouting != "ready":
		m.setStep(run, "email", StepSkipped, "Email Routing is not turned on for "+zone.Name+". Turn it on in Cloudflare (Email › Email Routing), then run setup again.")
	default:
		m.setStep(run, "email", StepRunning, "")
		created, err := m.ProvisionEmail(ctx, EmailSetup{
			Domain: zone.Name, LocalPart: request.Email.LocalPart, FallbackAddress: request.Email.FallbackAddress,
			EnableSubaddressing: request.Email.EnableSubaddressing, APIToken: token,
		}, run.actorID)
		if err != nil {
			failed = true
			m.setStep(run, "email", StepFailed, cleanMessage(friendly(err, "deploy the email Worker and routing rule"))+" The public URL is working; only email was not set up.")
			return
		}
		m.connect.mu.Lock()
		run.EmailBase = created.LocalPart + "+…@" + created.Domain
		m.connect.mu.Unlock()
		m.setStep(run, "email", StepDone, "Every webhook can now get an address like "+created.LocalPart+"+…@"+created.Domain+".")
	}
}

// cleanMessage drops the internal error prefixes from an error.
func cleanMessage(err error) string {
	text := err.Error()
	for _, prefix := range []error{ErrCloudflare, ErrEmailPrerequisite, ErrSubaddressingConsent, ErrInvalidHostname} {
		text = strings.TrimPrefix(text, prefix.Error()+": ")
	}
	return text
}
