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
	if body.Requests.Agent != 1 || len(body.Requests.Hourly) != adminActivityHours || len(body.Requests.Daily) != 7 {
		t.Fatalf("requests = %+v hourly=%d daily=%d", body.Requests.RequestCounts, len(body.Requests.Hourly), len(body.Requests.Daily))
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

func TestRequestActivitySurvivesRestart(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	agent, err := data.CreateActor(ctx, store.Actor{Kind: "agent", Name: "builder"}, "")
	if err != nil {
		t.Fatal(err)
	}
	identity := auth.Identity{Actor: agent, IsToken: true}
	at := time.Now()
	for index := 0; index < 3; index++ {
		server.adminActivityValue().record(identity, true, http.StatusOK, 10*time.Millisecond, at)
	}
	server.adminActivityValue().record(identity, true, http.StatusConflict, 0, at)
	if err := server.FlushRequestActivity(ctx); err != nil {
		t.Fatal(err)
	}

	// A new server on the same database stands in for a restart or deploy.
	restarted := New(data, server.Auth, server.Cfg)
	restarted.adminActivityValue().record(identity, true, http.StatusOK, 0, at)
	response := request(t, restarted, http.MethodGet, "/api/v1/admin/metrics?days=1", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("admin metrics: %d %s", response.Code, response.Body.String())
	}
	var body adminMetricsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var builder *store.RequestActor
	for index := range body.Requests.TopActors {
		if body.Requests.TopActors[index].ActorID == agent.ID {
			builder = &body.Requests.TopActors[index]
		}
	}
	if builder == nil || builder.Requests != 5 || builder.Errors != 1 || builder.Name != "builder" || builder.Kind != "agent" {
		t.Fatalf("persisted agent requests = %+v", builder)
	}
	if body.Requests.Agent != 5 {
		t.Fatalf("agent total = %d, want 5", body.Requests.Agent)
	}
}

func TestRequestActivityFlushRetriesWithoutDoubleCounting(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	tracker := newAdminActivityTracker()
	at := time.Now()
	tracker.record(auth.Identity{}, false, http.StatusUnauthorized, 0, at)

	// A canceled context makes the write fail; the batch must be kept.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := tracker.flush(canceled, data, at); err == nil {
		t.Fatal("flush with canceled context succeeded")
	}
	tracker.record(auth.Identity{}, false, http.StatusOK, 0, at)
	if err := tracker.flush(ctx, data, at); err != nil {
		t.Fatal(err)
	}
	if err := tracker.flush(ctx, data, at); err != nil {
		t.Fatal(err)
	}
	var requests, errors int
	if err := data.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(requests),0), COALESCE(SUM(errors),0) FROM request_activity_hourly`).Scan(&requests, &errors); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || errors != 1 {
		t.Fatalf("persisted requests=%d errors=%d, want 2 and 1", requests, errors)
	}
}

func TestRequestActivityPrunesOldRows(t *testing.T) {
	_, data := testServer(t, "disabled")
	ctx := context.Background()
	now := time.Now()
	old := store.RequestActivityDelta{Hour: now.Add(-100 * 24 * time.Hour), ActorKind: "anonymous", Requests: 1, LastSeenAt: now.Add(-100 * 24 * time.Hour)}
	if err := data.AddRequestActivity(ctx, []store.RequestActivityDelta{old}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	tracker := newAdminActivityTracker()
	tracker.record(auth.Identity{}, false, http.StatusOK, 0, now)
	if err := tracker.flush(ctx, data, now); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := data.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM request_activity_hourly`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows after prune = %d, want 1", rows)
	}
}

func TestAdminActivityTrackerBoundsPending(t *testing.T) {
	tracker := newAdminActivityTracker()
	at := time.Now()
	for index := 0; index < adminActivityMaxPending+10; index++ {
		identity := auth.Identity{Actor: store.Actor{ID: "actor-" + time.Duration(index).String(), Kind: "agent"}}
		tracker.record(identity, true, http.StatusOK, time.Millisecond, at)
	}
	if len(tracker.pending) != adminActivityMaxPending {
		t.Fatalf("pending keys = %d", len(tracker.pending))
	}
}
