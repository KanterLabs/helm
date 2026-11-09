package store

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// TC-380 item 8b (test/neglect/PROJECTOR.md "Known limit"): the comment
// source looked up each comment's comment.created cursor with a correlated
// json_extract scan of the task's events, so one rebuild cost O(comments x
// comment events). These tests were written before the query changed: the
// rewritten source must return exactly what the original query returned,
// and its plan must read the task's events once, not once per comment.

// referenceTimelineCommentsQuery is the original source query, verbatim.
const referenceTimelineCommentsQuery = `WITH source AS (
		SELECT c.id, c.task_id, c.actor_id, c.body, c.version, c.created_at, c.updated_at, c.deleted_at,
			COALESCE((
				SELECT MAX(ce.cursor)
				FROM events ce
				WHERE ce.task_id=c.task_id
				  AND ce.type='comment.created'
				  AND json_valid(ce.payload)
				  AND json_extract(ce.payload, '$.comment_id')=c.id
			), 0) AS timeline_event_cursor,
			a.id AS timeline_actor_id, a.kind AS timeline_actor_kind, a.name AS timeline_actor_name
		FROM comments c
		LEFT JOIN actors a ON a.id=c.actor_id
		WHERE c.task_id=?
		  AND c.deleted_at IS NULL
		  AND NOT EXISTS (
			  SELECT 1 FROM task_agent_work_history h
			  WHERE h.generated_comment_id=c.id
		  )
	)
	SELECT id, task_id, actor_id, body, version, created_at, updated_at, deleted_at,
		timeline_event_cursor, timeline_actor_id, timeline_actor_kind, timeline_actor_name
	FROM source`

