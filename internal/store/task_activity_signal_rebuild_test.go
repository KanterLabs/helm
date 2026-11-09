package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

// Isolation tests for the TC-378 failure modes listed in
// test/neglect/FAILURES.md ("TC-378 store isolation failure modes").

type taskActivitySignalRow struct {
	status, source, reason, computedAt string
	lastMeaningfulAt, notAfter         sql.NullString
	version                            int
}

func newTaskActivitySignalStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "helm.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database), ctx
}

func readTaskActivitySignalRow(t *testing.T, data *Store, ctx context.Context, taskID string) (taskActivitySignalRow, bool) {
	t.Helper()
	var row taskActivitySignalRow
	err := data.DB.QueryRowContext(ctx, `SELECT status, last_meaningful_at, source, not_after, reason, classification_version, computed_at FROM task_activity_signal WHERE task_id=?`, taskID).
		Scan(&row.status, &row.lastMeaningfulAt, &row.source, &row.notAfter, &row.reason, &row.version, &row.computedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return row, false
	}
	if err != nil {
		t.Fatalf("read signal row: %v", err)
	}
	return row, true
}

func mustExecSignalSQL(t *testing.T, data *Store, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := data.DB.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %s: %v", query, err)
	}
}

// backdateTaskActivityHistory moves a task's creation and every existing
// event to at, as the TC-377 harness does for owned synthetic rows.
func backdateTaskActivityHistory(t *testing.T, data *Store, ctx context.Context, taskID, at string) {
	t.Helper()
	mustExecSignalSQL(t, data, ctx, `UPDATE tasks SET created_at=? WHERE id=?`, at, taskID)
	mustExecSignalSQL(t, data, ctx, `UPDATE events SET created_at=? WHERE task_id=?`, at, taskID)
}

func rebuildTaskActivitySignalForTest(t *testing.T, data *Store, ctx context.Context, taskID string) (TaskActivitySignal, taskActivitySignalRow) {
	t.Helper()
	signal, err := data.RebuildTaskActivitySignal(ctx, taskID)
	if err != nil {
		t.Fatalf("rebuild signal: %v", err)
	}
	row, ok := readTaskActivitySignalRow(t, data, ctx, taskID)
	if !ok {
		t.Fatal("rebuild wrote no row")
	}
	if row.version != TaskActivityClassificationVersion || row.status != signal.Status || row.source != signal.Source || row.reason != signal.Reason {
		t.Fatalf("stored row %+v does not match returned signal %+v", row, signal)
	}
	return signal, row
}

func assertStoredSignal(t *testing.T, row taskActivitySignalRow, status, source, reason, notAfter string) {
	t.Helper()
	if row.status != status || row.source != source || row.reason != reason || row.notAfter.String != notAfter || row.notAfter.Valid != (notAfter != "") {
		t.Fatalf("signal row = %+v; want %s/%s/%s not_after=%q", row, status, source, reason, notAfter)
	}
	if status == TaskActivityKnown && row.lastMeaningfulAt != row.notAfter {
		t.Fatalf("known row last_meaningful_at %v != not_after %v", row.lastMeaningfulAt, row.notAfter)
	}
	if status != TaskActivityKnown && row.lastMeaningfulAt.Valid {
		t.Fatalf("%s row has last_meaningful_at %v", status, row.lastMeaningfulAt)
	}
}

func createHumanSignalActor(t *testing.T, data *Store, ctx context.Context, name string) Actor {
	t.Helper()
	actor, err := data.CreateActor(ctx, Actor{Kind: "human", Name: name}, "")
	if err != nil {
		t.Fatalf("create human: %v", err)
	}
	return actor
}

func collectTaskActivitySignalItems(t *testing.T, data *Store, ctx context.Context, taskID string) ([]TaskTimelineItem, int) {
	t.Helper()
	var items []TaskTimelineItem
	undecodable := 0
	err := data.eachTaskTimelineItem(ctx, taskID, func(item TaskTimelineItem, decodeErr error) error {
		if decodeErr != nil {
			undecodable++
		}
		items = append(items, item)
		return nil
	})
	if err != nil {
		t.Fatalf("iterate timeline: %v", err)
	}
	return items, undecodable
}

