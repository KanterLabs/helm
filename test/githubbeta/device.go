package main

import (
	"fmt"
	"net/http"
	"strings"
)

// Synthetic GitHub for device-flow sign-in (docs/GITHUB_DEVICE_FLOW_PLAN.md,
// C4). Every login, code, token and repository here is invented. Each secret
// kind carries its own sentinel so a leak can be traced to its kind.
const (
	deviceClientID   = "Iv1.synthetic0helm0beta"
	deviceAppSlug    = "synthetic-helm-beta"
	deviceLogin      = "synthetic-octo"
	docsRepository   = "synthetic/helm-docs"
	sentinelAccess   = "HELM_DEVICE_ACCESS_SENTINEL_DO_NOT_EXPOSE_9d41e2"
	sentinelRefresh  = "HELM_DEVICE_REFRESH_SENTINEL_DO_NOT_EXPOSE_5ac07b"
	sentinelDevice   = "HELM_DEVICE_CODE_SENTINEL_DO_NOT_EXPOSE_e37f10"
	userCodePrefix   = "WPFV"
	installationID   = 5501
	accessLifetime   = 8 * 60 * 60
	refreshLifetime  = 184 * 24 * 60 * 60
	attemptLifetime  = 900
	attemptInterval  = 5
	deviceGrant      = "urn:ietf:params:oauth:grant-type:device_code"
	refreshGrant     = "refresh_token"
	attemptPending   = "pending"
	attemptApproved  = "approved"
	attemptDenied    = "denied"
	attemptExpired   = "expired"
	attemptExchanged = "exchanged"
)

// repositoryIDs are the synthetic repositories GitHub knows about. Which of
// them the App is installed on is the control's to decide.
var repositoryIDs = map[string]int{syntheticRepository: 7401, docsRepository: 7402}

type deviceAttempt struct {
	deviceCode, userCode, state string
}

// deviceUpstream is guarded by syntheticState.mu.
type deviceUpstream struct {
	enabled      bool
	attempts     []*deviceAttempt
	access       string // the one access token GitHub accepts now
	refresh      string // the one refresh token GitHub accepts now
	seq          int
	refreshes    int
	tokenPolls   int
	rejectAccess string // a token GitHub refuses (401) from now on
	failRefresh  bool
	installed    []string
}

func (d *deviceUpstream) latest() *deviceAttempt {
	if len(d.attempts) == 0 {
		return nil
	}
	return d.attempts[len(d.attempts)-1]
}

func (d *deviceUpstream) issue() map[string]any {
	d.seq++
	d.access = fmt.Sprintf("%s-%d", sentinelAccess, d.seq)
	d.refresh = fmt.Sprintf("%s-%d", sentinelRefresh, d.seq)
	return map[string]any{"access_token": d.access, "token_type": "bearer", "scope": "", "expires_in": accessLifetime,
		"refresh_token": d.refresh, "refresh_token_expires_in": refreshLifetime}
}

func (d *deviceUpstream) installedOn(repository string) bool {
	for _, name := range d.installed {
		if name == repository {
			return true
		}
	}
	return false
}

// authorized reports whether GitHub accepts the request's token. Caller
// holds s.mu.
func (s *syntheticState) authorizedLocked(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !s.device.enabled {
		return !s.revoked && header == "Bearer "+sentinelToken
	}
	d := &s.device
	return !s.revoked && d.access != "" && header == "Bearer "+d.access && d.access != d.rejectAccess
}

// repositoryOf splits a /repos/{owner}/{name}{rest} path. Operator mode
// knows one repository; device-flow mode answers 404 outside the App's
// installations, as GitHub does for a user token.
func (s *syntheticState) repositoryOf(path string) (string, string, bool) {
	if !s.device.enabled {
		prefix := "/repos/" + syntheticRepository
		return syntheticRepository, strings.TrimPrefix(path, prefix), strings.HasPrefix(path, prefix)
	}
	for repository := range repositoryIDs {
		prefix := "/repos/" + repository
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			s.mu.Lock()
			installed := s.device.installedOn(repository)
			s.mu.Unlock()
			return repository, strings.TrimPrefix(path, prefix), installed
		}
	}
	return "", "", false
}

