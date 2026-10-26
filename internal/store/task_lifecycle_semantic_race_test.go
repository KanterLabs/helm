package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSemanticStateRaceRejectsGenericBugPatch(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAcePATCH")
	bug := semanticRaceBug(t, f, "", "patch race bug")
	completed, err := f.store.StateColumn(f.ctx, f.project.ID, "completed")
	if err != nil {
		t.Fatalf("read completed column: %v", err)
	}
	resolved, err := f.store.ResolveBug(f.ctx, bug.ID, ResolveBugInput{Resolution: "fixed"}, bug.Version, f.actor.ID)
	if err != nil {
		t.Fatalf("resolve patch race bug: %v", err)
	}
	extra := semanticRaceColumn(t, f, "Patch destination", "completed", 0)
	if extra.ID == completed.ID {
		t.Fatalf("extra patch destination reused source column %q", extra.ID)
	}
	beforeTask := dependencyDurableState(t, f, resolved.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	title := "patch after destination reclassification"

	_, err = semanticRaceMutation(t, f, extra.ID, "ready", func(ctx context.Context) (Task, error) {
		return f.store.UpdateTask(ctx, resolved.ID, TaskInput{ColumnID: &extra.ID, Title: &title}, resolved.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("generic bug patch race error = %v, want ErrInvalid", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, resolved.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected generic bug patch emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "ready")
}

func TestSemanticStateRaceRejectsBugMove(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceMOVE")
	bug := semanticRaceBug(t, f, "", "move race bug")
	extra := semanticRaceColumn(t, f, "Move destination", "ready", 0)
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	_, err := semanticRaceMutation(t, f, extra.ID, "completed", func(ctx context.Context) (Task, error) {
		return f.store.MoveTask(ctx, bug.ID, TaskMoveInput{
			DestinationColumnID:    extra.ID,
			ExpectedSourceColumnID: bug.ColumnID,
			Source:                 "semantic-race-test",
		}, bug.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("bug move race error = %v, want ErrConflict", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected bug move emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "completed")
}

func TestSemanticStateRaceRejectsCompleteTask(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceCOMPLETE")
	task := f.task(t, "complete race task")
	extra := semanticRaceColumn(t, f, "Complete destination", "completed", 0)
	beforeTask := dependencyDurableState(t, f, task.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	_, err := semanticRaceMutation(t, f, extra.ID, "ready", func(ctx context.Context) (Task, error) {
		return f.store.CompleteTask(ctx, task.ID, f.actor.ID, task.Version)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("complete task race error = %v, want ErrConflict", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, task.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected complete task emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "ready")
}

func TestSemanticStateRaceRejectsResolveBug(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceRESOLVE")
	extra := semanticRaceColumn(t, f, "Resolve destination", "completed", 0)
	bug := semanticRaceBug(t, f, "", "resolve race bug")
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	_, err := semanticRaceMutation(t, f, extra.ID, "ready", func(ctx context.Context) (Task, error) {
		return f.store.ResolveBug(ctx, bug.ID, ResolveBugInput{Resolution: "fixed", Note: "must roll back"}, bug.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("resolve bug race error = %v, want ErrConflict", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected resolve bug emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "ready")
}

func TestSemanticStateRaceRejectsReopenBug(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceREOPEN")
	bug := semanticRaceBug(t, f, "", "reopen race bug")
	resolved, err := f.store.ResolveBug(f.ctx, bug.ID, ResolveBugInput{Resolution: "fixed"}, bug.Version, f.actor.ID)
	if err != nil {
		t.Fatalf("resolve reopen race bug: %v", err)
	}
	extra, err := f.store.GetColumn(f.ctx, bug.ColumnID)
	if err != nil {
		t.Fatalf("read reopen destination: %v", err)
	}
	beforeTask := dependencyDurableState(t, f, resolved.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	_, err = semanticRaceMutation(t, f, extra.ID, "completed", func(ctx context.Context) (Task, error) {
		return f.store.ReopenBug(ctx, resolved.ID, "must roll back", resolved.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("reopen bug race error = %v, want ErrConflict", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, resolved.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected reopen bug emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "completed")
}

func TestSemanticStateRaceRejectsBugTriage(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceTRIAGE")
	bug := semanticRaceBug(t, f, "", "triage race bug")
	extra := semanticRaceColumn(t, f, "Triage destination", "ready", 0)
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	severity := "s1"

	_, err := semanticRaceMutation(t, f, extra.ID, "completed", func(ctx context.Context) (Task, error) {
		return f.store.TriageBug(ctx, bug.ID, TriageBugInput{
			Severity:    &severity,
			SeveritySet: true,
			ColumnID:    &extra.ID,
		}, bug.Version, f.actor.ID)
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("bug triage race error = %v, want ErrInvalid", err)
	}
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected bug triage race emitted events: %d -> %d", beforeEvents, got)
	}
	semanticRaceAssertColumnState(t, f, extra.ID, "completed")
}

func TestSemanticStateRaceRejectsBugTriageToArchivedColumn(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMTRIAGEARCH")
	bug := semanticRaceBug(t, f, "", "triage archive race bug")
	extra := semanticRaceColumn(t, f, "Archived triage destination", "ready", 0)
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	severity := "s1"

	_, err := semanticRaceMutationWithColumnUpdate(t, f, extra.ID, func(blocker *sql.Tx) error {
		_, err := blocker.ExecContext(f.ctx, `UPDATE columns SET archived_at=?, version=version+1, updated_at=? WHERE id=?`, now(), now(), extra.ID)
		return err
	}, func(ctx context.Context) (Task, error) {
		return f.store.TriageBug(ctx, bug.ID, TriageBugInput{
			Severity:    &severity,
			SeveritySet: true,
			ColumnID:    &extra.ID,
		}, bug.Version, f.actor.ID)
	})
	// The scheduled writer may commit before TriageBug's read-only preflight,
	// yielding the archive validation error, or after it, yielding the guarded
	// transaction's conflict. Both outcomes reject the mutation atomically.
	semanticRaceAssertArchiveRejection(t, err, "column is archived")
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected bug triage archive race emitted events: %d -> %d", beforeEvents, got)
	}
	column, err := f.store.GetColumn(f.ctx, extra.ID)
	if err != nil {
		t.Fatalf("reload archived triage destination: %v", err)
	}
	if column.ArchivedAt == nil {
		t.Fatalf("triage race destination is not archived: %+v", column)
	}
}

func TestSemanticStateRaceRejectsBugTriageFromArchivedColumn(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMSOURCARCH")
	source := semanticRaceColumn(t, f, "Archived triage source", "backlog", 0)
	bug := semanticRaceBug(t, f, source.ID, "triage source archive race bug")
	extra := semanticRaceColumn(t, f, "Triage source destination", "ready", 0)
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)
	severity := "s1"

	_, err := semanticRaceMutationWithColumnUpdate(t, f, source.ID, func(blocker *sql.Tx) error {
		_, err := blocker.ExecContext(f.ctx, `UPDATE columns SET archived_at=?, version=version+1, updated_at=? WHERE id=?`, now(), now(), source.ID)
		return err
	}, func(ctx context.Context) (Task, error) {
		return f.store.TriageBug(ctx, bug.ID, TriageBugInput{
			Severity:    &severity,
			SeveritySet: true,
			ColumnID:    &extra.ID,
		}, bug.Version, f.actor.ID)
	})
	// As with destination archives, either preflight validation or the guarded
	// transaction can observe the source archive first; both must reject.
	semanticRaceAssertArchiveRejection(t, err, "task is assigned to an archived column")
	assertSemanticRaceTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected bug triage source archive race emitted events: %d -> %d", beforeEvents, got)
	}
	column, err := f.store.GetColumn(f.ctx, source.ID)
	if err != nil {
		t.Fatalf("reload archived triage source: %v", err)
	}
	if column.ArchivedAt == nil {
		t.Fatalf("triage source race column is not archived: %+v", column)
	}
}
