package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Isolation tests for the TC-379 projector failure modes in
// test/neglect/PROJECTOR.md: seed, resume, reclassification and readers.

func attachProjectorForTest(t *testing.T, data *Store, options TaskActivitySignalProjectorOptions) *TaskActivitySignalProjector {
	t.Helper()
	projector, err := data.AttachTaskActivitySignalProjector(options)
	if err != nil {
		t.Fatalf("attach projector: %v", err)
	}
	t.Cleanup(projector.Detach)
	return projector
}

func syncProjectorForTest(t *testing.T, ctx context.Context, projector *TaskActivitySignalProjector) {
	t.Helper()
	if err := projector.Sync(ctx); err != nil {
		t.Fatalf("projector sync: %v", err)
	}
}

func projectionStateForTest(t *testing.T, data *Store, ctx context.Context) (cursor int64, seeded int, ok bool) {
	t.Helper()
	err := data.DB.QueryRowContext(ctx, `SELECT event_cursor, seeded_version FROM task_activity_signal_projection WHERE id=1`).Scan(&cursor, &seeded)
	if err != nil {
		return 0, 0, false
	}
	return cursor, seeded, true
}

func maxEventCursorForTest(t *testing.T, data *Store, ctx context.Context) int64 {
	t.Helper()
	var high int64
	if err := data.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(cursor), 0) FROM events`).Scan(&high); err != nil {
		t.Fatal(err)
	}
	return high
}

func countForTest(t *testing.T, data *Store, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var count int
	if err := data.DB.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", query, err)
	}
	return count
}

// createProjectorTasksForTest adds n plain tasks to the project.
func createProjectorTasksForTest(t *testing.T, data *Store, ctx context.Context, projectID, actorID string, n int) []Task {
	t.Helper()
	tasks := make([]Task, 0, n)
	for index := 0; index < n; index++ {
		task, err := data.CreateTask(ctx, projectID, TaskInput{Title: stringPtrForTest("Projector task")}, actorID)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	return tasks
}

// Failure mode 1: the cursor is recorded before any seed rebuild, and the
// seed gives every task (deleted and terminal included) a current row.
func TestTaskActivitySignalSeedRecordsCursorFirstAndFillsEveryTask(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, first := createAgentWorkFixture(t, data, ctx, "SEED")
	createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 4)
	if err := data.DeleteTask(ctx, first.ID, first.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	high := maxEventCursorForTest(t, data, ctx)
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{SeedBatch: 2})
	checked := false
	projector.beforeWrite = func(pass string, _ []string) {
		if pass != "seed" || checked {
			return
		}
		checked = true
		cursor, seeded, ok := projectionStateForTest(t, data, ctx)
		if !ok || cursor != high || seeded != 0 {
			t.Errorf("before the first seed write projection = %d/%d/%v; want cursor %d recorded, seed unfinished", cursor, seeded, ok, high)
		}
	}
	syncProjectorForTest(t, ctx, projector)
	if !checked {
		t.Fatal("seed wrote no batch")
	}
	tasks := countForTest(t, data, ctx, `SELECT COUNT(*) FROM tasks`)
	rows := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal WHERE classification_version=?`, TaskActivityClassificationVersion)
	if rows != tasks || tasks != 5 {
		t.Fatalf("rows %d, tasks %d", rows, tasks)
	}
	cursor, seeded, _ := projectionStateForTest(t, data, ctx)
	if cursor != maxEventCursorForTest(t, data, ctx) || seeded != TaskActivityClassificationVersion {
		t.Fatalf("projection %d/%d after sync", cursor, seeded)
	}
	stats := projector.Stats()
	if !stats.SeedComplete || stats.SeedRemaining != 0 || stats.SeedRebuilds != 5 || stats.SeedBatch != 2 {
		t.Fatalf("seed stats %+v", stats)
	}
}

