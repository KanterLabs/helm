package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/githubconnect"
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/httpapi"
	"github.com/KanterLabs/helm/internal/store"
)

// Two local-login administrators, so a spec can prove the second one never
// sees the first one's code. Synthetic and loopback-only.
const (
	adminOneEmail   = "admin-one@synthetic.invalid"
	adminTwoEmail   = "admin-two@synthetic.invalid"
	adminPassword   = "synthetic-admin-password-0001"
	maxClockOffset  = 400 * 24 * time.Hour
	credentialsFile = "connection.json"
)

// harnessClock is the connection manager's clock. Moving it forward makes
// the current access token due for refresh without touching the file.
type harnessClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *harnessClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

// ensureAdmins creates both administrators once; a restart keeps them.
func (h *harness) ensureAdmins(ctx context.Context, data *store.Store) error {
	if count, err := data.CountHumanActors(ctx); err != nil || count > 0 {
		return err
	}
	if _, err := auth.NewManager(data, h.cfg).Setup(ctx, adminOneEmail, "Synthetic Admin One", adminPassword); err != nil {
		return err
	}
	hash, err := auth.HashPassword(adminPassword)
	if err != nil {
		return err
	}
	email := adminTwoEmail
	_, err = data.CreateActor(ctx, store.Actor{Kind: "human", Name: "Synthetic Admin Two", Email: &email, Admin: true}, hash)
	return err
}

// installDeviceFlow replaces the manager httpapi.New built (real GitHub
// origins, never started) with one aimed at the synthetic upstream through
// the manager's constructor-only seams, and wires it as the provider's
// credential source and allowlist exactly as installGithubServices does.
func (h *harness) installDeviceFlow(s *httpapi.Server, data *store.Store) error {
	if s.GithubConnection != nil {
		s.GithubConnection.Close()
	}
	manager, err := githubconnect.New(githubconnect.Options{
		ClientID: h.cfg.GitHubAppClientID, AppSlug: h.cfg.GitHubAppSlug, StateDir: h.cfg.GitHubStateDir,
		Ceiling: h.cfg.GitHubRepositories, HTTPClient: h.upstream.Client(),
		WebOrigin: h.upstream.URL, APIOrigin: h.upstream.URL, Now: h.clock.now,
	})
	if err != nil {
		return err
	}
	provider := githubprovider.New(githubprovider.Options{Enabled: true, Source: manager,
		Endpoint: h.upstream.URL, HTTPClient: h.upstream.Client()})
	manager.OnRepositories(provider.SetRepositories)
	s.GithubConnection = manager
	s.Github = httpapi.NewGithubService(data, provider)
	s.GithubAudits = httpapi.NewGithubAuditService(data, provider, h.ai, nil)
	return nil
}

type credentialStat struct {
	Exists   bool   `json:"exists"`
	Regular  bool   `json:"regular"`
	FileMode string `json:"file_mode"`
	DirMode  string `json:"dir_mode"`
}

// statCredential reports the file's presence and permissions, never its
// content.
func (h *harness) statCredential() credentialStat {
	var stat credentialStat
	if info, err := os.Stat(h.cfg.GitHubStateDir); err == nil {
		stat.DirMode = fmt.Sprintf("%04o", info.Mode().Perm())
	}
	if info, err := os.Lstat(filepath.Join(h.cfg.GitHubStateDir, credentialsFile)); err == nil {
		stat.Exists, stat.Regular, stat.FileMode = true, info.Mode().IsRegular(), fmt.Sprintf("%04o", info.Mode().Perm())
	}
	return stat
}

// deviceControl drives the synthetic GitHub: the user's actions on GitHub
// (approve with the code, deny, let the code expire, install or remove the
// App on repositories) and GitHub-side faults (refuse the current token,
// refuse the next refresh), plus the manager's clock. Its answer carries
// counts and file permissions, never a token, device code or user code.
func (h *harness) deviceControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if !h.controlOrigin(r) || !h.deviceFlow {
		w.WriteHeader(403)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Action          string    `json:"action"`
			UserCode        string    `json:"user_code"`
			Installed       *[]string `json:"installed"`
			RejectAccess    bool      `json:"reject_access"`
			FailNextRefresh bool      `json:"fail_next_refresh"`
			AdvanceSeconds  int64     `json:"advance_seconds"`
			ResetClock      bool      `json:"reset_clock"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			http.Error(w, "invalid control", 400)
			return
		}
		if status := h.applyDevice(input.Action, input.UserCode, input.Installed, input.RejectAccess, input.FailNextRefresh); status != 0 {
			w.WriteHeader(status)
			writeJSON(w, map[string]string{"error": "no pending attempt with that code"})
			return
		}
		h.clock.mu.Lock()
		if input.ResetClock {
			h.clock.offset = 0
		}
		if next := h.clock.offset + time.Duration(input.AdvanceSeconds)*time.Second; input.AdvanceSeconds > 0 && next <= maxClockOffset {
			h.clock.offset = next
		}
		h.clock.mu.Unlock()
	}
	h.state.mu.Lock()
	d := &h.state.device
	pending := d.latest() != nil && d.latest().state == attemptPending
	body := map[string]any{"attempts": len(d.attempts), "pending": pending, "token_seq": d.seq, "refreshes": d.refreshes,
		"token_polls": d.tokenPolls, "installed": append([]string{}, d.installed...), "granted": d.access != "", "upstream_requests": h.state.requests}
	h.state.mu.Unlock()
	h.clock.mu.Lock()
	body["clock_offset_seconds"] = int64(h.clock.offset / time.Second)
	h.clock.mu.Unlock()
	body["credential"] = h.statCredential()
	writeJSON(w, body)
}

// applyDevice applies one control change. It answers 409 when the user
// action has no pending attempt to act on or the code does not match.
func (h *harness) applyDevice(action, userCode string, installed *[]string, rejectAccess, failRefresh bool) int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	d := &h.state.device
	if action != "" {
		att := d.latest()
		if att == nil || att.state != attemptPending {
			return http.StatusConflict
		}
		switch action {
		case "approve":
			if userCode != att.userCode {
				return http.StatusConflict
			}
			att.state = attemptApproved
		case "deny":
			att.state = attemptDenied
		case "expire":
			att.state = attemptExpired
		default:
			return http.StatusBadRequest
		}
	}
	if installed != nil {
		d.installed = []string{}
		for _, name := range *installed {
			if _, known := repositoryIDs[name]; known {
				d.installed = append(d.installed, name)
			}
		}
	}
	if rejectAccess {
		d.rejectAccess = d.access
	}
	if failRefresh {
		d.failRefresh = true
	}
	return 0
}