// serveDevice answers the device-flow, token and user endpoints. It returns
// false for any other path.
func (s *syntheticState) serveDevice(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	enabled := s.device.enabled
	s.mu.Unlock()
	if !enabled {
		return false
	}
	switch {
	case r.URL.Path == "/login/device/code" && r.Method == http.MethodPost:
		s.deviceCode(w, r)
	case r.URL.Path == "/login/oauth/access_token" && r.Method == http.MethodPost:
		s.token(w, r)
	case r.URL.Path == "/user" || strings.HasPrefix(r.URL.Path, "/user/installations"):
		s.userAPI(w, r)
	default:
		return false
	}
	return true
}

func (s *syntheticState) deviceCode(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || r.PostForm.Get("client_id") != deviceClientID {
		writeJSON(w, map[string]string{"error": "incorrect_client_credentials"})
		return
	}
	s.mu.Lock()
	d := &s.device
	n := len(d.attempts) + 1
	att := &deviceAttempt{deviceCode: fmt.Sprintf("%s-%d", sentinelDevice, n), userCode: fmt.Sprintf("%s-%04d", userCodePrefix, n), state: attemptPending}
	d.attempts = append(d.attempts, att)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"device_code": att.deviceCode, "user_code": att.userCode,
		"verification_uri": "http://" + r.Host + "/login/device", "expires_in": attemptLifetime, "interval": attemptInterval})
}

// token serves both the device-code exchange and the refresh grant. Like
// GitHub, it reports OAuth errors with HTTP 200 and an error field.
func (s *syntheticState) token(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || r.PostForm.Get("client_id") != deviceClientID {
		writeJSON(w, map[string]string{"error": "incorrect_client_credentials"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := &s.device
	switch r.PostForm.Get("grant_type") {
	case deviceGrant:
		d.tokenPolls++
		var att *deviceAttempt
		for _, candidate := range d.attempts {
			if candidate.deviceCode == r.PostForm.Get("device_code") {
				att = candidate
			}
		}
		switch {
		case att == nil:
			writeJSON(w, map[string]string{"error": "incorrect_device_code"})
		case att.state == attemptPending:
			writeJSON(w, map[string]string{"error": "authorization_pending"})
		case att.state == attemptDenied:
			writeJSON(w, map[string]string{"error": "access_denied"})
		case att.state == attemptApproved:
			att.state = attemptExchanged
			writeJSON(w, d.issue())
		default:
			writeJSON(w, map[string]string{"error": "expired_token"})
		}
	case refreshGrant:
		if d.failRefresh || d.refresh == "" || r.PostForm.Get("refresh_token") != d.refresh {
			// A refused refresh also kills the grant, as GitHub does.
			d.failRefresh, d.access, d.refresh = false, "", ""
			writeJSON(w, map[string]string{"error": "bad_refresh_token"})
			return
		}
		d.refreshes++
		writeJSON(w, d.issue())
	default:
		writeJSON(w, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (s *syntheticState) userAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	authorized := s.authorizedLocked(r)
	installed := append([]string{}, s.device.installed...)
	s.mu.Unlock()
	if !authorized {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"message": "Bad credentials"})
		return
	}
	switch r.URL.Path {
	case "/user":
		writeJSON(w, map[string]any{"login": deviceLogin, "id": 90001})
	case "/user/installations":
		installations := []any{}
		if len(installed) > 0 {
			installations = append(installations, map[string]any{"id": installationID})
		}
		writeJSON(w, map[string]any{"total_count": len(installations), "installations": installations})
	case fmt.Sprintf("/user/installations/%d/repositories", installationID):
		repositories := []any{}
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			for _, name := range installed {
				repositories = append(repositories, map[string]any{"id": repositoryIDs[name], "full_name": name})
			}
		}
		writeJSON(w, map[string]any{"total_count": len(installed), "repositories": repositories})
	default:
		http.NotFound(w, r)
	}
}
