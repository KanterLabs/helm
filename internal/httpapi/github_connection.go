package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubconnect"
	"github.com/KanterLabs/helm/internal/githubprovider"
)

// GitHub device-flow sign-in routes (docs/GITHUB_DEVICE_FLOW_PLAN.md, C5).
// Every answer is the integration document or a fixed error of ours, sent
// with Cache-Control: no-store. None is wrapped in s.mutation: the document
// can carry a user code, so nothing is stored for idempotent replay, and a
// repeated request reaches the manager again (each action is idempotent).

const (
	githubSyncTimeout    = 20 * time.Second
	githubBeginTimeout   = 20 * time.Second
	githubDiagnoseBudget = 15 * time.Second
)

// githubIntegrationDocument is GithubDiagnostics plus the connection object.
type githubIntegrationDocument struct {
	githubprovider.Diagnostics
	Connection githubconnect.Status `json:"connection"`
}

// githubConnectionErrors maps each manager code to a status, a wire code
// and a fixed sentence. Never 502 or 504 (amendment A2): a proxy in front of
// Helm replaces those bodies and the panel would lose error.code.
var githubConnectionErrors = map[string]struct {
	status        int
	code, message string
}{
	githubconnect.CodeSigninUnavailable: {http.StatusConflict, "github_signin_unavailable", "GitHub sign-in is not set up on this Helm."},
	githubconnect.CodeOperatorManaged:   {http.StatusConflict, "github_operator_managed", "An operator manages GitHub on this Helm with a token file."},
	githubconnect.CodeInvalidRequest:    {http.StatusBadRequest, "invalid_request", "The request could not be used."},
	githubconnect.CodeAlreadyConnected:  {http.StatusConflict, "github_already_connected", "GitHub is already connected."},
	githubconnect.CodeNotConnected:      {http.StatusConflict, "github_not_connected", "GitHub is not connected."},
	githubconnect.CodeReconnectRequired: {http.StatusConflict, "github_reconnect_required", "GitHub sign-in has ended; reconnect GitHub."},
	githubconnect.CodeGitHubUnreachable: {http.StatusFailedDependency, "github_unreachable", "Helm could not reach GitHub."},
	githubconnect.CodeGitHubRefused:     {http.StatusFailedDependency, "github_refused", "GitHub refused the request."},
	githubconnect.CodeStorageFailed:     {http.StatusInternalServerError, "github_storage_failed", "Helm could not save or remove its GitHub credential."},
}

// githubConnectionError returns the answer for err. Only the code is read:
// an error never carries GitHub's text, but nothing of it is sent anyway.
func githubConnectionError(err error) (int, string, string) {
	if entry, ok := githubConnectionErrors[githubconnect.Code(err)]; ok {
		return entry.status, entry.code, entry.message
	}
	return http.StatusInternalServerError, "internal_error", "internal server error"
}

func (s *Server) writeGithubConnectionError(w http.ResponseWriter, err error) {
	status, code, message := githubConnectionError(err)
	s.writeError(w, status, code, message, nil)
}

// githubConnectionStatus is the connection object as actorID may see it.
// The manager cannot describe operator_token mode, so it is built here.
func (s *Server) githubConnectionStatus(actorID string) githubconnect.Status {
	if s.githubMode() == githubconnect.ModeOperatorToken {
		return githubconnect.Status{Mode: githubconnect.ModeOperatorToken, State: githubconnect.StateDisconnected}
	}
	if s.GithubConnection == nil {
		return githubconnect.Status{Mode: githubconnect.ModeDeviceFlow, State: githubconnect.StateDisconnected}
	}
	return s.GithubConnection.Status(actorID)
}

func (s *Server) writeGithubIntegration(w http.ResponseWriter, actorID string, diagnostics githubprovider.Diagnostics) {
	body, err := json.Marshal(githubIntegrationDocument{Diagnostics: diagnostics, Connection: s.githubConnectionStatus(actorID)})
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	s.writeRaw(w, http.StatusOK, body, "")
}

