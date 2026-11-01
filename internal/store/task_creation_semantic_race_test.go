package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSemanticStateRaceRejectsBugCreate(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceCREATEBUG")
	extra := semanticRaceColumn(t, f, "Bug create destination", "ready", 0)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	title := "create race bug"
	actual := "create race actual behavior"
	kind := bugKind

	_, err := semanticRaceMutation(t, f, extra.ID, "completed", func(ctx context.Context) (Task, error) {
		return f.store.CreateTask(ctx, f.project.ID, TaskInput{
			Title:    &title,
			Kind:     &kind,
			ColumnID: &extra.ID,
			Bug:      &BugInput{ActualBehavior: &actual},
		}, f.actor.ID)
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("bug create race error = %v, want ErrInvalid", err)
	}
	var taskCount int
	if err := f.store.DB.QueryRowContext(f.ctx, `SELECT COUNT(1) FROM tasks WHERE project_id=? AND title=?`, f.project.ID, title).Scan(&taskCount); err != nil {
		t.Fatalf("count rejected bug create: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("rejected bug create inserted %d tasks", taskCount)
	}
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected bug create emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "completed")
}

func TestSemanticStateRaceRejectsBugCreateToArchivedColumn(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMCREATEARCH")
	extra := semanticRaceColumn(t, f, "Archived bug create destination", "ready", 0)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	title := "create archive race bug"
	actual := "create archive race actual behavior"
	kind := bugKind

	_, err := semanticRaceMutationWithColumnUpdate(t, f, extra.ID, func(blocker *sql.Tx) error {
		_, err := blocker.ExecContext(f.ctx, `UPDATE columns SET archived_at=?, version=version+1, updated_at=? WHERE id=?`, now(), now(), extra.ID)
		return err
	}, func(ctx context.Context) (Task, error) {
		return f.store.CreateTask(ctx, f.project.ID, TaskInput{
			Title:    &title,
			Kind:     &kind,
			ColumnID: &extra.ID,
			Bug:      &BugInput{ActualBehavior: &actual},
		}, f.actor.ID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("bug create archive race error = %v, want ErrConflict", err)
	}
	var taskCount int
	if err := f.store.DB.QueryRowContext(f.ctx, `SELECT COUNT(1) FROM tasks WHERE project_id=? AND title=?`, f.project.ID, title).Scan(&taskCount); err != nil {
		t.Fatalf("count rejected archived bug create: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("rejected archived bug create inserted %d tasks", taskCount)
	}
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected archived bug create emitted events: %d -> %d", beforeEvents, got)
	}
	column, err := f.store.GetColumn(f.ctx, extra.ID)
	if err != nil {
		t.Fatalf("reload archived bug create destination: %v", err)
	}
	if column.ArchivedAt == nil {
		t.Fatalf("bug create race destination is not archived: %+v", column)
	}
}

func TestSemanticStateRaceCreateTaskDerivesCompletedAt(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMCTASKCREATE")
	extra := semanticRaceColumn(t, f, "Task create destination", "ready", 0)
	title := "create completed task race"
	beforeEvents := semanticRaceProjectEventCount(t, f)

	created, err := semanticRaceMutation(t, f, extra.ID, "completed", func(ctx context.Context) (Task, error) {
		return f.store.CreateTask(ctx, f.project.ID, TaskInput{
			Title:    &title,
			ColumnID: &extra.ID,
		}, f.actor.ID)
	})
	if err != nil {
		t.Fatalf("ordinary task create race: %v", err)
	}
	if created.ColumnID != extra.ID || created.CompletedAt == nil {
		t.Fatalf("ordinary task create race result = %+v, want destination %q with completed_at", created, extra.ID)
	}
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents+1 {
		t.Fatalf("ordinary task create race events = %d, want %d", got, beforeEvents+1)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "completed")
}

func TestSemanticStateRaceCreateTaskClearsCompletedAt(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMCTASKCLEAR")
	extra := semanticRaceColumn(t, f, "Task create ready destination", "completed", 0)
	title := "create ready task race"
	beforeEvents := semanticRaceProjectEventCount(t, f)

	created, err := semanticRaceMutation(t, f, extra.ID, "ready", func(ctx context.Context) (Task, error) {
		return f.store.CreateTask(ctx, f.project.ID, TaskInput{
			Title:    &title,
			ColumnID: &extra.ID,
		}, f.actor.ID)
	})
	if err != nil {
		t.Fatalf("ordinary task create ready race: %v", err)
	}
	if created.ColumnID != extra.ID || created.CompletedAt != nil {
		t.Fatalf("ordinary task create ready race result = %+v, want destination %q without completed_at", created, extra.ID)
	}
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents+1 {
		t.Fatalf("ordinary task create ready race events = %d, want %d", got, beforeEvents+1)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "ready")
}

func TestSemanticStateRaceResolveDuplicateWaitsForWriter(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceDUPLICATE")
	bug := semanticRaceBug(t, f, "", "duplicate race bug")
	target := semanticRaceBug(t, f, "", "duplicate target")
	completed, err := f.store.StateColumn(f.ctx, f.project.ID, "completed")
	if err != nil {
		t.Fatalf("read duplicate completion column: %v", err)
	}
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	_, err = semanticRaceMutation(t, f, completed.ID, "ready", func(ctx context.Context) (Task, error) {
		return f.store.ResolveBug(ctx, bug.ID, ResolveBugInput{
			Resolution:  "duplicate",
			DuplicateOf: &target.ID,
		}, bug.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate resolution race error = %v, want ErrConflict", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected duplicate resolution race emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, completed.ID, "ready")
}

type semanticRaceResult struct {
	task Task
	err  error
}
