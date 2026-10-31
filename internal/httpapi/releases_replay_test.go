package httpapi

import (
	"context"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseMutationReplayHonorsCurrentProjectAuthorization(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatalf("ensure disabled actor: %v", err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REPLAYALLOW"), Name: stringPtr("Replay allowed")}, actor.ID)
	if err != nil {
		t.Fatalf("create allowed project: %v", err)
	}
	other, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REPLAYOTHER"), Name: stringPtr("Replay other")}, actor.ID)
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Release replay agent", ProjectIDs: []string{project.ID, other.ID}}, actor.ID, "")
	if err != nil {
		t.Fatalf("create replay agent: %v", err)
	}
	token, rawToken, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "release replay token", []string{"tasks:read", "tasks:write"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatalf("create replay token: %v", err)
	}
	authHeaders := func(ifMatch, key string) map[string]string {
		return map[string]string{
			"Authorization":   "Bearer " + rawToken,
			"If-Match":        ifMatch,
			"Idempotency-Key": key,
		}
	}
	setTokenProjects := func(projectID string) {
		t.Helper()
		encoded, encodeErr := json.Marshal([]string{projectID})
		if encodeErr != nil {
			t.Fatalf("encode token projects: %v", encodeErr)
		}
		if _, updateErr := data.DB.ExecContext(ctx, `UPDATE tokens SET project_ids=? WHERE id=?`, string(encoded), token.ID); updateErr != nil {
			t.Fatalf("update token project ceiling: %v", updateErr)
		}
	}
	denyReplay := func(method, target string, payload any, headers map[string]string, leaked string) {
		t.Helper()
		response := request(t, server, method, target, payload, headers)
		if response.Code != http.StatusForbidden || responseErrorCode(t, response.Body.Bytes()) != "forbidden" {
			t.Fatalf("unauthorized release replay %s %s: status=%d body=%s", method, target, response.Code, response.Body.String())
		}
		if leaked != "" && strings.Contains(response.Body.String(), leaked) {
			t.Fatalf("unauthorized release replay leaked %q: %s", leaked, response.Body.String())
		}
	}

	setTokenProjects(project.ID)
	patchName := "patch replay secret"
	patchReleaseName := "patch replay"
	patchRelease, err := data.CreateRelease(ctx, project.ID, store.ReleaseInput{Name: &patchReleaseName}, actor.ID)
	if err != nil {
		t.Fatalf("create patch release: %v", err)
	}
	patchPath := "/api/v1/releases/" + patchRelease.ID
	patchPayload := map[string]any{"name": patchName}
	patchKey := "release-replay-patch"
	patch := request(t, server, http.MethodPatch, patchPath, patchPayload, mergeHeaders(authHeaders(`"v1"`, patchKey), map[string]string{"Content-Type": "application/json"}))
	if patch.Code != http.StatusOK {
		t.Fatalf("patch release: status=%d body=%s", patch.Code, patch.Body.String())
	}
	setTokenProjects(other.ID)
	denyReplay(http.MethodPatch, patchPath, patchPayload, mergeHeaders(authHeaders(`"v1"`, patchKey), map[string]string{"Content-Type": "application/json"}), patchName)

	setTokenProjects(project.ID)
	completeReleaseName := "complete replay"
	completeRelease, err := data.CreateRelease(ctx, project.ID, store.ReleaseInput{Name: &completeReleaseName}, actor.ID)
	if err != nil {
		t.Fatalf("create complete release: %v", err)
	}
	completeTaskTitle := "complete replay task"
	completeTask, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &completeTaskTitle, ReleaseID: &completeRelease.ID}, actor.ID)
	if err != nil {
		t.Fatalf("create complete release task: %v", err)
	}
	if _, err := data.CompleteTask(ctx, completeTask.ID, actor.ID, completeTask.Version); err != nil {
		t.Fatalf("complete release task: %v", err)
	}
	completePath := "/api/v1/releases/" + completeRelease.ID + "/complete"
	completeKey := "release-replay-complete"
	complete := request(t, server, http.MethodPost, completePath, nil, authHeaders(`"v1"`, completeKey))
	if complete.Code != http.StatusOK {
		t.Fatalf("complete release: status=%d body=%s", complete.Code, complete.Body.String())
	}
	setTokenProjects(other.ID)
	denyReplay(http.MethodPost, completePath, nil, authHeaders(`"v1"`, completeKey), completeReleaseName)

	setTokenProjects(project.ID)
	reopenReleaseName := "reopen replay"
	reopenRelease, err := data.CreateRelease(ctx, project.ID, store.ReleaseInput{Name: &reopenReleaseName}, actor.ID)
	if err != nil {
		t.Fatalf("create reopen release: %v", err)
	}
	reopenTaskTitle := "reopen replay task"
	reopenTask, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &reopenTaskTitle, ReleaseID: &reopenRelease.ID}, actor.ID)
	if err != nil {
		t.Fatalf("create reopen release task: %v", err)
	}
	if _, err := data.CompleteTask(ctx, reopenTask.ID, actor.ID, reopenTask.Version); err != nil {
		t.Fatalf("complete reopen release task: %v", err)
	}
	reopenRelease, err = data.CompleteRelease(ctx, reopenRelease.ID, reopenRelease.Version, actor.ID)
	if err != nil {
		t.Fatalf("prepare reopen release: %v", err)
	}
	reopenPath := "/api/v1/releases/" + reopenRelease.ID + "/reopen"
	reopenPayload := map[string]any{"reason": "replay authorization regression"}
	reopenKey := "release-replay-reopen"
	reopen := request(t, server, http.MethodPost, reopenPath, reopenPayload, mergeHeaders(authHeaders(`"v2"`, reopenKey), map[string]string{"Content-Type": "application/json"}))
	if reopen.Code != http.StatusOK {
		t.Fatalf("reopen release: status=%d body=%s", reopen.Code, reopen.Body.String())
	}
	setTokenProjects(other.ID)
	denyReplay(http.MethodPost, reopenPath, reopenPayload, mergeHeaders(authHeaders(`"v2"`, reopenKey), map[string]string{"Content-Type": "application/json"}), reopenReleaseName)

	setTokenProjects(project.ID)
	deleteReleaseName := "delete replay"
	deleteRelease, err := data.CreateRelease(ctx, project.ID, store.ReleaseInput{Name: &deleteReleaseName}, actor.ID)
	if err != nil {
		t.Fatalf("create delete release: %v", err)
	}
	deletePath := "/api/v1/releases/" + deleteRelease.ID
	deleteKey := "release-replay-delete"
	lookup, err := data.LookupToken(ctx, rawToken)
	if err != nil {
		t.Fatal(err)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, deletePath, nil)
	requestHash := idempotencyRequestHash(deleteRequest, auth.Identity{Actor: lookup.Actor, IsToken: true, Token: lookup})
	if err := data.SaveIdempotency(ctx, agent.ID, "token:"+token.ID+":"+deleteKey, http.MethodDelete, deletePath, requestHash, store.IdempotencyRecord{Status: http.StatusNoContent}); err != nil {
		t.Fatalf("save delete replay: %v", err)
	}
	deleteReplay := request(t, server, http.MethodDelete, deletePath, nil, authHeaders(`"v1"`, deleteKey))
	if deleteReplay.Code != http.StatusNoContent {
		t.Fatalf("authorized delete replay: status=%d body=%s", deleteReplay.Code, deleteReplay.Body.String())
	}
	setTokenProjects(other.ID)
	denyReplay(http.MethodDelete, deletePath, nil, authHeaders(`"v1"`, deleteKey), "")
}
