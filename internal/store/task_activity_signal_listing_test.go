package store

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"
)

// Isolation tests for the TC-380 listing failure modes in
// test/neglect/API.md (1, 2, 7, 8, 9, 10, 12). Written before the listing
// code; the projector is synced so stored rows are current unless a test
// says otherwise.

const signalDay = 24 * time.Hour

func fixEvaluationClockForTest(data *Store, at time.Time) {
	data.evaluationClock = func() time.Time { return at }
}

func signalTimeForTest(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

func neglectPageForTest(t *testing.T, data *Store, projectID string, filter TaskFilter) TaskPage {
	t.Helper()
	page, err := data.ListTaskPage(t.Context(), projectID, filter)
	if err != nil {
		t.Fatalf("list %+v: %v", filter, err)
	}
	if filter.UsesActivitySignal() && page.Activity == nil {
		t.Fatalf("neglect listing %+v returned no activity metadata", filter)
	}
	for _, task := range page.Tasks {
		if task.Activity == nil {
			t.Fatalf("task %s listed without activity", task.ID)
		}
	}
	return page
}

// neglectChainForTest follows next cursors and returns every page.
func neglectChainForTest(t *testing.T, data *Store, projectID string, filter TaskFilter, between func(page int)) []TaskPage {
	t.Helper()
	var pages []TaskPage
	for {
		page := neglectPageForTest(t, data, projectID, filter)
		pages = append(pages, page)
		if !page.HasMore {
			return pages
		}
		if between != nil {
			between(len(pages))
		}
		filter.CursorToken = page.NextCursor
		if len(pages) > 100 {
			t.Fatal("page chain did not end")
		}
	}
}

func chainIDsForTest(pages []TaskPage) []string {
	var ids []string
	for _, page := range pages {
		ids = append(ids, taskIDs(page.Tasks)...)
	}
	return ids
}

func sortedIDsForTest(ids ...string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func sameIDSetForTest(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(sortedIDsForTest(got...), sortedIDsForTest(want...)) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func signalTaskForTest(t *testing.T, data *Store, projectID, actorID, title, at string) Task {
	t.Helper()
	task, err := data.CreateTask(t.Context(), projectID, TaskInput{Title: stringPtrForTest(title)}, actorID)
	if err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, t.Context(), task.ID, at)
	return task
}

// Failure modes 1 and 2: the threshold is exact and one evaluated_at governs
// the whole chain even when the clock moves between pages.
func TestTaskActivityListingBoundaryAndPageChain(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, _ := createAgentWorkFixture(t, data, ctx, "EDGE")
	evaluated := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	threshold := evaluated.Add(-14 * signalDay)
	atThreshold := signalTaskForTest(t, data, project.ID, agent.ID, "At threshold", signalTimeForTest(threshold))
	justInside := signalTaskForTest(t, data, project.ID, agent.ID, "One ms newer", signalTimeForTest(threshold.Add(time.Millisecond)))
	var old []string
	for days := 30; days <= 32; days++ {
		old = append(old, signalTaskForTest(t, data, project.ID, agent.ID, "Old", signalTimeForTest(evaluated.Add(-time.Duration(days)*signalDay))).ID)
	}
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	fixEvaluationClockForTest(data, evaluated)

	filter := TaskFilter{InactiveDays: 14, Limit: 1}
	pages := neglectChainForTest(t, data, project.ID, filter, func(int) {
		fixEvaluationClockForTest(data, evaluated.Add(signalDay))
	})
	sameIDSetForTest(t, "inactive chain", chainIDsForTest(pages), append(old, atThreshold.ID)...)
	for index, page := range pages {
		if !page.Activity.EvaluatedAt.Equal(evaluated) {
			t.Fatalf("page %d evaluated_at %s, want %s", index, page.Activity.EvaluatedAt, evaluated)
		}
		if !page.Activity.Coverage.Complete || page.Activity.Coverage.Lag != 0 || page.Activity.Coverage.Pending != 0 {
			t.Fatalf("page %d coverage %+v", index, page.Activity.Coverage)
		}
	}
	for _, task := range pages[0].Tasks {
		if task.ID == atThreshold.ID && (task.Activity.NotAfter == nil || *task.Activity.NotAfter != signalTimeForTest(threshold)) {
			t.Fatalf("threshold card activity %+v", task.Activity)
		}
	}
	// A new chain evaluates at the moved clock, where the 1 ms card is inactive.
	moved := neglectChainForTest(t, data, project.ID, filter, nil)
	sameIDSetForTest(t, "chain at moved clock", chainIDsForTest(moved), append(old, atThreshold.ID, justInside.ID)...)
}

// Failure modes 7, 8 and 9: status semantics, terminal and deleted tasks.
func TestTaskActivityListingStatusesTerminalAndUnknown(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, first := createAgentWorkFixture(t, data, ctx, "STAT")
	evaluated := time.Now().UTC().Truncate(time.Second)
	ago := func(days int) string { return signalTimeForTest(evaluated.Add(-time.Duration(days) * signalDay)) }
	knownOld := signalTaskForTest(t, data, project.ID, agent.ID, "Known old", ago(30))
	boundedOld := signalTaskForTest(t, data, project.ID, agent.ID, "Bounded old", ago(61))
	boundedRecent := signalTaskForTest(t, data, project.ID, agent.ID, "Bounded recent", ago(61))
	unknown := signalTaskForTest(t, data, project.ID, agent.ID, "Unknown", ago(30))
	for _, legacy := range []struct {
		task Task
		at   string
	}{{boundedOld, ago(60)}, {boundedRecent, ago(1)}, {unknown, "not-a-timestamp"}} {
		mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, 'task.updated', ?, ?, ?, '{"version":2}', ?)`,
			newID(), agent.ID, project.ID, legacy.task.ID, legacy.at)
	}
	terminal := signalTaskForTest(t, data, project.ID, agent.ID, "Terminal old", ago(40))
	if _, err := data.CompleteTask(ctx, terminal.ID, agent.ID, terminal.Version); err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, ctx, terminal.ID, ago(30))
	deleted := signalTaskForTest(t, data, project.ID, agent.ID, "Deleted old", ago(40))
	if err := data.DeleteTask(ctx, deleted.ID, deleted.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, ctx, deleted.ID, ago(30))
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	fixEvaluationClockForTest(data, evaluated)

	list := func(filter TaskFilter) []string {
		filter.Limit = 200
		return taskIDs(neglectPageForTest(t, data, project.ID, filter).Tasks)
	}
	sameIDSetForTest(t, "inactive_days=14", list(TaskFilter{InactiveDays: 14}), knownOld.ID, boundedOld.ID, unknown.ID)
	sameIDSetForTest(t, "known+14", list(TaskFilter{InactiveDays: 14, ActivityStatus: TaskActivityKnown}), knownOld.ID)
	sameIDSetForTest(t, "bounded+14", list(TaskFilter{InactiveDays: 14, ActivityStatus: TaskActivityBounded}), boundedOld.ID)
	sameIDSetForTest(t, "unknown+14", list(TaskFilter{InactiveDays: 14, ActivityStatus: TaskActivityUnknown}), unknown.ID)
	sameIDSetForTest(t, "undetermined+14", list(TaskFilter{InactiveDays: 14, ActivityStatus: TaskActivityUndetermined}), boundedRecent.ID)
	sameIDSetForTest(t, "known", list(TaskFilter{ActivityStatus: TaskActivityKnown}), knownOld.ID, first.ID)
	sameIDSetForTest(t, "bounded", list(TaskFilter{ActivityStatus: TaskActivityBounded}), boundedOld.ID, boundedRecent.ID)
	sameIDSetForTest(t, "unknown", list(TaskFilter{ActivityStatus: TaskActivityUnknown}), unknown.ID)
	sameIDSetForTest(t, "state=backlog release=unassigned +14", list(TaskFilter{InactiveDays: 14, State: "backlog", ReleaseID: "unassigned"}), knownOld.ID, boundedOld.ID, unknown.ID)
	sameIDSetForTest(t, "state=completed +14", list(TaskFilter{InactiveDays: 14, State: "completed"}))
	if _, err := data.ListTaskPage(ctx, project.ID, TaskFilter{ActivityStatus: TaskActivityUndetermined}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("undetermined without inactive_days error = %v", err)
	}
	if _, err := data.ListTaskPage(ctx, project.ID, TaskFilter{ActivityStatus: "active"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported activity_status error = %v", err)
	}

	// Plain reads still classify terminal tasks (open decision 3).
	plain := []Task{terminal, unknown, boundedOld}
	if err := data.PopulateTaskActivity(ctx, plain); err != nil {
		t.Fatal(err)
	}
	if got := plain[0].Activity; got == nil || got.Status != TaskActivityKnown || got.ClassificationVersion == nil || *got.ClassificationVersion != TaskActivityClassificationVersion {
		t.Fatalf("terminal activity = %+v", got)
	}
	if got := plain[1].Activity; got.Status != TaskActivityUnknown || got.NotAfter != nil || got.Reason != TaskActivityReasonUnparseableTime || got.Source != "" {
		t.Fatalf("unknown activity = %+v", got)
	}
	if got := plain[2].Activity; got.Status != TaskActivityBounded || got.LastMeaningfulAt != nil || got.NotAfter == nil || *got.NotAfter != ago(60) || got.Reason != TaskActivityReasonLegacyUpdate {
		t.Fatalf("bounded activity = %+v", got)
	}
}

// Failure mode 10: sort=not_after puts rows without a bound after every time
// ascending (before descending) and pages without gaps or repeats.
func TestTaskActivityListingNotAfterSortNullsAndKeyset(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, first := createAgentWorkFixture(t, data, ctx, "SORT")
	base := time.Now().UTC().Truncate(time.Second)
	oldest := signalTaskForTest(t, data, project.ID, agent.ID, "Oldest", signalTimeForTest(base.Add(-40*signalDay)))
	tieA := signalTaskForTest(t, data, project.ID, agent.ID, "Tie A", signalTimeForTest(base.Add(-30*signalDay)))
	tieB := signalTaskForTest(t, data, project.ID, agent.ID, "Tie B", signalTimeForTest(base.Add(-30*signalDay)))
	var nulls []string
	for index := 0; index < 2; index++ {
		task := signalTaskForTest(t, data, project.ID, agent.ID, "Unknown", signalTimeForTest(base.Add(-20*signalDay)))
		mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, 'task.updated', ?, ?, ?, '{}', 'never')`, newID(), agent.ID, project.ID, task.ID)
		nulls = append(nulls, task.ID)
	}
	missing := signalTaskForTest(t, data, project.ID, agent.ID, "No row", signalTimeForTest(base.Add(-50*signalDay)))
	nulls = append(nulls, missing.ID)
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	mustExecSignalSQL(t, data, ctx, `DELETE FROM task_activity_signal WHERE task_id=?`, missing.ID)
	fixEvaluationClockForTest(data, base)

	ascending := []string{oldest.ID, tieA.ID, tieB.ID, first.ID}
	ascending = append(ascending, nulls...)
	descending := make([]string, len(ascending))
	for index, id := range ascending {
		descending[len(ascending)-1-index] = id
	}
	for _, direction := range []struct {
		descending bool
		want       []string
	}{{false, ascending}, {true, descending}} {
		for _, limit := range []int{1, 2, 200} {
			pages := neglectChainForTest(t, data, project.ID, TaskFilter{Sort: TaskSortNotAfter, Descending: direction.descending, Limit: limit}, nil)
			if got := chainIDsForTest(pages); !reflect.DeepEqual(got, direction.want) {
				t.Fatalf("sort=not_after desc=%v limit=%d = %v, want %v", direction.descending, limit, got, direction.want)
			}
			for _, page := range pages {
				if page.Activity.Coverage.Complete {
					t.Fatal("coverage complete while a task has no row")
				}
			}
		}
	}
	// A not_after cursor cannot continue another sort.
	page := neglectPageForTest(t, data, project.ID, TaskFilter{Sort: TaskSortNotAfter, Limit: 1})
	if _, err := data.ListTaskPage(ctx, project.ID, TaskFilter{Sort: TaskSortNumber, CursorToken: page.NextCursor, Limit: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor reused across sorts error = %v", err)
	}
}

// Failure mode 12: a project write between pages restarts the chain.
func TestTaskActivityListingCollectionChangedBetweenPages(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, _ := createAgentWorkFixture(t, data, ctx, "RESTART")
	old := time.Now().UTC().Add(-30 * signalDay)
	var tasks []Task
	for index := 0; index < 3; index++ {
		tasks = append(tasks, signalTaskForTest(t, data, project.ID, agent.ID, "Old", signalTimeForTest(old)))
	}
	syncProjectorForTest(t, ctx, attachProjectorForTest(t, data, TaskActivitySignalProjectorOptions{}))
	page := neglectPageForTest(t, data, project.ID, TaskFilter{InactiveDays: 14, Limit: 1})
	if !page.HasMore {
		t.Fatal("expected another page")
	}
	human := createHumanSignalActor(t, data, ctx, "Restart human")
	if _, err := data.CreateComment(ctx, tasks[2].ID, human.ID, "Between pages"); err != nil {
		t.Fatal(err)
	}
	_, err := data.ListTaskPage(ctx, project.ID, TaskFilter{InactiveDays: 14, Limit: 1, CursorToken: page.NextCursor})
	if !errors.Is(err, ErrTaskCollectionChanged) {
		t.Fatalf("continuation after a project write error = %v", err)
	}
}
