package forgeconnect

import "time"

// Check states.
const (
	CheckOK           = "ok"
	CheckUnreachable  = "unreachable"
	CheckUnauthorized = "unauthorized"
)

// CheckResult is the outcome of the last Check, kept in the state file.
type CheckResult struct {
	State string `json:"state"`
	Code  string `json:"code"`
}

// valid accepts the empty result (never checked) and the known outcomes.
func (c CheckResult) valid() bool {
	switch c.State {
	case "":
		return c.Code == ""
	case CheckOK:
		return c.Code == "" || c.Code == CodeStorageFailed
	case CheckUnreachable, CheckUnauthorized:
		return knownCode(c.Code)
	}
	return false
}

// Status is the C5 status document.
type Status struct {
	Available         bool        `json:"available"`
	UnavailableReason string      `json:"unavailable_reason"`
	RedirectURI       string      `json:"redirect_uri"`
	State             string      `json:"state"`
	Server            *Server     `json:"server,omitempty"`
	ClientID          string      `json:"client_id,omitempty"`
	Account           *Account    `json:"account,omitempty"`
	Access            string      `json:"access"`
	RepositoryCount   int         `json:"repository_count"`
	ConnectedAt       string      `json:"connected_at"`
	CheckedAt         string      `json:"checked_at"`
	Check             CheckResult `json:"check"`
	RegisterURL       string      `json:"register_url,omitempty"`
	RevokeURL         string      `json:"revoke_url,omitempty"`
}

// Status reports the connection. It never does network I/O.
func (m *Manager) Status() Status {
	s := Status{Available: m.signInAvailable(), RedirectURI: m.redirect}
	switch {
	case !m.enabled:
		s.UnavailableReason = "disabled"
	case m.origin == "":
		s.UnavailableReason = "public_origin_missing"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.State, s.Check, s.RepositoryCount = m.stateLocked(), m.check, m.repoCount
	if rec := m.rec; rec != nil {
		server, account := rec.Server, rec.Account
		s.Server, s.Account, s.ClientID, s.Access = &server, &account, rec.ClientID, rec.Access
		s.ConnectedAt, s.CheckedAt = formatTime(rec.ConnectedAt), formatTime(rec.CheckedAt)
		s.RegisterURL, s.RevokeURL = RegisterURL(server.BaseURL), RevokeURL(server.BaseURL)
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
