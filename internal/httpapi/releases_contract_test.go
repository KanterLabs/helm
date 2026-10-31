package httpapi

import (
	"context"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestReleaseHTTPContractLifecycleFiltersAndRedaction(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatalf("ensure disabled actor: %v", err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("RELAPI"), Name: stringPtr("Release API")}, actor.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	for index, reservedName := range []string{"unassigned", "NONE"} {
		reserved := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/releases", map[string]any{
			"name": reservedName,
		}, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "reserved-release-name-" + strconv.Itoa(index)})
		if reserved.Code != http.StatusBadRequest || responseErrorCode(t, reserved.Body.Bytes()) != "invalid_request" {
			t.Fatalf("reserved release name %q: status=%d body=%s", reservedName, reserved.Code, reserved.Body.String())
		}
	}

	created := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/releases", map[string]any{
		"name":        "1.4",
		"description": "release secret description",
		"target_date": "2030-01-02",
	}, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "release-create"})
	if created.Code != http.StatusCreated || created.Header().Get("ETag") != `"v1"` {
		t.Fatalf("create release: status=%d etag=%q body=%s", created.Code, created.Header().Get("ETag"), created.Body.String())
	}
	var release store.Release
	if err := json.Unmarshal(created.Body.Bytes(), &release); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	if release.Name != "1.4" || release.Status != "planned" || release.TargetDate == nil || *release.TargetDate != "2030-01-02" {
		t.Fatalf("created release = %+v", release)
	}
	replay := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/releases", map[string]any{
		"name":        "1.4",
		"description": "release secret description",
		"target_date": "2030-01-02",
	}, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "release-create"})
	if replay.Code != created.Code || replay.Body.String() != created.Body.String() {
		t.Fatalf("release replay changed response: first=%d/%s replay=%d/%s", created.Code, created.Body.String(), replay.Code, replay.Body.String())
	}

	patch := request(t, server, http.MethodPatch, "/api/v1/releases/"+release.ID, map[string]any{"name": "1.4.1"}, map[string]string{
		"Content-Type":    "application/json",
		"If-Match":        `"v1"`,
		"Idempotency-Key": "release-patch",
	})
	if patch.Code != http.StatusOK || patch.Header().Get("ETag") != `"v2"` {
		t.Fatalf("patch release: status=%d etag=%q body=%s", patch.Code, patch.Header().Get("ETag"), patch.Body.String())
	}
	malformedReplay := request(t, server, http.MethodPatch, "/api/v1/releases/"+release.ID, map[string]any{"name": "1.4.1"}, map[string]string{
		"Content-Type":    "application/json",
		"If-Match":        `"invalid"`,
		"Idempotency-Key": "release-patch",
	})
	if malformedReplay.Code != http.StatusBadRequest {
		t.Fatalf("malformed If-Match replay: status=%d body=%s", malformedReplay.Code, malformedReplay.Body.String())
	}
	validReplay := request(t, server, http.MethodPatch, "/api/v1/releases/"+release.ID, map[string]any{"name": "1.4.1"}, map[string]string{
		"Content-Type":    "application/json",
		"If-Match":        `"v1"`,
		"Idempotency-Key": "release-patch",
	})
	if validReplay.Code != patch.Code || validReplay.Body.String() != patch.Body.String() || validReplay.Header().Get("ETag") != patch.Header().Get("ETag") {
		t.Fatalf("valid patch replay changed response: first=%d/%s replay=%d/%s", patch.Code, patch.Body.String(), validReplay.Code, validReplay.Body.String())
	}
	release.Version = 2
	release.Name = "1.4.1"

	bound := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks", map[string]any{
		"title":      "bound task",
		"release_id": release.ID,
	}, map[string]string{"Content-Type": "application/json"})
	if bound.Code != http.StatusCreated {
		t.Fatalf("create bound task: status=%d body=%s", bound.Code, bound.Body.String())
	}
	var boundTask store.Task
	if err := json.Unmarshal(bound.Body.Bytes(), &boundTask); err != nil {
		t.Fatalf("decode bound task: %v", err)
	}
	if boundTask.ReleaseID == nil || *boundTask.ReleaseID != release.ID || boundTask.Release == nil || boundTask.Release.Name != release.Name {
		t.Fatalf("bound task release = %+v", boundTask)
	}
	unassigned := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks", map[string]any{"title": "unassigned task"}, map[string]string{"Content-Type": "application/json"})
	if unassigned.Code != http.StatusCreated {
		t.Fatalf("create unassigned task: status=%d body=%s", unassigned.Code, unassigned.Body.String())
	}
	queue := request(t, server, http.MethodGet, "/api/v1/releases/"+release.ID+"/work-queue", nil, nil)
	if queue.Code != http.StatusOK || !strings.Contains(queue.Body.String(), boundTask.ID) {
		t.Fatalf("release work queue: status=%d body=%s", queue.Code, queue.Body.String())
	}
	emptyCursor := request(t, server, http.MethodGet, "/api/v1/releases/"+release.ID+"/work-queue?cursor=", nil, nil)
	if emptyCursor.Code != http.StatusBadRequest || responseErrorCode(t, emptyCursor.Body.Bytes()) != "invalid_request" || !strings.Contains(emptyCursor.Body.String(), "cursor must not be empty") {
		t.Fatalf("empty release work queue cursor: status=%d body=%s", emptyCursor.Code, emptyCursor.Body.String())
	}

	byName := request(t, server, http.MethodGet, "/api/v1/projects/"+project.Key+"/tasks?release=1.4.1", nil, nil)
	if byName.Code != http.StatusOK || !strings.Contains(byName.Body.String(), boundTask.ID) || strings.Contains(byName.Body.String(), "unassigned task") {
		t.Fatalf("name release filter: status=%d body=%s", byName.Code, byName.Body.String())
	}
	withoutRelease := request(t, server, http.MethodGet, "/api/v1/projects/"+project.Key+"/tasks?release=unassigned", nil, nil)
	if withoutRelease.Code != http.StatusOK || !strings.Contains(withoutRelease.Body.String(), "unassigned task") || strings.Contains(withoutRelease.Body.String(), boundTask.ID) {
		t.Fatalf("unassigned release filter: status=%d body=%s", withoutRelease.Code, withoutRelease.Body.String())
	}

	completeIncomplete := request(t, server, http.MethodPost, "/api/v1/releases/"+release.ID+"/complete", nil, map[string]string{
		"If-Match":        `"v2"`,
		"Idempotency-Key": "release-complete-incomplete",
	})
	if completeIncomplete.Code != http.StatusConflict || responseErrorCode(t, completeIncomplete.Body.Bytes()) != "release_incomplete" {
		t.Fatalf("incomplete release completion: status=%d body=%s", completeIncomplete.Code, completeIncomplete.Body.String())
	}

	writer, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "release writer", ProjectIDs: []string{project.ID}}, actor.ID, "")
	if err != nil {
		t.Fatalf("create release writer: %v", err)
	}
	_, writeToken, err := data.CreateTokenBy(ctx, writer.ID, actor.ID, "release writer", []string{"tasks:write"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatalf("create release writer token: %v", err)
	}
	redacted := request(t, server, http.MethodPost, "/api/v1/releases/"+release.ID+"/complete", nil, map[string]string{
		"Authorization":   "Bearer " + writeToken,
		"If-Match":        `"v2"`,
		"Idempotency-Key": "release-complete-redacted",
	})
	if redacted.Code != http.StatusConflict || responseErrorCode(t, redacted.Body.Bytes()) != "release_incomplete" {
		t.Fatalf("write-only completion: status=%d body=%s", redacted.Code, redacted.Body.String())
	}
	if strings.Contains(redacted.Body.String(), "release secret description") || strings.Contains(redacted.Body.String(), `"summary"`) {
		t.Fatalf("write-only release conflict leaked details: %s", redacted.Body.String())
	}

	missingIfMatch := request(t, server, http.MethodPatch, "/api/v1/releases/"+release.ID, map[string]any{"name": "new"}, map[string]string{"Idempotency-Key": "release-no-etag"})
	if missingIfMatch.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing release If-Match status=%d body=%s", missingIfMatch.Code, missingIfMatch.Body.String())
	}
	missingIdempotency := request(t, server, http.MethodPatch, "/api/v1/releases/"+release.ID, map[string]any{"name": "new"}, map[string]string{"If-Match": `"v2"`})
	if missingIdempotency.Code != http.StatusBadRequest {
		t.Fatalf("missing release idempotency status=%d body=%s", missingIdempotency.Code, missingIdempotency.Body.String())
	}

	deleteWithTasks := request(t, server, http.MethodDelete, "/api/v1/releases/"+release.ID, nil, map[string]string{
		"If-Match":        `"v2"`,
		"Idempotency-Key": "release-delete-with-tasks",
	})
	if deleteWithTasks.Code != http.StatusConflict || responseErrorCode(t, deleteWithTasks.Body.Bytes()) != "release_has_tasks" {
		t.Fatalf("delete non-empty release: status=%d body=%s", deleteWithTasks.Code, deleteWithTasks.Body.String())
	}
}

