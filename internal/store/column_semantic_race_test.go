package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestVersionedColumnReclassificationRejectsUnresolvedBugAtomically(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMADMINUNRES")
	backlogState := "backlog"
	extraBacklog, err := f.store.CreateColumn(f.ctx, f.project.ID, ColumnInput{
		Name:          semanticRaceStringPtr("Bug intake"),
		SemanticState: &backlogState,
	}, f.actor.ID)
	if err != nil {
		t.Fatalf("create extra backlog column: %v", err)
	}
	bug := semanticRaceBug(t, f, extraBacklog.ID, "unresolved bug")
	beforeColumn, err := f.store.GetColumn(f.ctx, extraBacklog.ID)
	if err != nil {
		t.Fatalf("read bug column: %v", err)
	}
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	completedState := "completed"
	_, err = f.store.UpdateColumnWithVersion(f.ctx, extraBacklog.ID, ColumnInput{SemanticState: &completedState}, f.actor.ID, true, semanticRaceVersionPtr(beforeColumn.Version))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("unresolved bug reclassification error = %v, want ErrConflict", err)
	}

	afterColumn, err := f.store.GetColumn(f.ctx, extraBacklog.ID)
	if err != nil {
		t.Fatalf("reload rejected bug column: %v", err)
	}
	if !reflect.DeepEqual(afterColumn, beforeColumn) {
		t.Fatalf("rejected unresolved-bug reclassification changed column:\nbefore=%+v\nafter=%+v", beforeColumn, afterColumn)
	}
	assertDependencyTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected unresolved-bug reclassification emitted events: %d -> %d", beforeEvents, got)
	}
}

func TestVersionedColumnReclassificationRejectsResolvedBugAtomically(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMADMINRESOLVED")
	bug := semanticRaceBug(t, f, "", "resolved bug")
	completed, err := f.store.StateColumn(f.ctx, f.project.ID, "completed")
	if err != nil {
		t.Fatalf("read completed column: %v", err)
	}
	resolved, err := f.store.ResolveBug(f.ctx, bug.ID, ResolveBugInput{Resolution: "fixed"}, bug.Version, f.actor.ID)
	if err != nil {
		t.Fatalf("resolve bug: %v", err)
	}
	if resolved.ColumnID != completed.ID || resolved.CompletedAt == nil || resolved.Bug == nil || resolved.Bug.Resolution == nil {
		t.Fatalf("resolved bug = %+v, want completed with resolution", resolved)
	}
	extraCompletedState := "completed"
	if _, err := f.store.CreateColumn(f.ctx, f.project.ID, ColumnInput{
		Name:          semanticRaceStringPtr("Verification complete"),
		SemanticState: &extraCompletedState,
	}, f.actor.ID); err != nil {
		t.Fatalf("create extra completed column: %v", err)
	}
	beforeColumn, err := f.store.GetColumn(f.ctx, completed.ID)
	if err != nil {
		t.Fatalf("read resolved bug column: %v", err)
	}
	beforeTask := dependencyDurableState(t, f, bug.ID)
	beforeEvents := semanticRaceProjectEventCount(t, f)

	readyState := "ready"
	_, err = f.store.UpdateColumnWithVersion(f.ctx, completed.ID, ColumnInput{SemanticState: &readyState}, f.actor.ID, true, semanticRaceVersionPtr(beforeColumn.Version))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("resolved bug reclassification error = %v, want ErrConflict", err)
	}

	afterColumn, err := f.store.GetColumn(f.ctx, completed.ID)
	if err != nil {
		t.Fatalf("reload resolved bug column: %v", err)
	}
	if !reflect.DeepEqual(afterColumn, beforeColumn) {
		t.Fatalf("rejected resolved-bug reclassification changed column:\nbefore=%+v\nafter=%+v", beforeColumn, afterColumn)
	}
	assertDependencyTaskUnchanged(t, f, bug.ID, beforeTask)
	if got := semanticRaceProjectEventCount(t, f); got != beforeEvents {
		t.Fatalf("rejected resolved-bug reclassification emitted events: %d -> %d", beforeEvents, got)
	}
}

func semanticRaceMutation(t *testing.T, f dependencyFixture, destinationID, reclassifiedState string, call func(context.Context) (Task, error)) (Task, error) {
	t.Helper()
	return semanticRaceMutationWithColumnUpdate(t, f, destinationID, func(blocker *sql.Tx) error {
		_, err := blocker.ExecContext(f.ctx, `UPDATE columns SET semantic_state=?, version=version+1, updated_at=? WHERE id=?`, reclassifiedState, now(), destinationID)
		return err
	}, call)
}

