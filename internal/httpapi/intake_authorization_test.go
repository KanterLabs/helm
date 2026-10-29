package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestIntakeProjectCeilingsHTTP(t *testing.T) {
	j, data := newIntakeJourney(t)
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("VIS"), Name: stringPtr("Visible")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("HID"), Name: stringPtr("Hidden")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := data.TicketQueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID, queue.ID} {
		if _, err := data.CreateTicket(ctx, id, store.TaskInput{Title: stringPtr("Synthetic request")}, actor.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := data.CreateTask(ctx, id, store.TaskInput{Title: stringPtr("Synthetic bug"), Kind: stringPtr("bug"), Bug: &store.BugInput{ActualBehavior: stringPtr("Synthetic failure")}}, actor.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Token initially includes the queue. Shrink the actor ceiling afterward
	// to prove reads use the live intersection rather than issuance-time grants.
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Synthetic Intake reader"}, actor.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, wideToken, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "wide", []string{"tasks:read"}, []string{first.ID, queue.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(ctx, `INSERT INTO actor_projects(actor_id,project_id) VALUES (?,?)`, agent.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	r := j.list("intersection", "?scope=all", wideToken)
	if len(r.Data) != 2 || r.Counts.All != 2 || r.Queue != nil {
		t.Fatalf("ceiling leak: %+v", r)
	}
	for _, task := range r.Data {
		if task.ProjectID != first.ID {
			t.Fatalf("leaked project: %+v", task)
		}
	}
	j.call("queue-denied", http.MethodGet, "/api/v1/intake?project="+queue.ID, nil, wideToken, 403)
	j.call("other-denied", http.MethodGet, "/api/v1/intake?project="+second.ID, nil, wideToken, 403)
	_, actorToken, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "actor-only", []string{"tasks:read"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := j.list("actor-only", "?scope=all", actorToken); got.Counts.All != 2 || got.Queue != nil {
		t.Fatalf("actor ceiling: %+v", got)
	}
	_, noRead, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "write-only", []string{"tasks:write"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	j.call("scope-denied", http.MethodGet, "/api/v1/intake", nil, noRead, 403)
	// An empty effective intersection is distinct from an unrestricted token.
	if _, err := data.DB.ExecContext(ctx, `DELETE FROM actor_projects WHERE actor_id=?`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(ctx, `INSERT INTO actor_projects(actor_id,project_id) VALUES (?,?)`, agent.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if got := j.list("empty-intersection", "?scope=all", wideToken); got.Counts.All != 0 || len(got.Data) != 0 || got.Queue != nil {
		t.Fatalf("empty intersection widened: %+v", got)
	}
	if _, err := data.DB.ExecContext(ctx, `UPDATE actors SET disabled_at='2026-10-29T00:00:00Z' WHERE id=?`, agent.ID); err != nil {
		t.Fatal(err)
	}
	j.call("disabled-actor", http.MethodGet, "/api/v1/intake", nil, wideToken, 401)
}

func TestIntakeAlertHistoryAndCompletionHTTP(t *testing.T) {
	j, data := newIntakeJourney(t)
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("DST"), Name: stringPtr("Destination")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	route := store.AlertIntakeRoute{Integration: "synthetic", ActorName: "Synthetic intake"}
	alerts := []store.IntakeAlert{{AlertType: "health", FamilyKey: "synthetic-family", ConditionKey: "synthetic-condition", ResourceName: "Synthetic service", ResourceID: "synthetic-resource", Title: "Synthetic service unhealthy", Priority: "high", Evidence: map[string]string{"status": "unhealthy"}}}
	first, err := data.IngestAlerts(ctx, route, alerts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.IngestAlerts(ctx, route, alerts); err != nil {
		t.Fatal(err)
	}
	id, oldKey := first[0].TaskID, first[0].TaskKey
	j.call("file-alert", "POST", "/api/v1/tickets/"+id+"/file", map[string]any{"project": project.ID}, "", 200)
	if _, err := data.IngestAlerts(ctx, route, alerts); err != nil {
		t.Fatal(err)
	}
	var completed store.Task
	if err := json.Unmarshal(j.call("complete", "POST", "/api/v1/tasks/"+id+"/complete", map[string]any{}, "", 200), &completed); err != nil {
		t.Fatal(err)
	}
	before := intakeDomainSnapshot(t, data)
	all := j.list("completed-all", "?scope=all&q="+oldKey, "")
	if len(all.Data) != 1 || all.Counts.All != 1 || all.Counts.Triage != 0 || all.Data[0].ID != id || all.Data[0].Ticket == nil || all.Data[0].Kind != "task" || all.Data[0].AlertSource == nil || all.Data[0].AlertSource.OccurrenceCount != 3 {
		t.Fatalf("lost source/filed completion: %+v", all)
	}
	triage := j.list("completed-triage", "", "")
	if len(triage.Data) != 0 {
		t.Fatalf("completed in triage: %+v", triage)
	}
	if after := intakeDomainSnapshot(t, data); !reflect.DeepEqual(before, after) {
		t.Fatal("read mutated source or domain history")
	}
	// Archived projects and deleted tasks never affect either scope/counts.
	if _, err := data.DB.ExecContext(ctx, `UPDATE projects SET archived_at='2026-10-29T00:00:00Z' WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if r := j.list("archived", "?scope=all", ""); r.Counts.All != 0 || len(r.Data) != 0 {
		t.Fatalf("archived leak: %+v", r)
	}
	if _, err := data.DB.ExecContext(ctx, `UPDATE projects SET archived_at=NULL WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(ctx, `UPDATE tasks SET deleted_at='2026-10-29T00:00:00Z' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if r := j.list("deleted", "?scope=all", ""); r.Counts.All != 0 || len(r.Data) != 0 {
		t.Fatalf("deleted leak: %+v", r)
	}
	// A storage failure must not masquerade as an empty successful page.
	if _, err := data.DB.ExecContext(ctx, `ALTER TABLE tickets RENAME TO unavailable_tickets`); err != nil {
		t.Fatal(err)
	}
	j.call("database-error", "GET", "/api/v1/intake", nil, "", 500)
}
