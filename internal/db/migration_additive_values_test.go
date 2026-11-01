package db

import (
	"context"
	"database/sql"
	"testing"
)

func assertMigratedAdditiveValues(t *testing.T, ctx context.Context, database *sql.DB, prefix int) {
	t.Helper()
	var value string
	if err := database.QueryRowContext(ctx, `SELECT next_number FROM project_counters WHERE project_id='project'`).Scan(&value); err != nil || value != "3" {
		t.Fatalf("project counter = %q, %v; want 3", value, err)
	}
	if prefix >= 4 {
		if err := database.QueryRowContext(ctx, `SELECT actor_id || ':' || project_id FROM actor_projects WHERE actor_id='agent' AND project_id='project'`).Scan(&value); err != nil || value != "agent:project" {
			t.Fatalf("actor project grant = %q, %v; want agent:project", value, err)
		}
	}
	if prefix >= 6 {
		if err := database.QueryRowContext(ctx, `SELECT actor_id || ':' || reserved_bytes FROM actor_resource_usage WHERE actor_id='agent'`).Scan(&value); err != nil || value != "agent:42" {
			t.Fatalf("actor resource usage = %q, %v; want agent:42", value, err)
		}
	}
	if prefix >= 7 {
		if err := database.QueryRowContext(ctx, `SELECT COALESCE(response_location,'') FROM idempotency_keys WHERE actor_id='owner' AND key='fixture-key'`).Scan(&value); err != nil || value != "/api/v1/tasks/task-1" {
			t.Fatalf("idempotency response location = %q, %v; want task location", value, err)
		}
	}
	if err := database.QueryRowContext(ctx, `SELECT kind FROM tasks WHERE id='task-1'`).Scan(&value); err != nil || value != "task" {
		t.Fatalf("task kind = %q, %v; want task", value, err)
	}
	if prefix >= 8 {
		if err := database.QueryRowContext(ctx, `SELECT actual_behavior FROM bug_details WHERE task_id='bug-1'`).Scan(&value); err != nil || value != "It fails" {
			t.Fatalf("bug details = %q, %v; want fixture value", value, err)
		}
	} else {
		var bugs int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM bug_details WHERE task_id='bug-1'`).Scan(&bugs); err != nil || bugs != 0 {
			t.Fatalf("pre-008 bug details = %d, %v; want none", bugs, err)
		}
	}
	var checklistCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_checklist_items WHERE task_id='task-1'`).Scan(&checklistCount); err != nil {
		t.Fatalf("checklist table after migration: %v", err)
	}
	if prefix >= 15 {
		if checklistCount != 1 {
			t.Fatalf("checklist rows after retained-data migration = %d, want 1", checklistCount)
		}
		if err := database.QueryRowContext(ctx, `SELECT text FROM task_checklist_items WHERE id='checklist-1'`).Scan(&value); err != nil || value != "Preserve this acceptance criterion" {
			t.Fatalf("checklist retained value = %q, %v", value, err)
		}
	} else if checklistCount != 0 {
		t.Fatalf("new checklist table unexpectedly populated during migration = %d", checklistCount)
	}
	if err := database.QueryRowContext(ctx, `SELECT checklist_completion_policy FROM projects WHERE id='project'`).Scan(&value); err != nil || value != "warn" {
		t.Fatalf("checklist completion policy after migration = %q, %v; want warn", value, err)
	}
	var lunaRunCount int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM luna_runs WHERE actor_id='owner'`).Scan(&lunaRunCount); err != nil {
		t.Fatalf("luna run history table after migration: %v", err)
	}
	if prefix >= 26 {
		if lunaRunCount != 1 {
			t.Fatalf("luna run rows after retained-data migration = %d, want 1", lunaRunCount)
		}
		if err := database.QueryRowContext(ctx, `SELECT feature || ':' || outcome || ':' || detail FROM luna_runs WHERE id='luna-run-1'`).Scan(&value); err != nil || value != "task_draft:succeeded:validated" {
			t.Fatalf("luna run retained value = %q, %v", value, err)
		}
	} else if lunaRunCount != 0 {
		t.Fatalf("new luna run table unexpectedly populated during migration = %d", lunaRunCount)
	}
	var collectionRevision int64
	wantCollectionRevision := int64(0)
	if prefix >= 20 {
		// When the fixture is populated after migration 020, its two retained
		// task inserts are intentionally observed by the compatibility triggers.
		wantCollectionRevision = 2
	}
	if err := database.QueryRowContext(ctx, `SELECT task_collection_revision FROM projects WHERE id='project'`).Scan(&collectionRevision); err != nil || collectionRevision != wantCollectionRevision {
		t.Fatalf("task collection revision after migration = %d, %v; want %d", collectionRevision, err, wantCollectionRevision)
	}
	var commentVersion int64
	if err := database.QueryRowContext(ctx, `SELECT version FROM comments WHERE id='comment-1'`).Scan(&commentVersion); err != nil || commentVersion != 1 {
		t.Fatalf("comment lifecycle version = %d, %v; want default 1", commentVersion, err)
	}
	var deletedAt sql.NullString
	if err := database.QueryRowContext(ctx, `SELECT deleted_at FROM comments WHERE id='comment-1'`).Scan(&deletedAt); err != nil || deletedAt.Valid {
		t.Fatalf("comment lifecycle tombstone = %v, %v; want live NULL", deletedAt, err)
	}
}