// githubConnectionCaller admits signed-in human administrators only. Bearer
// tokens are refused as codex_account.go refuses them, whatever their actor.
func githubConnectionCaller(w http.ResponseWriter, identity auth.Identity) bool {
	w.Header().Set("Cache-Control", "no-store")
	if identity.IsToken || identity.Actor.Kind != "human" {
		sDummyWriteError(w, http.StatusForbidden, "human_account_required", "The GitHub connection can only be managed by a signed-in administrator", nil)
		return false
	}
	return requireAdmin(w, identity)
}

// githubIntegration serves GET /integrations/github and POST .../check. In
// device_flow mode, check first re-reads the installations (Sync).
// Its access rule is unchanged: human administrators, tokens refused.
func (s *Server) githubIntegration(w http.ResponseWriter, r *http.Request, identity auth.Identity, check bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !requireAdmin(w, identity) {
		return
	}
	if (check && r.Method != http.MethodPost) || (!check && r.Method != http.MethodGet) {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if check && s.githubMode() == githubconnect.ModeDeviceFlow && s.GithubConnection != nil {
		ctx, cancel := context.WithTimeout(r.Context(), githubSyncTimeout)
		err := s.GithubConnection.Sync(ctx)
		cancel()
		if err != nil {
			s.writeGithubConnectionError(w, err)
			return
		}
	}
	if !check {
		s.writeGithubIntegration(w, identity.Actor.ID, s.cachedGithubDiagnostics())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), githubDiagnoseBudget)
	defer cancel()
	s.writeGithubIntegration(w, identity.Actor.ID, s.githubService().Diagnostics(ctx, true))
}

// cachedGithubDiagnostics reads diagnostics without network I/O, once the
// provider holds the manager's current allowlist. A sign-in that completes
// in the background notifies the provider just after it records the
// connection; without this wait a read in between would say "connected"
// with no repositories, and the panel stops polling once connected.
func (s *Server) cachedGithubDiagnostics() githubprovider.Diagnostics {
	d := s.githubService().Diagnostics(context.Background(), false)
	for try := 0; s.GithubConnection != nil && try < 40 && !slices.Equal(s.GithubConnection.Repositories(), d.AllowedRepositories); try++ {
		time.Sleep(5 * time.Millisecond)
		d = s.githubService().Diagnostics(context.Background(), false)
	}
	return d
}

// githubConnection serves POST .../connect, .../connect/cancel and
// .../disconnect.
func (s *Server) githubConnection(w http.ResponseWriter, r *http.Request, identity auth.Identity, action string) {
	if !githubConnectionCaller(w, identity) {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	attemptID := ""
	if action == "cancel" {
		var input struct {
			AttemptID *string `json:"attempt_id"`
		}
		fields, err := decodeJSONObject(r, &input)
		if err != nil || len(fields) != 1 || input.AttemptID == nil || strings.TrimSpace(*input.AttemptID) == "" || len(*input.AttemptID) > 200 {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "attempt_id is required", nil)
			return
		}
		attemptID = *input.AttemptID
	}
	if s.githubMode() == githubconnect.ModeOperatorToken {
		s.writeGithubConnectionError(w, &githubconnect.Error{Code: githubconnect.CodeOperatorManaged})
		return
	}
	manager := s.GithubConnection
	if manager == nil {
		s.writeGithubConnectionError(w, &githubconnect.Error{Code: githubconnect.CodeSigninUnavailable})
		return
	}
	actorID := identity.Actor.ID
	var err error
	switch action {
	case "connect":
		ctx, cancel := context.WithTimeout(r.Context(), githubBeginTimeout)
		_, err = manager.Begin(ctx, actorID)
		cancel()
	case "cancel":
		manager.Cancel(actorID, attemptID)
	case "disconnect":
		err = manager.Disconnect(actorID)
	}
	if err != nil {
		s.writeGithubConnectionError(w, err)
		return
	}
	s.writeGithubIntegration(w, actorID, s.cachedGithubDiagnostics())
}