// Failure mode 2 (case 8): an interrupted seed resumes with no missing rows
// and repeats nothing that was committed; the cursor never regresses.
func TestTaskActivitySignalSeedResumesAfterInterruption(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, _ := createAgentWorkFixture(t, data, ctx, "RESUME")
	createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 6)
	first, err := data.AttachTaskActivitySignalProjector(TaskActivitySignalProjectorOptions{SeedBatch: 3})
	if err != nil {
		t.Fatal(err)
	}
	killCtx, kill := context.WithCancel(ctx)
	batches := 0
	first.beforeWrite = func(pass string, _ []string) {
		if pass == "seed" {
			batches++
			if batches == 2 {
				kill() // the process dies with the second batch classified, not written
			}
		}
	}
	if err := first.Sync(killCtx); err == nil {
		t.Fatal("interrupted sync reported success")
	}
	first.Detach()
	recorded, seeded, _ := projectionStateForTest(t, data, ctx)
	committed := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal`)
	if committed != 3 || seeded != 0 {
		t.Fatalf("after kill rows=%d seeded=%d; want one committed batch of 3", committed, seeded)
	}
	var keptComputedAt string
	if err := data.DB.QueryRowContext(ctx, `SELECT MIN(computed_at) FROM task_activity_signal`).Scan(&keptComputedAt); err != nil {
		t.Fatal(err)
	}

	second := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{SeedBatch: 3})
	syncProjectorForTest(t, ctx, second)
	if got := second.Stats().SeedRebuilds; got != 4 {
		t.Fatalf("restart rebuilt %d tasks; want only the 4 without a committed row", got)
	}
	if rows := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal`); rows != 7 {
		t.Fatalf("rows after resume = %d", rows)
	}
	if count := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal WHERE computed_at=?`, keptComputedAt); count == 0 {
		t.Fatal("restart rewrote rows the interrupted seed had committed")
	}
	cursor, seeded, _ := projectionStateForTest(t, data, ctx)
	if cursor < recorded || seeded != TaskActivityClassificationVersion {
		t.Fatalf("projection after resume %d/%d; recorded %d", cursor, seeded, recorded)
	}
}

// Failure mode 11: rows below the current version are reclassified by the
// seed pass; current rows are left alone.
func TestTaskActivitySignalVersionBumpReclassifiesOlderRows(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, stale := createAgentWorkFixture(t, data, ctx, "BUMP")
	createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 3)
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal SET classification_version=1, status='unknown', reason='legacy_update', computed_at='2000-01-01T00:00:00.000000000Z' WHERE task_id=?`, stale.ID)
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal_projection SET seeded_version=1`)
	var untouched string
	if err := data.DB.QueryRowContext(ctx, `SELECT MAX(computed_at) FROM task_activity_signal`).Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	data.activitySignal.Load().Detach()

	upgraded := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{})
	syncProjectorForTest(t, ctx, upgraded)
	row, _ := readTaskActivitySignalRow(t, data, ctx, stale.ID)
	if row.version != TaskActivityClassificationVersion || row.status != TaskActivityKnown || row.source != "task.created" {
		t.Fatalf("older row not reclassified: %+v", row)
	}
	if got := upgraded.Stats().SeedRebuilds; got != 1 {
		t.Fatalf("version bump rebuilt %d rows; want 1", got)
	}
	if count := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal WHERE computed_at=?`, untouched); count != 3 {
		t.Fatalf("current rows rewritten: %d keep their computed_at", count)
	}
	if _, seeded, _ := projectionStateForTest(t, data, ctx); seeded != TaskActivityClassificationVersion {
		t.Fatalf("seeded_version = %d", seeded)
	}
}

// Failure mode 13: freshness reads never write, wait at most their budget,
// and report exactly the tasks whose rows are not current.
func TestTaskActivitySignalReadersNeverWrite(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "READ")
	other := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 1)[0]
	progress, err := data.TaskActivitySignalProgress(ctx)
	if err != nil || progress.Initialized || progress.High == 0 || progress.Lag() != progress.High {
		t.Fatalf("uninitialised progress = %+v, %v", progress, err)
	}
	started := time.Now()
	waited, reached, err := data.WaitForTaskActivitySignal(ctx, progress.High, 120*time.Millisecond)
	elapsed := time.Since(started)
	if err != nil || reached || waited.Initialized || elapsed < 100*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("wait without projector = %+v reached=%v err=%v after %s", waited, reached, err, elapsed)
	}
	pending, err := data.TaskActivitySignalUnprocessed(ctx, progress.Processed, []string{task.ID, other.ID, "no-such-task"})
	if err != nil || len(pending) != 2 || !pending[task.ID] || !pending[other.ID] {
		t.Fatalf("unprocessed before seed = %v, %v", pending, err)
	}
	if _, _, ok := projectionStateForTest(t, data, ctx); ok {
		t.Fatal("a reader created the projection row")
	}
	if rows := countForTest(t, data, ctx, `SELECT COUNT(*) FROM task_activity_signal`); rows != 0 {
		t.Fatalf("readers wrote %d rows", rows)
	}

	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	progress, err = data.TaskActivitySignalProgress(ctx)
	if err != nil || !progress.Initialized || progress.Lag() != 0 || progress.SeededVersion != TaskActivityClassificationVersion {
		t.Fatalf("progress after sync = %+v, %v", progress, err)
	}
	if _, reached, err := data.WaitForTaskActivitySignal(ctx, progress.High, 0); err != nil || !reached {
		t.Fatalf("caught-up wait reached=%v err=%v", reached, err)
	}
	if pending, err := data.TaskActivitySignalUnprocessed(ctx, progress.Processed, []string{task.ID, other.ID}); err != nil || len(pending) != 0 {
		t.Fatalf("unprocessed after sync = %v, %v", pending, err)
	}
	human := createHumanSignalActor(t, data, ctx, "Reader human")
	if _, err := data.CreateComment(ctx, task.ID, human.ID, "Fresh evidence"); err != nil {
		t.Fatal(err)
	}
	mustExecSignalSQL(t, data, ctx, `UPDATE task_activity_signal SET classification_version=1 WHERE task_id=?`, other.ID)
	var updatedAt string
	if err := data.DB.QueryRowContext(ctx, `SELECT updated_at FROM task_activity_signal_projection`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	pending, err = data.TaskActivitySignalUnprocessed(ctx, progress.Processed, []string{task.ID, other.ID})
	if err != nil || len(pending) != 2 {
		t.Fatalf("unprocessed after comment and version drop = %v, %v", pending, err)
	}
	if _, reached, err := data.WaitForTaskActivitySignal(ctx, maxEventCursorForTest(t, data, ctx), 50*time.Millisecond); err != nil || reached {
		t.Fatalf("wait with a detached-from-run projector reached=%v err=%v", reached, err)
	}
	var after string
	if err := data.DB.QueryRowContext(ctx, `SELECT updated_at FROM task_activity_signal_projection`).Scan(&after); err != nil || after != updatedAt {
		t.Fatalf("a reader advanced the projection: %q -> %q (%v)", updatedAt, after, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := data.WaitForTaskActivitySignal(canceled, maxEventCursorForTest(t, data, ctx), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait error = %v", err)
	}
}
