package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
)

// Publication can fail through unknown, duplicate, foreign, unready, deleted,
// already-linked, or excessive IDs; no comment, pulse, event, or task version
// may survive such a failure. History must retain images after the live pulse
// is replaced, with one canonical group and no images from deleted comments.
func TestActivityAttachmentsPublishAtomicallyAndRetainHistory(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	actor, project, task := createAgentWorkFixture(t, data, ctx, "IMAGES")
	stage := func(id, uploader, status string, ready int) {
		t.Helper()
		_, err := database.ExecContext(ctx, `INSERT INTO task_attachments(id, task_id, project_id, actor_id, filename, content_type, size_bytes, sha256, width, height, status, ready, created_at, updated_at) VALUES (?, ?, ?, ?, 'proof.png', 'image/png', 12, ?, 2, 2, ?, ?, ?, ?)`, id, task.ID, project.ID, uploader, strings.Repeat("a", 64), status, ready, now(), now())
		if err != nil {
			t.Fatal(err)
		}
	}
	stage("comment-image", actor.ID, "staged", 1)
	comment, err := data.CreateCommentWithAttachments(ctx, task.ID, actor.ID, "", []string{"comment-image"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(comment.Attachments) != 1 || comment.Attachments[0].ID != "comment-image" {
		t.Fatalf("comment attachments: %+v", comment)
	}
	comments, err := data.ListComments(ctx, task.ID, 100, 0)
	if err != nil || len(comments) != 1 || len(comments[0].Attachments) != 1 {
		t.Fatalf("list: %+v %v", comments, err)
	}

	stage("recoverable", actor.ID, "staged", 1)
	stage("unready", actor.ID, "staged", 0)
	other, err := data.CreateActor(ctx, Actor{Kind: "agent", Name: "Other uploader"}, "")
	if err != nil {
		t.Fatal(err)
	}
	stage("foreign", other.ID, "staged", 1)
	count := func(table string) int {
		t.Helper()
		var count int
		if err := database.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	beforeEvents := count("events")
	for _, ids := range [][]string{{"recoverable", "missing"}, {"recoverable", "recoverable"}, {"unready"}, {"foreign"}, {"comment-image"}, {"a", "b", "c", "d", "e"}} {
		if _, err := data.CreateCommentWithAttachments(ctx, task.ID, actor.ID, "rolled back", ids, false); err == nil {
			t.Fatalf("accepted invalid IDs: %v", ids)
		}
		if count("comments") != 1 || count("events") != beforeEvents || count("task_attachment_links") != 1 {
			t.Fatalf("partial publication for %v", ids)
		}
	}
	claimed, err := data.ClaimTask(ctx, task.ID, actor.ID, time.Hour, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.PublishAgentWork(ctx, task.ID, AgentWorkInput{OperationID: "images/run", State: "working", Summary: "Bad pulse", AttachmentIDs: []string{"recoverable", "missing"}}, claimed.Version, actor.ID); err == nil {
		t.Fatal("accepted invalid pulse")
	}
	current, err := data.GetTask(ctx, task.ID)
	if err != nil || current.Version != claimed.Version || current.AgentWork != nil || count("task_agent_work_history") != 0 {
		t.Fatalf("partial pulse: %+v %v", current, err)
	}
	published, err := data.PublishAgentWork(ctx, task.ID, AgentWorkInput{OperationID: "images/run", State: "working", Summary: "Proof pulse", AttachmentIDs: []string{"recoverable"}}, claimed.Version, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.PublishAgentWork(ctx, task.ID, AgentWorkInput{OperationID: "images/run", State: "verifying", Summary: "Later pulse"}, published.Version, actor.ID); err != nil {
		t.Fatal(err)
	}
	for _, projectView := range []bool{false, true} {
		items, _, err := data.ListTaskTimeline(ctx, task.ID, TaskTimelineFilter{})
		if projectView {
			items, _, err = data.ListProjectTimeline(ctx, project.ID, TaskTimelineFilter{})
		}
		if err != nil {
			t.Fatal(err)
		}
		progressCount, imageGroups := 0, 0
		for _, item := range items {
			if item.Progress != nil {
				progressCount++
				if len(item.Progress.Attachments) != 0 {
					imageGroups++
					if item.Progress.Summary != "Proof pulse" || item.Progress.Attachments[0].ID != "recoverable" {
						t.Fatalf("wrong history image: %+v", item)
					}
				}
			}
			if item.Comment != nil && len(item.Comment.Attachments) > 0 {
				imageGroups++
			}
		}
		if progressCount != 2 || imageGroups != 2 {
			t.Fatalf("history duplication/loss: progress=%d groups=%d", progressCount, imageGroups)
		}
	}
	if err := data.DeleteComment(ctx, task.ID, comment.ID, actor.ID, comment.Version, false); err != nil {
		t.Fatal(err)
	}
	items, _, err := data.ListTaskTimeline(ctx, task.ID, TaskTimelineFilter{Kind: "comment"})
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted comment visible: %+v %v", items, err)
	}
}
