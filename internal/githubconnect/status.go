package githubconnect

import "time"

const (
	appsURL           = "https://github.com/apps/"
	authorizationsURL = "https://github.com/settings/apps/authorizations"
)

// Attempt is the C5 attempt object.
type Attempt struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// UserCode is set only for the administrator who began the attempt, and
	// only while it is pending.
	UserCode        string `json:"user_code,omitempty"`
	VerificationURI string `json:"verification_uri"`
	ExpiresAt       string `json:"expires_at"`
	Message         string `json:"message"`
}

// Status is the C5 connection object (web type GithubConnection).
type Status struct {
	Mode                  string   `json:"mode"`
	SigninAvailable       bool     `json:"signin_available"`
	State                 string   `json:"state"`
	Login                 string   `json:"login,omitempty"`
	ConnectedAt           string   `json:"connected_at,omitempty"`
	InstallURL            string   `json:"install_url,omitempty"`
	AuthorizationsURL     string   `json:"authorizations_url,omitempty"`
	RepositoriesTruncated bool     `json:"repositories_truncated"`
	Attempt               *Attempt `json:"attempt,omitempty"`
}

// Status reports the connection as actorID may see it. No network I/O.
func (m *Manager) Status(actorID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(actorID)
}

func (m *Manager) statusLocked(actorID string) Status {
	s := Status{
		Mode: ModeDeviceFlow, SigninAvailable: m.clientID != "" && !m.isClosed(),
		State: m.stateLocked(), AuthorizationsURL: authorizationsURL,
	}
	if m.slug != "" {
		s.InstallURL = appsURL + m.slug + "/installations/new"
	}
	if r := m.rec; r != nil {
		s.Login, s.ConnectedAt, s.RepositoriesTruncated = r.Login, formatTime(r.ConnectedAt), r.RepositoriesTruncated
	}
	if a := m.attempt; a != nil {
		s.Attempt = &Attempt{ID: a.id, State: a.state, VerificationURI: a.verificationURI, ExpiresAt: formatTime(a.expires), Message: attemptMessage(a)}
		if actorID != "" && actorID == a.actor && a.state == AttemptPending {
			s.Attempt.UserCode = a.userCode
		}
	}
	return s
}

// attemptMessage is Helm's own wording; GitHub's text is never shown.
func attemptMessage(a *attempt) string {
	switch a.state {
	case AttemptExpired:
		return "The code expired before it was entered on GitHub. Start again to get a new code."
	case AttemptDenied:
		return "The request was declined on GitHub. Start again if that was a mistake."
	case AttemptFailed:
		switch a.reason {
		case CodeGitHubUnreachable:
			return "Helm could not reach GitHub to finish signing in. Try again in a moment."
		case CodeStorageFailed:
			return "Helm signed in but could not save the connection. Ask the operator to check Helm's GitHub state directory, then try again."
		}
		return "GitHub did not complete the sign-in. Try again; if it keeps failing, ask the operator to check the GitHub App settings."
	}
	return ""
}

// Disconnect deletes the local credential (or an untrusted file in its
// place) and ends any attempt. Helm cannot revoke the grant on GitHub; the
// panel links to authorizations_url for that. Disconnecting twice is fine.
func (m *Manager) Disconnect(actorID string) error {
	m.mu.Lock()
	had := m.rec != nil || m.broken
	err := m.store.remove()
	m.rec, m.broken, m.attempt, m.rejected, m.unsaved, m.lastRefresh = nil, false, nil, false, false, time.Time{}
	m.gen++
	m.mu.Unlock()
	m.signal()
	m.publish()
	if had {
		m.logf(`{"level":"info","msg":"github disconnected"}`)
	}
	return err
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
