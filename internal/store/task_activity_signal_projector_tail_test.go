package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

// Isolation tests for the TC-379 tail, poison, deletion and concurrency
// failure modes in test/neglect/PROJECTOR.md.

func stepProjectorForTest(t *testing.T, ctx context.Context, projector *TaskActivitySignalProjector) {
	t.Helper()
	if _, err := projector.step(ctx); err != nil {
		t.Fatalf("projector step: %v", err)
	}
}

func insertTouchEventForTest(t *testing.T, data *Store, ctx context.Context, task Task, actorID string) int64 {
	t.Helper()
	var cursor int64
	err := data.withTx(ctx, func(tx *sql.Tx) error {
		var err error
		cursor, err = insertEvent(ctx, tx, "task.claimed", actorID, task.ProjectID, task.ID, map[string]any{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

// Failure mode 4 (case 9): an event committed between a task's history read
// and the cursor update is processed by the next window, never skipped.
func TestTaskActivitySignalTailProcessesEventsCommittedDuringRebuild(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, _, task := createAgentWorkFixture(t, data, ctx, "RACE")
	human := createHumanSignalActor(t, data, ctx, "Racing human")
	backdateTaskActivityHistory(t, data, ctx, task.ID, "2026-08-01T00:00:00Z")
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{})
	syncProjectorForTest(t, ctx, projector)
	touched := insertTouchEventForTest(t, data, ctx, task, agent.ID)

	var raced int64
	projector.beforeWrite = func(pass string, ids []string) {
		if pass != "tail" || raced != 0 {
			return
		}
		if _, err := data.CreateComment(ctx, task.ID, human.ID, "Arrives mid-rebuild"); err != nil {
			t.Errorf("racing comment: %v", err)
		}
		raced = maxEventCursorForTest(t, data, ctx)
	}
	stepProjectorForTest(t, ctx, projector)
	cursor, _, _ := projectionStateForTest(t, data, ctx)
	if raced <= touched || cursor != touched {
		t.Fatalf("first window cursor %d; want the window end %d, below the racing event %d", cursor, touched, raced)
	}
	row, _ := readTaskActivitySignalRow(t, data, ctx, task.ID)
	if row.source != "task.created" {
		t.Fatalf("first window row %+v already includes the racing comment", row)
	}
	syncProjectorForTest(t, ctx, projector)
	row, _ = readTaskActivitySignalRow(t, data, ctx, task.ID)
	cursor, _, _ = projectionStateForTest(t, data, ctx)
	if row.source != "human.comment" || cursor != raced {
		t.Fatalf("next window row %+v cursor %d; want human.comment at %d", row, cursor, raced)
	}
}

// Failure modes 5 and 12: the cursor never regresses, one projector per
// process, and a second process running concurrently is safe.
func TestTaskActivitySignalCursorNeverRegressesAndSecondProjectorIsSafe(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "TWO")
	createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 5)
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{SeedBatch: 2, TailWindow: 3})
	if _, err := data.AttachTaskActivitySignalProjector(TaskActivitySignalProjectorOptions{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second attach in one process error = %v; want conflict", err)
	}
	syncProjectorForTest(t, ctx, projector)

	ahead := maxEventCursorForTest(t, data, ctx) + 1000
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal_projection SET event_cursor=?`, ahead)
	insertTouchEventForTest(t, data, ctx, task, agent.ID)
	syncProjectorForTest(t, ctx, projector)
	if cursor, _, _ := projectionStateForTest(t, data, ctx); cursor != ahead {
		t.Fatalf("cursor regressed from %d to %d", ahead, cursor)
	}
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal_projection SET event_cursor=?`, ahead-1000)

	path := filepath.Join(t.TempDir(), "shared.db")
	shared, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { shared.Close() })
	other, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	one, two := New(shared), New(other)
	oneAgent, oneProject, _ := createAgentWorkFixture(t, one, ctx, "PAIR")
	createProjectorTasksForTest(t, one, ctx, oneProject.ID, oneAgent.ID, 12)
	first := attachProjectorForTest(t, one, TaskActivitySignalProjectorOptions{SeedBatch: 3, TailWindow: 4})
	second := attachProjectorForTest(t, two, TaskActivitySignalProjectorOptions{SeedBatch: 3, TailWindow: 4})
	errs := make(chan error, 2)
	for _, p := range []*TaskActivitySignalProjector{first, second} {
		go func(p *TaskActivitySignalProjector) { errs <- p.Sync(ctx) }(p)
	}
	for index := 0; index < 2; index++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent sync: %v", err)
		}
	}
	syncProjectorForTest(t, ctx, first)
	cursor, seeded, _ := projectionStateForTest(t, one, ctx)
	if cursor != maxEventCursorForTest(t, one, ctx) || seeded != TaskActivityClassificationVersion {
		t.Fatalf("two projectors left projection %d/%d", cursor, seeded)
	}
	if rows, tasks := countForTest(t, one, ctx, `SELECT COUNT(*) FROM task_activity_signal`), countForTest(t, one, ctx, `SELECT COUNT(*) FROM tasks`); rows != tasks {
		t.Fatalf("rows %d tasks %d", rows, tasks)
	}
}