// Failure mode 2: the iterator serves exactly the endpoint's merged stream.
func TestTaskActivitySignalIteratorMatchesTimelineEndpoint(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, _, task := createAgentWorkFixture(t, data, ctx, "SIGITER")
	human := createHumanSignalActor(t, data, ctx, "Signal human")
	if _, err := data.CreateComment(ctx, task.ID, human.ID, "Human context"); err != nil {
		t.Fatal(err)
	}
	removed, err := data.CreateComment(ctx, task.ID, human.ID, "Removed later")
	if err != nil {
		t.Fatal(err)
	}
	if err := data.DeleteComment(ctx, task.ID, removed.ID, human.ID, removed.Version, false); err != nil {
		t.Fatal(err)
	}
	claimed, err := data.ClaimTask(ctx, task.ID, agent.ID, time.Hour, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.PublishAgentWork(ctx, task.ID, AgentWorkInput{OperationID: "signal/run", State: "working", Phase: "Build", Summary: "Pulse", NextAction: "Next"}, claimed.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateComment(ctx, task.ID, agent.ID, "Agent note"); err != nil {
		t.Fatal(err)
	}
	page, more, err := data.ListTaskTimeline(ctx, task.ID, TaskTimelineFilter{Limit: 200})
	if err != nil || more {
		t.Fatalf("endpoint page: more=%v err=%v", more, err)
	}
	items, undecodable := collectTaskActivitySignalItems(t, data, ctx, task.ID)
	if undecodable != 0 {
		t.Fatalf("undecodable rows = %d", undecodable)
	}
	ids := func(list []TaskTimelineItem) []string {
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, item.Kind+":"+item.ID)
		}
		return out
	}
	if !reflect.DeepEqual(ids(items), ids(page)) {
		t.Fatalf("iterator items differ from endpoint:\n iterator %v\n endpoint %v", ids(items), ids(page))
	}
	for _, item := range items {
		if item.ID == removed.ID {
			t.Fatal("soft-deleted comment reached the classifier")
		}
	}
}

// Failure mode 3: no page limit leaks into the classifier's history.
func TestTaskActivitySignalReadsCompleteLongHistory(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	_, project, task := createAgentWorkFixture(t, data, ctx, "SIGLONG")
	human := createHumanSignalActor(t, data, ctx, "Long human")
	comment, err := data.CreateComment(ctx, task.ID, human.ID, "Old evidence")
	if err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, ctx, task.ID, "2026-08-01T00:00:00Z")
	mustExecSignalSQL(t, data, ctx, `UPDATE comments SET created_at='2026-08-10T00:00:00Z' WHERE id=?`, comment.ID)
	for i := 0; i < 260; i++ {
		mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, 'task.claimed', ?, ?, ?, '{}', ?)`,
			fmt.Sprintf("noise-%03d", i), human.ID, project.ID, task.ID, fmt.Sprintf("2026-09-01T00:%02d:%02dZ", i/60, i%60))
	}
	items, _ := collectTaskActivitySignalItems(t, data, ctx, task.ID)
	if len(items) < 262 {
		t.Fatalf("iterator returned %d items, want the complete history (>= 262)", len(items))
	}
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityKnown, "human.comment", "", "2026-08-10T00:00:00.000000000Z")
}

// Failure mode 4: malformed payload JSON is a classification_error, and the
// existing timeline read is unchanged.
func TestTaskActivitySignalMalformedPayloadIsClassificationError(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "SIGBAD")
	mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES ('poison', 'task.updated', ?, ?, ?, '{not json', '2026-09-05T00:00:00Z')`, agent.ID, project.ID, task.ID)
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityUnknown, "", TaskActivityReasonClassificationError, "")
	page, _, err := data.ListTaskTimeline(ctx, task.ID, TaskTimelineFilter{Limit: 50})
	if err != nil {
		t.Fatalf("store timeline read changed behaviour: %v", err)
	}
	found := false
	for _, item := range page {
		found = found || (item.ID == "poison" && string(item.Change.Payload) == "{not json")
	}
	if !found {
		t.Fatal("store timeline no longer returns the raw malformed row")
	}
}

