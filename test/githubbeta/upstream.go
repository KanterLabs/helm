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
	mu            sync.Mutex
	revoked       bool
	delayMS       int
	branchDelayMS int
	requests      int
	aiCalls       int
	lastAIActor   string
	device        deviceUpstream
}

func (s *syntheticState) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.mu.Unlock()
	if s.serveDevice(w, r) {
		return
	}
	s.mu.Lock()
	authorized := s.authorizedLocked(r)
	s.mu.Unlock()
	if !authorized {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]string{"message": "Synthetic GitHub access revoked"})
		return
	}
	repository, path, ok := s.repositoryOf(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	repositoryID := repositoryIDs[repository]
	if s.serveBranches(w, r, path) {
		return
	}
	switch {
	case path == "":
		writeJSON(w, map[string]any{"id": repositoryID, "full_name": repository, "default_branch": "main", "permissions": map[string]bool{"pull": true}})
	case path == "/pulls":
		writeJSON(w, []any{})
	case path == "/pulls/42":
		writeJSON(w, map[string]any{"title": "Synthetic merged implementation", "state": "closed", "merged": true,
			"draft": false, "merge_commit_sha": sourceSHA, "head": map[string]string{"sha": sourceSHA},
			"base": map[string]any{"sha": baseSHA, "repo": map[string]int{"id": repositoryID}}})
	case path == "/pulls/42/reviews":
		writeJSON(w, []any{map[string]any{"id": 1, "state": "APPROVED", "commit_id": sourceSHA, "user": map[string]int{"id": 31}}})
	case strings.HasSuffix(path, "/check-runs"):
		writeJSON(w, map[string]any{"total_count": 1, "check_runs": []any{map[string]string{
			"name": "Synthetic source validation", "head_sha": sourceSHA, "status": "completed", "conclusion": "success",
			"html_url": "https://github.com/" + repository + "/actions/runs/7401"}}})
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