// Failure mode 6 (case 11): hard deletion never stalls the tail or
// recreates a row; a restored task is reclassified.
func TestTaskActivitySignalHardDeleteAndRestore(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, gone := createAgentWorkFixture(t, data, ctx, "HARD")
	tasks := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 3)
	kept, racing, restored := tasks[0], tasks[1], tasks[2]
	human := createHumanSignalActor(t, data, ctx, "Deleting human")
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{})
	syncProjectorForTest(t, ctx, projector)

	if _, err := data.CreateComment(ctx, gone.ID, human.ID, "Then hard-deleted"); err != nil {
		t.Fatal(err)
	}
	mustExecSignalSQL(t, data, ctx, `DELETE FROM tasks WHERE id=?`, gone.ID)
	if _, err := data.CreateComment(ctx, kept.ID, human.ID, "Kept evidence"); err != nil {
		t.Fatal(err)
	}
	insertTouchEventForTest(t, data, ctx, racing, agent.ID)
	projector.beforeWrite = func(pass string, ids []string) {
		for _, id := range ids {
			if id == racing.ID {
				mustExecSignalSQL(t, data, ctx, `DELETE FROM tasks WHERE id=?`, racing.ID)
			}
		}
	}
	syncProjectorForTest(t, ctx, projector)
	projector.beforeWrite = nil
	for _, id := range []string{gone.ID, racing.ID} {
		if _, ok := readTaskActivitySignalRow(t, data, ctx, id); ok {
			t.Fatalf("hard-deleted task %s has a signal row", id)
		}
	}
	if row, _ := readTaskActivitySignalRow(t, data, ctx, kept.ID); row.source != "human.comment" {
		t.Fatalf("neighbour not processed: %+v", row)
	}
	if cursor, _, _ := projectionStateForTest(t, data, ctx); cursor != maxEventCursorForTest(t, data, ctx) {
		t.Fatalf("hard deletion stalled the cursor at %d", cursor)
	}

	if err := data.DeleteTask(ctx, restored.ID, restored.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	syncProjectorForTest(t, ctx, projector)
	var version int64
	if err := data.DB.QueryRowContext(ctx, `SELECT version FROM tasks WHERE id=?`, restored.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	back, err := data.RestoreTask(ctx, restored.ID, version, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	syncProjectorForTest(t, ctx, projector)
	row, _ := readTaskActivitySignalRow(t, data, ctx, restored.ID)
	if row.source != "task.restored" || row.computedAt < back.UpdatedAt[:19] {
		t.Fatalf("restored task row %+v (restored at %s)", row, back.UpdatedAt)
	}
}

// Failure modes 7 and 8: a task whose classification fails is stored as
// unknown/classification_error and counted without stalling the cursor; a
// canceled pass writes nothing.
func TestTaskActivitySignalPoisonRecordedAndCanceledPassWritesNothing(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, poison := createAgentWorkFixture(t, data, ctx, "POISON")
	neighbour := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 1)[0]
	human := createHumanSignalActor(t, data, ctx, "Poison human")
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{})
	failures := 0
	projector.classify = func(ctx context.Context, taskID string) (TaskActivitySignal, error) {
		if taskID == poison.ID {
			failures++
			return TaskActivitySignal{}, errors.New("synthetic undecodable history")
		}
		return data.ClassifyTaskActivitySignal(ctx, taskID)
	}
	syncProjectorForTest(t, ctx, projector)
	row, ok := readTaskActivitySignalRow(t, data, ctx, poison.ID)
	if !ok || row.status != TaskActivityUnknown || row.reason != TaskActivityReasonClassificationError || row.version != TaskActivityClassificationVersion {
		t.Fatalf("seeded poison row = %+v, %v", row, ok)
	}
	for _, task := range []Task{poison, neighbour} {
		if _, err := data.CreateComment(ctx, task.ID, human.ID, "After poison"); err != nil {
			t.Fatal(err)
		}
	}
	syncProjectorForTest(t, ctx, projector)
	if row, _ := readTaskActivitySignalRow(t, data, ctx, neighbour.ID); row.source != "human.comment" {
		t.Fatalf("poison stalled its neighbour: %+v", row)
	}
	if cursor, _, _ := projectionStateForTest(t, data, ctx); cursor != maxEventCursorForTest(t, data, ctx) {
		t.Fatalf("poison stalled the cursor at %d", cursor)
	}
	if stats := projector.Stats(); stats.ClassificationErrors < 2 || failures < 2 {
		t.Fatalf("classification errors counted %d over %d failures", stats.ClassificationErrors, failures)
	}

	before, _, _ := projectionStateForTest(t, data, ctx)
	if _, err := data.CreateComment(ctx, poison.ID, human.ID, "During shutdown"); err != nil {
		t.Fatal(err)
	}
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal SET status='known', reason='' WHERE task_id=?`, poison.ID)
	canceled, cancel := context.WithCancel(ctx)
	projector.classify = func(ctx context.Context, taskID string) (TaskActivitySignal, error) {
		cancel()
		return TaskActivitySignal{}, ctx.Err()
	}
	if _, err := projector.step(canceled); err == nil {
		t.Fatal("canceled pass reported success")
	}
	if row, _ := readTaskActivitySignalRow(t, data, ctx, poison.ID); row.reason != "" {
		t.Fatalf("canceled pass wrote a poison row: %+v", row)
	}
	if cursor, _, _ := projectionStateForTest(t, data, ctx); cursor != before {
		t.Fatalf("canceled pass moved the cursor %d -> %d", before, cursor)
	}
}

// Failure modes 9, 10 and 15: every shared transaction wrapper wakes a
// running projector after commit (the ticker is an hour), another process's
// writes are found by the ticker, and Run stops on cancel.
func TestTaskActivitySignalWakeAfterCommitAndTickerFallback(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "WAKE")
	prerequisite := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 2)
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{Interval: time.Hour})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- projector.Run(runCtx) }()
	// Poll the stored cursor directly: WaitForTaskActivitySignal would wake
	// the projector itself and hide a missing commit wake.
	caughtUp := func(writer string) {
		t.Helper()
		high := maxEventCursorForTest(t, data, ctx)
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if cursor, _, _ := projectionStateForTest(t, data, ctx); cursor >= high {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: projector did not reach %d after commit", writer, high)
			}
		}
	}
	caughtUp("startup seed")
	writers := []struct {
		name  string
		write func() error
	}{
		{"withTx comment", func() error { _, err := data.CreateComment(ctx, task.ID, agent.ID, "Wake"); return err }},
		{"withTx raw", func() error {
			return data.withTx(ctx, func(tx *sql.Tx) error {
				_, err := insertEvent(ctx, tx, "task.claimed", agent.ID, project.ID, task.ID, nil)
				return err
			})
		}},
		{"withImmediateTx raw", func() error {
			return data.withImmediateTx(ctx, func(q dependencySQL) error {
				_, err := insertEvent(ctx, q, "task.claimed", agent.ID, project.ID, task.ID, nil)
				return err
			})
		}},
		{"dependency", func() error {
			current, err := data.GetTask(ctx, task.ID)
			if err == nil {
				_, err = data.AddTaskDependency(ctx, task.ID, prerequisite[0].ID, current.Version, agent.ID)
			}
			return err
		}},
		{"hierarchy", func() error {
			current, err := data.GetTask(ctx, prerequisite[1].ID)
			if err == nil {
				_, err = data.SetTaskParent(ctx, prerequisite[1].ID, task.ID, current.Version, agent.ID)
			}
			return err
		}},
	}
	for _, writer := range writers {
		if err := writer.write(); err != nil {
			t.Fatalf("%s: %v", writer.name, err)
		}
		caughtUp(writer.name)
	}
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	projector.Detach()

	foreignDB, err := db.Open(ctx, databasePathForTest(t, data, ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { foreignDB.Close() })
	ticking := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{Interval: 50 * time.Millisecond})
	runCtx, stop = context.WithCancel(ctx)
	defer stop()
	go func() { _ = ticking.Run(runCtx) }()
	if _, err := New(foreignDB).CreateComment(ctx, task.ID, agent.ID, "Written by another process"); err != nil {
		t.Fatal(err)
	}
	caughtUp("foreign process via ticker")
}

// databasePathForTest returns the file behind a test store so a second
// connection pool can stand in for another process.
func databasePathForTest(t *testing.T, data *Store, ctx context.Context) string {
	t.Helper()
	var seq int
	var name, file string
	if err := data.DB.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &file); err != nil || file == "" {
		t.Fatalf("database path: %q %v", file, err)
	}
	return file
}
