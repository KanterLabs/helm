package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
)

const (
	syntheticRepository = "synthetic/helm-beta"
	sourceSHA           = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blobSHA             = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	baseSHA             = "cccccccccccccccccccccccccccccccccccccccc"
	sentinelToken       = "HELM_GITHUB_BETA_SENTINEL_DO_NOT_EXPOSE_74bf8c"
	sourceText          = "package synthetic\n\n// The implementation is ready for a human to schedule.\nconst Ready = true\n"
)

type syntheticState struct {
	mu          sync.Mutex
	revoked     bool
	delayMS     int
	requests    int
	aiCalls     int
	lastAIActor string
}

func (s *syntheticState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	revoked := s.revoked
	s.mu.Unlock()
	if revoked || r.Header.Get("Authorization") != "Bearer "+sentinelToken {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"message": "Synthetic GitHub access revoked"})
		return
	}
	prefix := "/repos/" + syntheticRepository
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, prefix)
	switch {
	case path == "":
		writeJSON(w, map[string]any{"id": 7401, "full_name": syntheticRepository, "default_branch": "main", "permissions": map[string]bool{"pull": true}})
	case path == "/pulls":
		writeJSON(w, []any{})
	case path == "/pulls/42":
		writeJSON(w, map[string]any{"title": "Synthetic merged implementation", "state": "closed", "merged": true,
			"draft": false, "merge_commit_sha": sourceSHA, "head": map[string]string{"sha": sourceSHA},
			"base": map[string]any{"sha": baseSHA, "repo": map[string]int{"id": 7401}}})
	case path == "/pulls/42/reviews":
		writeJSON(w, []any{map[string]any{"id": 1, "state": "APPROVED", "commit_id": sourceSHA, "user": map[string]int{"id": 31}}})
	case path == "/pulls/42/files":
		writeJSON(w, []any{map[string]string{"filename": "ready.go"}})
	case strings.HasSuffix(path, "/check-runs"):
		writeJSON(w, map[string]any{"total_count": 1, "check_runs": []any{map[string]string{
			"name": "Synthetic source validation", "head_sha": sourceSHA, "status": "completed", "conclusion": "success",
			"html_url": "https://github.com/" + syntheticRepository + "/actions/runs/7401"}}})
	case strings.HasSuffix(path, "/statuses"):
		writeJSON(w, []any{})
	case strings.HasPrefix(path, "/commits/"):
		writeJSON(w, map[string]string{"sha": sourceSHA})
	case path == "/git/trees/"+sourceSHA:
		writeJSON(w, map[string]any{"truncated": false, "tree": []any{map[string]any{
			"path": "ready.go", "type": "blob", "mode": "100644", "sha": blobSHA, "size": len(sourceText)}}})
	case path == "/contents/ready.go" && r.URL.Query().Get("ref") == sourceSHA:
		writeJSON(w, map[string]any{"type": "file", "encoding": "base64", "sha": blobSHA, "size": len(sourceText),
			"content": base64.StdEncoding.EncodeToString([]byte(sourceText))})
	default:
		http.NotFound(w, r)
	}
}
