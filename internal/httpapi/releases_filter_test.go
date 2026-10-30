package httpapi

import (
	"context"
	"encoding/json"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseFiltersApplyBeforePaginationAcrossGlobalCollections(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		filterUnassign bool
	}{
		{name: "release_id", filterUnassign: false},
		{name: "unassigned", filterUnassign: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, data := testServer(t, "disabled")
			ctx := context.Background()
			actor, err := data.EnsureDisabledActor(ctx)
			if err != nil {
				t.Fatalf("ensure disabled actor: %v", err)
			}
			project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("FILTER" + testCase.name), Name: stringPtr("Release filter " + testCase.name)}, actor.ID)
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			releaseName := "1.0"
			release, err := data.CreateRelease(ctx, project.ID, store.ReleaseInput{Name: &releaseName}, actor.ID)
			if err != nil {
				t.Fatalf("create release: %v", err)
			}
			filterValue := release.ID
			if testCase.filterUnassign {
				filterValue = "unassigned"
			}

			// Create the nonmatching task first. Each collection's ordering then
			// puts it ahead of the matching row unless the SQL release predicate
			// is applied before LIMIT/OFFSET.
			var noiseReleaseID, targetReleaseID *string
			if testCase.filterUnassign {
				noiseReleaseID = &release.ID
			} else {
				targetReleaseID = &release.ID
			}
			noiseTitle := "A release filter noise"
			targetTitle := "B release filter target"
			noise, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &noiseTitle, ReleaseID: noiseReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create search noise: %v", err)
			}
			target, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &targetTitle, ReleaseID: targetReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create search target: %v", err)
			}

			search := request(t, server, http.MethodGet, "/api/v1/search?release_id="+filterValue+"&sort=title:asc&limit=1", nil, nil)
			assertReleaseFilterPage(t, search, target.ID, "global search")

			viewResponse := request(t, server, http.MethodPost, "/api/v1/views", map[string]any{
				"name":    "Release filter view " + testCase.name,
				"filters": map[string]any{"release_id": filterValue},
				"sort":    []map[string]string{{"field": "title", "direction": "asc"}},
			}, map[string]string{"Content-Type": "application/json", "Idempotency-Key": "release-filter-view-" + testCase.name})
			if viewResponse.Code != http.StatusCreated {
				t.Fatalf("create release saved view: status=%d body=%s", viewResponse.Code, viewResponse.Body.String())
			}
			var view store.SavedView
			if err := json.Unmarshal(viewResponse.Body.Bytes(), &view); err != nil {
				t.Fatalf("decode release saved view: %v", err)
			}
			viewSearch := request(t, server, http.MethodGet, "/api/v1/views/"+view.ID+"/search?limit=1", nil, nil)
			assertReleaseFilterPage(t, viewSearch, target.ID, "saved view search")
			if strings.Contains(viewSearch.Body.String(), noise.ID) {
				t.Fatalf("saved view release filter leaked noise task: %s", viewSearch.Body.String())
			}

			assignee := actor.ID
			myWorkNoiseTitle := "A my-work release filter noise"
			myWorkTargetTitle := "B my-work release filter target"
			myWorkNoise, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &myWorkNoiseTitle, Assignee: &assignee, AssigneeSet: true, ReleaseID: noiseReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create my-work noise: %v", err)
			}
			myWorkTarget, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &myWorkTargetTitle, Assignee: &assignee, AssigneeSet: true, ReleaseID: targetReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create my-work target: %v", err)
			}
			myWork := request(t, server, http.MethodGet, "/api/v1/my-work?project="+project.Key+"&release_id="+filterValue+"&limit=1", nil, nil)
			assertReleaseFilterPage(t, myWork, myWorkTarget.ID, "my work")
			if strings.Contains(myWork.Body.String(), myWorkNoise.ID) {
				t.Fatalf("my-work release filter leaked noise task: %s", myWork.Body.String())
			}

			actual := "release filter test failure"
			bugKind := "bug"
			issueNoiseTitle := "A issue release filter noise"
			issueTargetTitle := "B issue release filter target"
			issueNoise, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &issueNoiseTitle, Kind: &bugKind, Bug: &store.BugInput{ActualBehavior: &actual}, ReleaseID: noiseReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create issue noise: %v", err)
			}
			issueTarget, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: &issueTargetTitle, Kind: &bugKind, Bug: &store.BugInput{ActualBehavior: &actual}, ReleaseID: targetReleaseID}, actor.ID)
			if err != nil {
				t.Fatalf("create issue target: %v", err)
			}
			issues := request(t, server, http.MethodGet, "/api/v1/issues?release_id="+filterValue+"&limit=1", nil, nil)
			assertReleaseFilterPage(t, issues, issueTarget.ID, "issues")
			if strings.Contains(issues.Body.String(), issueNoise.ID) {
				t.Fatalf("issue release filter leaked noise task: %s", issues.Body.String())
			}

		})
	}
}

func assertReleaseFilterPage(t *testing.T, response *httptest.ResponseRecorder, expectedID, collection string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", collection, response.Code, response.Body.String())
	}
	var page struct {
		Data       []store.Task `json:"data"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode %s page: %v; body=%s", collection, err, response.Body.String())
	}
	if len(page.Data) != 1 || page.Data[0].ID != expectedID || page.NextCursor != "" {
		t.Fatalf("%s page=%+v, want only matching task %s without cursor", collection, page, expectedID)
	}
}
