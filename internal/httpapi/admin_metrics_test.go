package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

func TestAdminMetricsAggregatesActivityAndRequests(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	project := request(t, server, http.MethodPost, "/api/v1/projects", map[string]any{"key": "OPS", "name": "Operations"}, map[string]string{"Content-Type": "application/json"})
	if project.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", project.Code, project.Body.String())
	}
	for _, title := range []string{"one", "two"} {
		created := request(t, server, http.MethodPost, "/api/v1/projects/OPS/tasks", map[string]any{"title": title}, map[string]string{"Content-Type": "application/json"})
		if created.Code != http.StatusCreated {
			t.Fatalf("create task: %d %s", created.Code, created.Body.String())
		}
	}
	agent, err := data.CreateActor(ctx, store.Actor{Kind: "agent", Name: "builder"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateToken(ctx, agent.ID, "metrics", []string{"projects:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + token}
	if response := request(t, server, http.MethodGet, "/api/v1/projects", nil, bearer); response.Code != http.StatusOK {
		t.Fatalf("agent list projects: %d %s", response.Code, response.Body.String())
	}

	response := request(t, server, http.MethodGet, "/api/v1/admin/metrics?days=7", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("admin metrics: %d %s", response.Code, response.Body.String())
	}
	var body adminMetricsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.WindowDays != 7 || len(body.Daily) != 7 {
		t.Fatalf("window = %d days, daily = %d entries", body.WindowDays, len(body.Daily))
	}
	if body.Activity.TasksCreated != 2 || body.Totals.TasksOpen != 2 || body.Totals.Projects != 1 {
		t.Fatalf("activity = %+v totals = %+v", body.Activity, body.Totals)
	}
	if last := body.Daily[len(body.Daily)-1]; last.Date != time.Now().UTC().Format("2006-01-02") || last.TasksCreated != 2 {
		t.Fatalf("today = %+v", last)
	}
	if body.Requests.Agent != 1 || len(body.Requests.Hourly) != adminActivityHours {
		t.Fatalf("requests = %+v", body.Requests.adminActivityCounts)
	}
	found := false
	for _, actor := range body.Requests.TopActors {
		if actor.ActorID == agent.ID && actor.Name == "builder" && actor.Requests == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent missing from top actors: %+v", body.Requests.TopActors)
	}
	if len(body.Agents) != 1 || body.Agents[0].ActorID != agent.ID || body.Agents[0].TokenLastUsedAt == nil {
		t.Fatalf("agents = %+v", body.Agents)
	}

	// Administrative metrics are human-only even for a token holder.
	if denied := request(t, server, http.MethodGet, "/api/v1/admin/metrics", nil, bearer); denied.Code != http.StatusForbidden {
		t.Fatalf("bearer admin metrics status = %d", denied.Code)
	}
	if invalid := request(t, server, http.MethodGet, "/api/v1/admin/metrics?days=91", nil, nil); invalid.Code != http.StatusBadRequest {
		t.Fatalf("days=91 status = %d", invalid.Code)
	}
}

func TestAdminActivityTrackerBoundsActors(t *testing.T) {
	tracker := newAdminActivityTracker()
	at := time.Now()
	for index := 0; index < adminActivityMaxActors+10; index++ {
		identity := auth.Identity{Actor: store.Actor{ID: "actor-" + time.Duration(index).String(), Kind: "agent"}}
		tracker.record(identity, true, http.StatusOK, time.Millisecond, at)
	}
	tracker.record(auth.Identity{}, false, http.StatusUnauthorized, 0, at)
	snapshot := tracker.snapshot(at)
	if len(tracker.actors) != adminActivityMaxActors {
		t.Fatalf("tracked actors = %d", len(tracker.actors))
	}
	if snapshot.Total != adminActivityMaxActors+11 || snapshot.Anonymous != 1 || snapshot.Errors != 1 {
		t.Fatalf("totals = %+v", snapshot.adminActivityCounts)
	}
	if got := snapshot.Hourly[len(snapshot.Hourly)-1].Total; got != snapshot.Total {
		t.Fatalf("current hour total = %d, want %d", got, snapshot.Total)
	}
}
