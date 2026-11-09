package db

import (
	"context"
	"strings"
	"testing"
)

// Migration 044 (TC-379) adds two indexes on task_agent_work_history so the
// timeline source queries, which the neglect-signal projector replays for
// every rebuilt task, look up a history row by event cursor or generated
// comment instead of scanning the table once per event or comment. It must
// be additive: no row changes, nothing dropped.
const taskActivitySignalIndexMigration = 44

func TestTaskActivitySignalIndexMigrationIsAdditiveAndUsedByTimeline(t *testing.T) {
	ctx := context.Background()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < taskActivitySignalIndexMigration {
		t.Fatalf("migration %03d is not present", taskActivitySignalIndexMigration)
	}
	database := newRawDatabase(t)
	applyMigrationPrefix(t, ctx, database, migrations, taskActivitySignalIndexMigration-1)
	populateProductionFixture(t, ctx, database, taskActivitySignalIndexMigration-1)
	populateTaskActivitySignalHistory(t, ctx, database, 30, 10)
	before := taskActivitySignalCounts(t, ctx, database)
	beforeStable := readStableFixture(t, ctx, database, taskActivitySignalIndexMigration-1)
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("migrate populated pre-%03d database: %v", taskActivitySignalIndexMigration, err)
	}
	after := taskActivitySignalCounts(t, ctx, database)
	for _, table := range []string{"tasks", "events", "comments", "task_agent_work_history"} {
		if before[table] != after[table] {
			t.Fatalf("%s count changed by migration: %d -> %d", table, before[table], after[table])
		}
	}
	assertStableFixtureMigration(t, beforeStable, readStableFixture(t, ctx, database, taskActivitySignalIndexMigration))

	plans := map[string]string{
		"task_agent_work_history_progress_event_idx":    `SELECT 1 FROM task_agent_work_history h WHERE h.progress_event_cursor=?`,
		"task_agent_work_history_generated_comment_idx": `SELECT 1 FROM task_agent_work_history h WHERE h.generated_comment_id=?`,
	}
	for index, query := range plans {
		rows, err := database.QueryContext(ctx, `EXPLAIN QUERY PLAN `+query, "x")
		if err != nil {
			t.Fatal(err)
		}
		var detail strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var text string
			if err := rows.Scan(&id, &parent, &unused, &text); err != nil {
				t.Fatal(err)
			}
			detail.WriteString(text + "; ")
		}
		rows.Close()
		if !strings.Contains(detail.String(), index) {
			t.Fatalf("%s does not use %s: %s", query, index, detail.String())
		}
	}
}
