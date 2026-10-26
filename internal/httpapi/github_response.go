package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/KanterLabs/helm/internal/store"
)

// githubArtifactResponse keeps idempotency records free of disposable private
// snapshots. Each successful link response hydrates the current artifact only
// after the caller's project authorization and current credential checks.
type githubArtifactResponse struct {
	http.ResponseWriter
	ctx     context.Context
	service *GithubService
	taskID  string
	status  int
}

func (w *githubArtifactResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *githubArtifactResponse) Write(body []byte) (int, error) {
	if w.status < 200 || w.status >= 300 {
		return w.ResponseWriter.Write(body)
	}
	var response struct {
		Artifact    store.GithubArtifact `json:"artifact"`
		TaskVersion int64                `json:"task_version"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Artifact.ID == "" {
		return w.ResponseWriter.Write(body)
	}
	current, err := w.service.data.GetGithubArtifact(w.ctx, w.taskID, response.Artifact.ID)
	if err == nil {
		response.Artifact = w.service.MaskArtifact(current)
	} else {
		response.Artifact.Snapshot = nil
		response.Artifact.Verification = "unavailable"
		response.Artifact.ErrorCode = "artifact_detached"
		response.Artifact.ErrorMessage = "The pull request link is no longer available on this task."
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return 0, err
	}
	_, err = w.ResponseWriter.Write(encoded)
	if err != nil {
		return 0, err
	}
	return len(body), nil
}
