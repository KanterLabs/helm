package httpapi

import (
	"context"

	"github.com/KanterLabs/helm/internal/forgeconnect"
)

// giteaStatusBody is the C5 status document as the Settings panel reads it
// (web/src/lib/types/gitea.ts): every key is always present, absent objects
// and times are null, and access and check have their neutral values before
// anything is known.
type giteaStatusBody struct {
	Available         bool                     `json:"available"`
	UnavailableReason string                   `json:"unavailable_reason"`
	RedirectURI       string                   `json:"redirect_uri"`
	State             string                   `json:"state"`
	Server            *forgeconnect.Server     `json:"server"`
	ClientID          string                   `json:"client_id"`
	Account           *forgeconnect.Account    `json:"account"`
	Access            string                   `json:"access"`
	RepositoryCount   int                      `json:"repository_count"`
	ConnectedAt       *string                  `json:"connected_at"`
	CheckedAt         *string                  `json:"checked_at"`
	Check             forgeconnect.CheckResult `json:"check"`
	RegisterURL       string                   `json:"register_url"`
	RevokeURL         string                   `json:"revoke_url"`
}

func nullableTime(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func newGiteaStatusBody(s forgeconnect.Status) giteaStatusBody {
	body := giteaStatusBody{
		Available: s.Available, UnavailableReason: s.UnavailableReason, RedirectURI: s.RedirectURI, State: s.State,
		Server: s.Server, ClientID: s.ClientID, Account: s.Account, Access: s.Access, RepositoryCount: s.RepositoryCount,
		ConnectedAt: nullableTime(s.ConnectedAt), CheckedAt: nullableTime(s.CheckedAt), Check: s.Check,
		RegisterURL: s.RegisterURL, RevokeURL: s.RevokeURL,
	}
	if body.Access == "" {
		body.Access = forgeconnect.AccessUnknown
	}
	if body.Check.State == "" {
		body.Check = forgeconnect.CheckResult{State: forgeconnect.CheckOK}
	}
	if body.State == "" {
		body.State = forgeconnect.StateDisconnected
	}
	return body
}

// giteaStatusDocument never contacts the forge. Without a manager (it could
// not be built) the connection is reported as disabled.
func giteaStatusDocument(m *forgeconnect.Manager) giteaStatusBody {
	if m == nil {
		return newGiteaStatusBody(forgeconnect.Status{UnavailableReason: "disabled"})
	}
	return newGiteaStatusBody(m.Status())
}

// configureGiteaConnection builds the optional connection manager. It does
// no network I/O and starts no goroutines; a failure is one log line with
// the code only and Helm runs without the connection.
func (s *Server) configureGiteaConnection() {
	m, err := forgeconnect.New(forgeconnect.Options{
		Enabled: s.Cfg.GiteaOAuth, PublicOrigin: s.Cfg.PublicOrigin, StateDir: s.Cfg.ForgeStateDir,
	})
	if err != nil {
		s.logJSON(map[string]any{"level": "warn", "msg": "gitea connection unavailable", "code": forgeconnect.Code(err)})
		return
	}
	s.Gitea = m
}

// StartGiteaConnection runs the daily keep-alive for the server's lifetime.
func (s *Server) StartGiteaConnection(ctx context.Context) {
	if s.Gitea != nil {
		s.Gitea.Start(ctx)
	}
}

// StopGiteaConnection stops the keep-alive and waits for it. Call it after
// the HTTP servers have drained. It is safe to call twice or before Start.
func (s *Server) StopGiteaConnection() {
	if s.Gitea != nil {
		s.Gitea.Close()
	}
}