func TestReleaseSavedViewFiltersRespectProjectCeiling(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatalf("ensure disabled actor: %v", err)
	}
	allowed, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("RELALLOW"), Name: stringPtr("Release allowed")}, actor.ID)
	if err != nil {
		t.Fatalf("create allowed project: %v", err)
	}
	blocked, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("RELBLOCK"), Name: stringPtr("Release blocked")}, actor.ID)
	if err != nil {
		t.Fatalf("create blocked project: %v", err)
	}
	allowedName, blockedName := "1.0", "2.0"
	allowedRelease, err := data.CreateRelease(ctx, allowed.ID, store.ReleaseInput{Name: &allowedName}, actor.ID)
	if err != nil {
		t.Fatalf("create allowed release: %v", err)
	}
	blockedRelease, err := data.CreateRelease(ctx, blocked.ID, store.ReleaseInput{Name: &blockedName}, actor.ID)
	if err != nil {
		t.Fatalf("create blocked release: %v", err)
	}
	targetTitle := "Allowed release task"
	if _, err := data.CreateTask(ctx, allowed.ID, store.TaskInput{Title: &targetTitle, ReleaseID: &allowedRelease.ID}, actor.ID); err != nil {
		t.Fatalf("create allowed release task: %v", err)
	}

	reader, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Release filter reader", ProjectIDs: []string{allowed.ID}}, actor.ID, "")
	if err != nil {
		t.Fatalf("create scoped reader: %v", err)
	}
	_, token, err := data.CreateTokenBy(ctx, reader.ID, actor.ID, "release-filter-reader", []string{"tasks:read", "tasks:write"}, []string{allowed.ID}, nil)
	if err != nil {
		t.Fatalf("create scoped reader token: %v", err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}

	allowedView := request(t, server, http.MethodPost, "/api/v1/views", map[string]any{
		"name":    "Allowed release view",
		"filters": map[string]any{"release_id": allowedRelease.ID},
	}, mergeHeaders(headers, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "allowed-release-view"}))
	if allowedView.Code != http.StatusCreated {
		t.Fatalf("allowed release saved view: status=%d body=%s", allowedView.Code, allowedView.Body.String())
	}
	var allowedViewResource store.SavedView
	if err := json.Unmarshal(allowedView.Body.Bytes(), &allowedViewResource); err != nil {
		t.Fatalf("decode allowed release view: %v", err)
	}
	allowedSearch := request(t, server, http.MethodGet, "/api/v1/views/"+allowedViewResource.ID+"/search?limit=1", nil, headers)
	if allowedSearch.Code != http.StatusOK || !strings.Contains(allowedSearch.Body.String(), targetTitle) {
		t.Fatalf("allowed release view search: status=%d body=%s", allowedSearch.Code, allowedSearch.Body.String())
	}

	blockedView := request(t, server, http.MethodPost, "/api/v1/views", map[string]any{
		"name":    "Blocked release view",
		"filters": map[string]any{"release_id": blockedRelease.ID},
	}, mergeHeaders(headers, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "blocked-release-view"}))
	if blockedView.Code != http.StatusForbidden || responseErrorCode(t, blockedView.Body.Bytes()) != "forbidden" {
		t.Fatalf("blocked release saved view: status=%d body=%s", blockedView.Code, blockedView.Body.String())
	}

	shared := true
	hiddenView, err := data.CreateSavedView(ctx, actor.ID, store.SavedViewInput{
		Name:       stringPtr("Hidden blocked release view"),
		Filters:    map[string]any{"release_id": blockedRelease.ID},
		FiltersSet: true,
		Shared:     &shared,
	})
	if err != nil {
		t.Fatalf("create hidden shared view: %v", err)
	}
	visibleViews := request(t, server, http.MethodGet, "/api/v1/views", nil, headers)
	if visibleViews.Code != http.StatusOK || strings.Contains(visibleViews.Body.String(), hiddenView.ID) || strings.Contains(visibleViews.Body.String(), hiddenView.Name) {
		t.Fatalf("scoped saved view listing leaked blocked release: status=%d body=%s", visibleViews.Code, visibleViews.Body.String())
	}
}

func mergeHeaders(base, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