// Failure mode 5: an undecodable stored progress row is tolerated.
func TestTaskActivitySignalUndecodableProgressIsClassificationError(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, _, task := createAgentWorkFixture(t, data, ctx, "SIGREFS")
	claimed, err := data.ClaimTask(ctx, task.ID, agent.ID, time.Hour, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.PublishAgentWork(ctx, task.ID, AgentWorkInput{OperationID: "refs/run", State: "working", Phase: "Build", Summary: "Pulse", NextAction: "Next"}, claimed.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	mustExecSignalSQL(t, data, ctx, `UPDATE task_agent_work_history SET checkpoint_refs='[1,2]' WHERE task_id=?`, task.ID)
	if _, _, err := data.ListTaskTimeline(ctx, task.ID, TaskTimelineFilter{Limit: 50}); err == nil {
		t.Fatal("existing strict timeline read now tolerates corrupt progress; behaviour changed")
	}
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityUnknown, "", TaskActivityReasonClassificationError, "")
}

// Failure modes 6, 7 and 8: every task gets a row, rebuild is idempotent,
// hard deletion cascades.
func TestTaskActivitySignalRowsForDeletedAndTerminalTasksAreIdempotent(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, deleted := createAgentWorkFixture(t, data, ctx, "SIGROWS")
	backdateTaskActivityHistory(t, data, ctx, deleted.ID, "2026-08-01T00:00:00Z")
	if err := data.DeleteTask(ctx, deleted.ID, deleted.Version, agent.ID); err != nil {
		t.Fatal(err)
	}
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, deleted.ID)
	assertStoredSignal(t, row, TaskActivityKnown, "task.created", "", "2026-08-01T00:00:00.000000000Z")

	terminal, err := data.CreateTask(ctx, project.ID, TaskInput{Title: stringPtrForTest("Terminal")}, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.CompleteTask(ctx, terminal.ID, agent.ID, terminal.Version); err != nil {
		t.Fatal(err)
	}
	first, firstRow := rebuildTaskActivitySignalForTest(t, data, ctx, terminal.ID)
	if first.Status != TaskActivityKnown || first.Source != "task.completed" {
		t.Fatalf("terminal signal = %+v", first)
	}
	second, secondRow := rebuildTaskActivitySignalForTest(t, data, ctx, terminal.ID)
	firstRow.computedAt, secondRow.computedAt = "", ""
	first.ComputedAt, second.ComputedAt = "", ""
	if !reflect.DeepEqual(firstRow, secondRow) || !reflect.DeepEqual(first, second) {
		t.Fatalf("rebuild not idempotent: %+v / %+v", firstRow, secondRow)
	}
	var rows int
	if err := data.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_activity_signal WHERE task_id=?`, terminal.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows after two rebuilds = %d, %v", rows, err)
	}

	mustExecSignalSQL(t, data, ctx, `DELETE FROM tasks WHERE id=?`, terminal.ID)
	if _, ok := readTaskActivitySignalRow(t, data, ctx, terminal.ID); ok {
		t.Fatal("hard-deleted task kept its signal row")
	}
}

// Failure mode 9: stored times are fixed-width UTC whatever the source form.
func TestTaskActivitySignalStoresSortableUTCInstants(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	_, _, task := createAgentWorkFixture(t, data, ctx, "SIGTIME")
	human := createHumanSignalActor(t, data, ctx, "Offset human")
	comment, err := data.CreateComment(ctx, task.ID, human.ID, "Offset evidence")
	if err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, ctx, task.ID, "2026-08-01T00:00:00Z")
	mustExecSignalSQL(t, data, ctx, `UPDATE comments SET created_at='2026-09-05T02:00:00.5+02:00' WHERE id=?`, comment.ID)
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityKnown, "human.comment", "", "2026-09-05T00:00:00.500000000Z")
}

// Failure mode 10 (plan case 12) and the D2 bounded rule on a real database.
func TestTaskActivitySignalCommentRemovalAndLegacyBound(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "SIGDEL")
	human := createHumanSignalActor(t, data, ctx, "Removal human")
	comment, err := data.CreateComment(ctx, task.ID, human.ID, "Soon removed")
	if err != nil {
		t.Fatal(err)
	}
	backdateTaskActivityHistory(t, data, ctx, task.ID, "2026-08-01T00:00:00Z")
	mustExecSignalSQL(t, data, ctx, `UPDATE comments SET created_at='2026-08-20T00:00:00Z' WHERE id=?`, comment.ID)
	_, row := rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityKnown, "human.comment", "", "2026-08-20T00:00:00.000000000Z")
	if err := data.DeleteComment(ctx, task.ID, comment.ID, human.ID, comment.Version, false); err != nil {
		t.Fatal(err)
	}
	_, row = rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityKnown, "task.created", "", "2026-08-01T00:00:00.000000000Z")

	mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES ('legacy-edit', 'task.updated', ?, ?, ?, '{"version":3}', '2026-09-05T00:00:00Z')`, agent.ID, project.ID, task.ID)
	_, row = rebuildTaskActivitySignalForTest(t, data, ctx, task.ID)
	assertStoredSignal(t, row, TaskActivityBounded, "", TaskActivityReasonLegacyUpdate, "2026-09-05T00:00:00.000000000Z")
}

// Failure mode 11: an unknown task is not found and writes nothing.
func TestTaskActivitySignalRebuildUnknownTask(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	_, err := data.RebuildTaskActivitySignal(ctx, "no-such-task")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("rebuild unknown task error = %v, want not found", err)
	}
	if _, ok := readTaskActivitySignalRow(t, data, ctx, "no-such-task"); ok {
		t.Fatal("rebuild of unknown task wrote a row")
	}
}