func referenceTimelineComments(t *testing.T, data *Store, ctx context.Context, taskID string, boundary timelineSortKey, limit int) []timelineCommentRow {
	t.Helper()
	predicate, predicateArgs := timelineKeysetPredicate("created_at", "timeline_event_cursor", "id", "comment", boundary)
	query, args := referenceTimelineCommentsQuery, []any{taskID}
	if predicate != "" {
		query += " WHERE " + predicate
		args = append(args, predicateArgs...)
	}
	query += ` ORDER BY created_at DESC, timeline_event_cursor DESC, id DESC LIMIT ?`
	rows, err := data.DB.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []timelineCommentRow
	for rows.Next() {
		var row timelineCommentRow
		var deletedAt sql.NullString
		if err := rows.Scan(&row.comment.ID, &row.comment.TaskID, &row.comment.ActorID, &row.comment.Body, &row.comment.Version, &row.comment.CreatedAt, &row.comment.UpdatedAt, &deletedAt, &row.timelineEventCursor, &row.actorID, &row.actorKind, &row.actorName); err != nil {
			t.Fatal(err)
		}
		row.comment.DeletedAt = nullableString(deletedAt)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTimelineCommentSourceMatchesOriginalQuery(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	agent, project, task := createAgentWorkFixture(t, data, ctx, "TLC")
	other := createProjectorTasksForTest(t, data, ctx, project.ID, agent.ID, 1)[0]
	human := createHumanSignalActor(t, data, ctx, "Timeline human")
	var comments []Comment
	for index := 0; index < 6; index++ {
		comment, err := data.CreateComment(ctx, task.ID, human.ID, "Evidence")
		if err != nil {
			t.Fatal(err)
		}
		comments = append(comments, comment)
	}
	insert := func(taskID, payload, at string) {
		mustExecSignalSQL(t, data, ctx, `INSERT INTO events(id, type, actor_id, project_id, task_id, payload, created_at) VALUES (?, 'comment.created', ?, ?, ?, ?, ?)`,
			newID(), human.ID, project.ID, taskID, payload, at)
	}
	// A second comment.created for one comment: the newest cursor wins.
	insert(task.ID, `{"comment_id":"`+comments[1].ID+`"}`, comments[1].CreatedAt)
	// The event for one comment lives on another task: no cursor.
	mustExecSignalSQL(t, data, ctx, `UPDATE events SET task_id=? WHERE type='comment.created' AND json_extract(payload, '$.comment_id')=?`, other.ID, comments[2].ID)
	// Malformed and unrelated payloads never match.
	insert(task.ID, `{"comment_id":"`+comments[3].ID, comments[3].CreatedAt)
	insert(task.ID, `{"other":"`+comments[3].ID+`"}`, comments[3].CreatedAt)
	// An all-digit comment id matched by a numeric and a text payload.
	mustExecSignalSQL(t, data, ctx, `INSERT INTO comments(id, task_id, actor_id, body, version, created_at, updated_at) VALUES ('12345', ?, ?, 'Digits', 1, ?, ?)`,
		task.ID, human.ID, comments[0].CreatedAt, comments[0].CreatedAt)
	insert(task.ID, `{"comment_id":12345}`, comments[0].CreatedAt)
	insert(task.ID, `{"comment_id":"12345"}`, comments[0].CreatedAt)
	// A comment without any event, and a deleted comment.
	mustExecSignalSQL(t, data, ctx, `DELETE FROM events WHERE type='comment.created' AND json_valid(payload) AND json_extract(payload, '$.comment_id')=?`, comments[4].ID)
	if err := data.DeleteComment(ctx, task.ID, comments[5].ID, human.ID, comments[5].Version, false); err != nil {
		t.Fatal(err)
	}
	// Equal timestamps exercise the cursor and id tie breakers.
	mustExecSignalSQL(t, data, ctx, `UPDATE comments SET created_at=? WHERE task_id=?`, comments[0].CreatedAt, task.ID)

	full := referenceTimelineComments(t, data, ctx, task.ID, timelineSortKey{}, 100)
	if len(full) != 6 {
		t.Fatalf("reference returned %d comments", len(full))
	}
	boundaries := []timelineSortKey{{}}
	for _, row := range full {
		boundaries = append(boundaries, newTimelineSortKey(row.comment.CreatedAt, "comment", row.comment.ID, row.timelineEventCursor))
	}
	for _, boundary := range boundaries {
		for _, limit := range []int{1, 3, 100} {
			want := referenceTimelineComments(t, data, ctx, task.ID, boundary, limit)
			got, err := data.listTimelineComments(ctx, task.ID, boundary, limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 && len(want) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("boundary %+v limit %d:\n got %+v\nwant %+v", boundary, limit, got, want)
			}
		}
	}
}

func TestTimelineCommentSourceReadsEventsOncePerTask(t *testing.T) {
	data, ctx := newTaskActivitySignalStore(t)
	_, _, task := createAgentWorkFixture(t, data, ctx, "TLCPLAN")
	var query string
	data.observeQuery = func(text string, _ []any) {
		if strings.Contains(text, "FROM comments c") {
			query = text
		}
	}
	if _, err := data.listTimelineComments(ctx, task.ID, timelineSortKey{}, 10); err != nil {
		t.Fatal(err)
	}
	if query == "" {
		t.Fatal("comment source query was not observed")
	}
	args := []any{}
	for range strings.Count(query, "?") {
		args = append(args, task.ID)
	}
	args[len(args)-1] = 10
	rows, err := data.DB.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	parents := map[int]string{}
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		parents[id] = detail
		plan = append(plan, detail)
		reads := strings.Contains(detail, " ce ") || strings.HasSuffix(detail, " ce") || strings.Contains(detail, " events ")
		if reads && strings.HasPrefix(parents[parent], "CORRELATED") {
			t.Fatalf("events are read per comment: %v", plan)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "MATERIALIZE") {
		t.Fatalf("comment events are not materialized once: %v", plan)
	}
	// Each comment must find its cursor through an index, not by scanning
	// the materialized events (still quadratic, only cheaper).
	if !strings.Contains(joined, "SEARCH cr USING") {
		t.Fatalf("comment cursor lookup is not indexed: %v", plan)
	}
}
