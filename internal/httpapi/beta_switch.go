package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/betaswitch"
)

const betaSwitchIdempotencyTTL = 10 * time.Minute

type betaSwitchReplay struct {
	requestHash string
	status      int
	body        []byte
	location    string
	createdAt   time.Time
}

type betaBuildsResponse struct {
	Enabled    bool                 `json:"enabled"`
	CurrentSHA string               `json:"current_sha"`
	Builds     []betaswitch.Release `json:"builds"`
}

type betaSwitchResponse struct {
	Enabled bool           `json:"enabled"`
	Job     betaswitch.Job `json:"job"`
}

func (s *Server) betaSwitchRoute(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) {
	// Keep the disabled boundary inside dispatch as well as in ServeHTTP so
	// direct route tests/callers cannot accidentally reach the broker.
	if !s.betaSwitchEnabled() {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	if !requireAdmin(w, identity) {
		return
	}
	switch {
	case len(parts) == 1 && parts[0] == "builds":
		s.betaBuilds(w, r)
	case len(parts) == 1 && parts[0] == "switch":
		s.betaSwitch(w, r, identity)
	case len(parts) == 2 && parts[0] == "switches":
		s.betaSwitchJob(w, r, parts[1])
	default:
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

func (s *Server) betaBuilds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	client := s.BetaSwitch
	if client == nil {
		writeBetaUnavailable(w)
		return
	}
	result, err := client.ListReleases(r.Context())
	if err != nil {
		writeBetaControllerError(w, err)
		return
	}
	builds, currentSHA, ok := sanitizeBetaReleases(result)
	if !ok {
		writeBetaUnavailable(w)
		return
	}
	s.writeJSON(w, http.StatusOK, betaBuildsResponse{Enabled: true, CurrentSHA: currentSHA, Builds: builds})
}

func (s *Server) betaSwitch(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		s.writeError(w, http.StatusBadRequest, "idempotency_required", "Idempotency-Key is required for beta switches", nil)
		return
	}
	if len(key) > 255 {
		s.writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long", nil)
		return
	}
	requestHash := betaSwitchRequestHash(r)
	replayKey := betaSwitchReplayKey(identity, r, key)
	if replay, found, conflict := s.betaSwitchReplay(w, replayKey, requestHash); found {
		if conflict {
			s.writeError(w, http.StatusConflict, "idempotency_key_reused", "Idempotency-Key was already used for a different request", nil)
			return
		}
		if replay.location != "" {
			w.Header().Set("Location", replay.location)
		}
		s.writeRaw(w, replay.status, replay.body, "")
		return
	}
	var payload struct {
		SHA *string `json:"sha"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "sha"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := rejectJSONNull(fields, "sha"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	sha := *payload.SHA
	if !validBetaSHA(sha) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "sha must be 40 lowercase hexadecimal characters", nil)
		return
	}
	client := s.BetaSwitch
	if client == nil {
		writeBetaUnavailable(w)
		return
	}
	// Hold the short-lived replay lock across enqueueing so two concurrent
	// requests with one key cannot both reach the broker. The broker itself
	// remains the authority for target admission and job creation.
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	if replay, found, conflict := s.betaSwitchReplayLocked(replayKey, requestHash); found {
		if conflict {
			s.writeError(w, http.StatusConflict, "idempotency_key_reused", "Idempotency-Key was already used for a different request", nil)
			return
		}
		if replay.location != "" {
			w.Header().Set("Location", replay.location)
		}
		s.writeRaw(w, replay.status, replay.body, "")
		return
	}
	job, err := client.RequestSwitch(r.Context(), sha)
	if err != nil {
		writeBetaControllerError(w, err)
		return
	}
	if job.TargetSHA == "" {
		job.TargetSHA = sha
	}
	job, ok := sanitizeBetaJob(job)
	if !ok {
		writeBetaUnavailable(w)
		return
	}
	location := "/api/v1/admin/beta/switches/" + job.ID
	response, err := json.Marshal(betaSwitchResponse{Enabled: true, Job: job})
	if err != nil {
		writeBetaUnavailable(w)
		return
	}
	if s.betaSwitchIdem == nil {
		s.betaSwitchIdem = make(map[string]betaSwitchReplay)
	}
	s.betaSwitchIdem[replayKey] = betaSwitchReplay{requestHash: requestHash, status: http.StatusAccepted, body: append([]byte(nil), response...), location: location, createdAt: time.Now()}
	w.Header().Set("Location", location)
	s.writeRaw(w, http.StatusAccepted, response, "")
}

func (s *Server) betaSwitchJob(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !validBetaJobID(id) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "switch job id must be 32 lowercase hexadecimal characters", nil)
		return
	}
	client := s.BetaSwitch
	if client == nil {
		writeBetaUnavailable(w)
		return
	}
	job, err := client.GetJob(r.Context(), id)
	if err != nil {
		writeBetaControllerError(w, err)
		return
	}
	job, ok := sanitizeBetaJob(job)
	if !ok || job.ID != id {
		writeBetaUnavailable(w)
		return
	}
	s.writeJSON(w, http.StatusOK, betaSwitchResponse{Enabled: true, Job: job})
}

func (s *Server) betaSwitchReplay(w http.ResponseWriter, key, requestHash string) (betaSwitchReplay, bool, bool) {
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	return s.betaSwitchReplayLocked(key, requestHash)
}

func (s *Server) betaSwitchReplayLocked(key, requestHash string) (betaSwitchReplay, bool, bool) {
	if len(s.betaSwitchIdem) == 0 {
		return betaSwitchReplay{}, false, false
	}
	now := time.Now()
	for replayKey, replay := range s.betaSwitchIdem {
		if now.Sub(replay.createdAt) > betaSwitchIdempotencyTTL {
			delete(s.betaSwitchIdem, replayKey)
		}
	}
	replay, found := s.betaSwitchIdem[key]
	if !found {
		return betaSwitchReplay{}, false, false
	}
	return replay, true, replay.requestHash != requestHash
}

func betaSwitchReplayKey(identity auth.Identity, r *http.Request, key string) string {
	return identity.Actor.ID + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + key
}

func betaSwitchRequestHash(r *http.Request) string {
	body := bodyBytes(r)
	if body == nil && r != nil && r.Body != nil {
		body, _ = requestBodyData(r)
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func validBetaSHA(sha string) bool {
	if len(sha) != 40 || sha != strings.ToLower(sha) {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func validBetaJobID(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func sanitizeBetaReleases(result betaswitch.ReleasesResponse) ([]betaswitch.Release, string, bool) {
	currentSHA := result.CurrentSHA
	if currentSHA != "" && currentSHA != "none" && !validBetaSHA(currentSHA) {
		return nil, "", false
	}
	builds := make([]betaswitch.Release, 0, len(result.Releases))
	for _, release := range result.Releases {
		if !validBetaSHA(release.SHA) || !safeBetaRef(release.Ref) {
			return nil, "", false
		}
		if release.Subject != "" && !betaswitch.ValidCommitSubject(release.Subject) {
			return nil, "", false
		}
		builds = append(builds, betaswitch.Release{SHA: release.SHA, Ref: release.Ref, Subject: release.Subject, Current: release.Current})
	}
	return builds, currentSHA, true
}

func sanitizeBetaJob(job betaswitch.Job) (betaswitch.Job, bool) {
	if !validBetaJobID(job.ID) || !validBetaSHA(job.TargetSHA) {
		return betaswitch.Job{}, false
	}
	switch job.State {
	case betaswitch.JobQueued, betaswitch.JobRunning, betaswitch.JobSucceeded, betaswitch.JobFailed:
	default:
		return betaswitch.Job{}, false
	}
	if job.Error != "" && !safeBetaError(job.Error) {
		job.Error = string(betaswitch.ErrInternal)
	}
	return betaswitch.Job{ID: job.ID, TargetSHA: job.TargetSHA, State: job.State, Error: job.Error}, true
}

func safeBetaRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return false
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	// Keep this in lockstep with betaswitch.validateBranchLabel. Refs are
	// canonical branch labels, so nested branches such as feature/foo are
	// valid, while path/control-character tricks remain rejected.
	if branch == "" || len(branch) > 201 || strings.ContainsAny(branch, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x20~^:?*[\\") {
		return false
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "//") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") {
		return false
	}
	for _, segment := range strings.Split(branch, "/") {
		if segment == "." || segment == ".." || strings.HasSuffix(segment, ".") || !safeBetaBranchSegment(segment) {
			return false
		}
	}
	if strings.HasSuffix(strings.ToLower(branch), ".lock") {
		return false
	}
	return true
}

func safeBetaBranchSegment(segment string) bool {
	if segment == "" || len(segment) > 201 {
		return false
	}
	for index, char := range segment {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '.' && char != '_' && char != '-' {
			return false
		}
		if index == 0 && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func safeBetaError(value string) bool {
	switch betaswitch.ErrorCode(value) {
	case betaswitch.ErrInvalidRequest, betaswitch.ErrUnauthorized, betaswitch.ErrNotFound,
		betaswitch.ErrConflict, betaswitch.ErrReleaseInvalid, betaswitch.ErrStateFailure,
		betaswitch.ErrRollbackFailure, betaswitch.ErrUnavailable, betaswitch.ErrInternal:
		return true
	default:
		return false
	}
}

func writeBetaUnavailable(w http.ResponseWriter) {
	sDummyWriteError(w, http.StatusBadGateway, "beta_switch_unavailable", "beta switch broker is unavailable", nil)
}

func writeBetaControllerError(w http.ResponseWriter, err error) {
	var codedErr *betaswitch.CodedError
	if !errors.As(err, &codedErr) || codedErr == nil {
		writeBetaUnavailable(w)
		return
	}
	switch codedErr.Code {
	case betaswitch.ErrInvalidRequest:
		sDummyWriteError(w, http.StatusBadRequest, "invalid_request", "beta switch request is invalid", nil)
	case betaswitch.ErrReleaseInvalid:
		writeBetaUnavailable(w)
	case betaswitch.ErrNotFound:
		sDummyWriteError(w, http.StatusNotFound, "not_found", "beta switch resource not found", nil)
	case betaswitch.ErrConflict:
		sDummyWriteError(w, http.StatusConflict, "conflict", "beta switch request conflicts with current state", nil)
	case betaswitch.ErrUnavailable:
		writeBetaUnavailable(w)
	default:
		sDummyWriteError(w, http.StatusBadGateway, "beta_switch_failed", "beta switch broker rejected the request", nil)
	}
}