func semanticRaceMutationWithColumnUpdate(t *testing.T, f dependencyFixture, destinationID string, updateColumn func(*sql.Tx) error, call func(context.Context) (Task, error)) (Task, error) {
	t.Helper()
	barrier := semanticRaceBarrierFor(t, f.store.DB)
	blocker, err := f.store.DB.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatalf("begin semantic race blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(f.ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, f.project.ID); err != nil {
		t.Fatalf("acquire semantic race writer lock: %v", err)
	}

	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	result := make(chan semanticRaceResult, 1)
	barrier.arm()
	defer barrier.release()
	go func() {
		task, callErr := call(ctx)
		result <- semanticRaceResult{task: task, err: callErr}
	}()
	semanticRaceWaitForTransaction(t, barrier, result)

	if err := updateColumn(blocker); err != nil {
		t.Fatalf("reclassify semantic race destination: %v", err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release semantic race writer lock: %v", err)
	}

	barrier.release()
	select {
	case outcome := <-result:
		return outcome.task, outcome.err
	case <-time.After(4 * time.Second):
		t.Fatalf("semantic race operation did not finish after writer release")
		return Task{}, context.DeadlineExceeded
	}
}

func semanticRaceWaitForTransaction(t *testing.T, barrier *semanticRaceBarrier, result <-chan semanticRaceResult) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	select {
	case <-barrier.reached:
		return
	case outcome := <-result:
		t.Fatalf("semantic race operation finished before its writer transaction: task=%+v err=%v", outcome.task, outcome.err)
	case <-deadline.C:
		t.Fatalf("semantic race operation did not reach its writer transaction")
	}
}

func semanticRaceBug(t *testing.T, f dependencyFixture, columnID, title string) Task {
	t.Helper()
	kind := bugKind
	actual := title + " actual behavior"
	input := TaskInput{
		Title: semanticRaceStringPtr(title),
		Kind:  &kind,
		Bug:   &BugInput{ActualBehavior: &actual},
	}
	if columnID != "" {
		input.ColumnID = &columnID
	}
	bug, err := f.store.CreateTask(f.ctx, f.project.ID, input, f.actor.ID)
	if err != nil {
		t.Fatalf("create semantic race bug %q: %v", title, err)
	}
	return bug
}

func semanticRaceColumn(t *testing.T, f dependencyFixture, name, state string, position int) Column {
	t.Helper()
	column, err := f.store.CreateColumn(f.ctx, f.project.ID, ColumnInput{
		Name:          semanticRaceStringPtr(name),
		SemanticState: semanticRaceStringPtr(state),
		Position:      semanticRaceIntPtr(position),
	}, f.actor.ID)
	if err != nil {
		t.Fatalf("create semantic race column %q: %v", name, err)
	}
	return column
}

func assertSemanticRaceTaskUnchanged(t *testing.T, f dependencyFixture, taskID string, before dependencyDurableTaskState) {
	t.Helper()
	after := dependencyDurableState(t, f, taskID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected semantic race mutation changed durable task state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func semanticRaceAssertColumnState(t *testing.T, f dependencyFixture, columnID, state string) {
	t.Helper()
	column, err := f.store.GetColumn(f.ctx, columnID)
	if err != nil {
		t.Fatalf("reload reclassified semantic race column: %v", err)
	}
	if column.SemanticState != state {
		t.Fatalf("semantic race destination state = %q, want %q", column.SemanticState, state)
	}
}

func semanticRaceAssertArchiveRejection(t *testing.T, err error, invalidMessage string) {
	t.Helper()
	if errors.Is(err, ErrConflict) {
		return
	}
	var storeErr *Error
	if errors.Is(err, ErrInvalid) && errors.As(err, &storeErr) && storeErr.Message == invalidMessage {
		return
	}
	t.Fatalf("archive race error = %v, want ErrConflict or ErrInvalid(%q)", err, invalidMessage)
}

func semanticRaceProjectEventCount(t *testing.T, f dependencyFixture) int {
	t.Helper()
	var count int
	if err := f.store.DB.QueryRowContext(f.ctx, `SELECT COUNT(1) FROM events WHERE project_id=?`, f.project.ID).Scan(&count); err != nil {
		t.Fatalf("count semantic race project events: %v", err)
	}
	return count
}

func semanticRaceStringPtr(value string) *string { return &value }

func semanticRaceIntPtr(value int) *int { return &value }

func semanticRaceVersionPtr(value int64) *int64 { return &value }
