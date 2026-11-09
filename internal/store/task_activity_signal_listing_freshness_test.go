package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Isolation tests for TC-380 freshness failure modes in test/neglect/API.md
// (3, 4, 5, 6). Written before the listing code.

type observedSignalQueries struct {
	mu      sync.Mutex
	queries []string
}

func (o *observedSignalQueries) observe(query string, _ []any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.queries = append(o.queries, query)
}

func (o *observedSignalQueries) take() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := o.queries
	o.queries = nil
	return out
}

func projectionUpdatedAtForTest(t *testing.T, data *Store) string {
	t.Helper()
	var updatedAt string
	if err := data.DB.QueryRowContext(t.Context(), `SELECT updated_at FROM task_activity_signal_projection WHERE id=1`).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	return updatedAt
}

// Failure modes 3 and 4: pending is never listed as inactive, coverage says
// incomplete, and neither listings nor plain reads write.
func TestTaskActivityListingPendingAndReadersNeverWrite(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, first := createAgentWorkFixture(t, data, ctx, "PEND")
	old := signalTaskForTest(t, data, project.ID, agent.ID, "Old", signalTimeForTest(time.Now().UTC().Add(-30*signalDay)))

	// No projector has ever run: nothing is classified and nothing is written.
	page := neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: 200})
	if len(page.Tasks) != 0 || page.Activity.Coverage.Complete || page.Activity.Coverage.Lag == 0 {
		t.Fatalf("listing before any projector = %v %+v", taskIDs(page.Tasks), page.Activity.Coverage)
	}
	plain := []Task{first, old}
	if err := data.PopulateTaskActivity(ctx, plain); err != nil {
		t.Fatal(err)
	}
	for _, task := range plain {
		if task.Activity.Status != TaskActivityPending || task.Activity.NotAfter != nil || task.Activity.ClassificationVersion != nil {
			t.Fatalf("unprojected task activity = %+v", task.Activity)
		}
	}
	if _, _, ok := projectionStateForTest(t, data, ctx); ok {
		t.Fatal("a reader created the projection row")
	}

	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{Interval: time.Hour})
	syncProjectorForTest(t, ctx, projector)
	page = neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: 200})
	sameIDSetForTest(t, "settled listing", taskIDs(page.Tasks), old.ID)
	if !page.Activity.Coverage.Complete {
		t.Fatalf("settled coverage %+v", page.Activity.Coverage)
	}

	// A human comment the projector has not processed yet (Run is not going).
	human := createHumanSignalActor(t, data, ctx, "Pending human")
	if _, err := data.CreateComment(ctx, old.ID, human.ID, "Fresh evidence"); err != nil {
		t.Fatal(err)
	}
	updatedAt := projectionUpdatedAtForTest(t, data)
	rowBefore, _ := readTaskActivitySignalRow(t, data, ctx, old.ID)
	page = neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: 200})
	for _, task := range page.Tasks {
		if task.ID == old.ID && task.Activity.Status != TaskActivityPending {
			t.Fatalf("commented card listed as %+v", task.Activity)
		}
	}
	coverage := page.Activity.Coverage
	if coverage.Complete || coverage.Lag <= 0 || coverage.Pending != len(page.Tasks) {
		t.Fatalf("coverage with an unprocessed comment %+v (%d tasks)", coverage, len(page.Tasks))
	}
	plain = []Task{first, old}
	if err := data.PopulateTaskActivity(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if plain[0].Activity.Status != TaskActivityKnown || plain[1].Activity.Status != TaskActivityPending {
		t.Fatalf("plain read activity = %+v / %+v", plain[0].Activity, plain[1].Activity)
	}
	rowAfter, _ := readTaskActivitySignalRow(t, data, ctx, old.ID)
	if projectionUpdatedAtForTest(t, data) != updatedAt || rowAfter != rowBefore {
		t.Fatal("a reader wrote the projection or a signal row")
	}

	// With the projector running, a listing waits within its budget and
	// reports the card as active with complete coverage.
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = projector.Run(runCtx) }()
	t.Cleanup(func() { cancel(); <-done })
	if _, err := data.CreateComment(ctx, old.ID, human.ID, "More evidence"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	page = neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: 200, ActivityWait: 2 * time.Second})
	if elapsed := time.Since(started); elapsed > 2*time.Second || !page.Activity.Coverage.Complete || len(page.Tasks) != 0 {
		t.Fatalf("waited listing after %s = %v %+v", elapsed, taskIDs(page.Tasks), page.Activity.Coverage)
	}
}

// Failure mode 5: only neglect listings wait; a plain listing returns at once
// while the projector is behind.
func TestTaskActivityPlainReadsDoNotWait(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	_, project, task := createAgentWorkFixture(t, data, ctx, "NOWAIT")
	attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{Interval: time.Hour})
	started := time.Now()
	page := neglectPageForTest(t, data, project.ID, TaskFilter{IncludeActivity: true, ActivityWait: time.Second, Limit: 50})
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("plain listing waited %s", elapsed)
	}
	if page.Activity != nil || len(page.Tasks) != 1 || page.Tasks[0].Activity.Status != TaskActivityPending {
		t.Fatalf("plain listing = %+v", page)
	}
	started = time.Now()
	page = neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, ActivityWait: 200 * time.Millisecond, Limit: 50})
	if elapsed := time.Since(started); elapsed < 150*time.Millisecond || page.Activity.Coverage.Complete {
		t.Fatalf("neglect listing returned after %s with %+v", elapsed, page.Activity.Coverage)
	}
	if err := data.PopulateTaskActivity(ctx, []Task{task}); err != nil {
		t.Fatal(err)
	}
}

// Failure mode 6: activity is read in a fixed number of batched statements,
// whatever the page size.
func TestTaskActivityReadsAreBatched(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, _ := createAgentWorkFixture(t, data, ctx, "BATCH")
	tasks := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 40)
	projector := attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{Interval: time.Hour})
	syncProjectorForTest(t, ctx, projector)
	human := createHumanSignalActor(t, data, ctx, "Batch human")
	if _, err := data.CreateComment(ctx, tasks[0].ID, human.ID, "Behind"); err != nil {
		t.Fatal(err)
	}
	observer := &observedSignalQueries{}
	data.observeQuery = observer.observe
	counts := map[int]int{}
	for _, size := range []int{1, 40} {
		page := append([]Task(nil), tasks[:size]...)
		if err := data.PopulateTaskActivity(ctx, page); err != nil {
			t.Fatal(err)
		}
		counts[size] = len(observer.take())
	}
	if counts[1] == 0 || counts[1] != counts[40] || counts[40] > 3 {
		t.Fatalf("activity statements by page size = %v", counts)
	}
	for _, size := range []int{1, 40} {
		neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: size})
		counts[size] = len(observer.take())
	}
	if counts[1] != counts[40] {
		t.Fatalf("listing statements by page size = %v", counts)
	}
}
