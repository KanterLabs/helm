package store

import (
	"context"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

func TestProjectIntelligenceSnapshotAggregatesAndScopes(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := New(database)
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Reader"}, "")
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	visible, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("INTEL"), Name: stringPtrForTest("Intelligence")}, actor.ID)
	if err != nil {
		t.Fatalf("create visible project: %v", err)
	}
	hidden, err := data.CreateProject(ctx, ProjectInput{Key: stringPtrForTest("HIDDEN"), Name: stringPtrForTest("Hidden")}, actor.ID)
	if err != nil {
		t.Fatalf("create hidden project: %v", err)
	}
	active, err := data.StateColumn(ctx, visible.ID, "active")
	if err != nil {
		t.Fatalf("active column: %v", err)
	}
	priority := "urgent"
	task, err := data.CreateTask(ctx, visible.ID, TaskInput{Title: stringPtrForTest("Needs attention"), ColumnID: &active.ID, Priority: &priority}, actor.ID)
	if err != nil {
		t.Fatalf("create active task: %v", err)
	}
	asOf := time.Now().UTC().Truncate(time.Second)
	if _, err := database.ExecContext(ctx, `UPDATE tasks SET due_at=?, created_at=? WHERE id=?`, asOf.Add(-time.Hour).Format(time.RFC3339Nano), asOf.Add(-3*24*time.Hour).Format(time.RFC3339Nano), task.ID); err != nil {
		t.Fatalf("age task: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO task_agent_work(task_id, operation_id, actor_id, state, phase, summary, next_action, checkpoint_refs, started_at, updated_at) VALUES (?, 'intel-test', ?, 'handoff', '', 'Ready', '', '[]', ?, ?)`, task.ID, actor.ID, asOf.Add(-time.Hour).Format(time.RFC3339Nano), asOf.Add(-time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert work: %v", err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO notifications(id, recipient_id, actor_id, event_type, project_id, task_id, title, body, payload, dedupe_key, created_at, updated_at) VALUES ('intel-notification', ?, ?, 'task.blocked', ?, ?, 'Blocked', '', '{}', 'intel-notification', ?, ?)`, actor.ID, actor.ID, visible.ID, task.ID, asOf.Format(time.RFC3339Nano), asOf.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert notification: %v", err)
	}

	projects, err := data.ProjectIntelligenceSnapshot(ctx, actor.ID, []string{visible.ID}, asOf)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != visible.ID {
		t.Fatalf("scoped projects = %+v, hidden=%s", projects, hidden.ID)
	}
	metrics := projects[0].Metrics
	if metrics.OpenTasks != 1 || metrics.ActiveTasks != 1 || metrics.OverdueTasks != 1 || metrics.UrgentTasks != 1 {
		t.Fatalf("task metrics = %+v", metrics)
	}
	if metrics.ActionNeeded != 1 || metrics.StaleAgentWork != 1 || metrics.UnreadNotifications != 1 {
		t.Fatalf("attention metrics = %+v", metrics)
	}
	if metrics.Created7d != 1 || metrics.NetOpenChange7d != 1 || metrics.LastActivityAt == nil {
		t.Fatalf("event metrics = %+v", metrics)
	}
	if metrics.OldestActiveDays == nil || *metrics.OldestActiveDays < 2.9 {
		t.Fatalf("oldest active = %v", metrics.OldestActiveDays)
	}

	empty, err := data.ProjectIntelligenceSnapshot(ctx, actor.ID, []string{}, asOf)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty ceiling = %+v err=%v", empty, err)
	}
}
