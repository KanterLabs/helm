package store

import (
	"context"
	"testing"

	"github.com/KanterLabs/helm/internal/db"
)

func TestNotificationsWatchesMentionsPreferencesAndLifecycle(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	owner, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	assignee, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Assignee"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: ptr("NOTICE"), Name: ptr("Notifications")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, project.ID, TaskInput{Title: ptr("Watch me"), Assignee: &assignee.ID, AssigneeSet: true}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	notifications, _, err := data.ListNotifications(ctx, assignee.ID, NotificationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].EventType != "task.created" || notifications[0].ReadAt != nil {
		t.Fatalf("assignment notification = %+v", notifications)
	}
	if _, err := data.MarkNotificationRead(ctx, assignee.ID, notifications[0].ID); err != nil {
		t.Fatal(err)
	}
	if count, err := data.UnreadNotificationCount(ctx, assignee.ID); err != nil || count != 0 {
		t.Fatalf("unread count = %d, %v; want 0", count, err)
	}

	if _, err := data.CreateProjectWatch(ctx, assignee.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateComment(ctx, task.ID, owner.ID, "Please review this, @Assignee"); err != nil {
		t.Fatal(err)
	}
	notifications, _, err = data.ListNotifications(ctx, assignee.ID, NotificationFilter{UnreadOnly: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 || notifications[0].EventType != "comment.created" {
		t.Fatalf("mention notification = %+v", notifications)
	}
	if _, err := data.UpdateNotificationPreferences(ctx, assignee.ID, NotificationPreferencesInput{Mentions: boolPtr(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateComment(ctx, task.ID, owner.ID, "No ping, @Assignee"); err != nil {
		t.Fatal(err)
	}
	notifications, _, err = data.ListNotifications(ctx, assignee.ID, NotificationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 {
		t.Fatalf("stored notifications after preference = %d, want 2", len(notifications))
	}
}

func TestNotificationFanoutHonorsDisabledActorsAndProjectCeilings(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	owner, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: ptr("ELIGIBLE"), Name: ptr("Eligible")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	outside, err := data.CreateProject(ctx, ProjectInput{Key: ptr("OUTSIDE"), Name: ptr("Outside")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := data.CreateAgent(ctx, Actor{Kind: "agent", Name: "Restricted", ProjectIDs: []string{outside.ID}}, owner.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Disabled"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateProjectWatch(ctx, restricted.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateProjectWatch(ctx, disabled.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	disabledAt := now()
	if _, err := database.ExecContext(ctx, `UPDATE actors SET disabled_at=? WHERE id=?`, disabledAt, disabled.ID); err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, project.ID, TaskInput{Title: ptr("Eligibility")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []Actor{restricted, disabled} {
		items, _, listErr := data.ListNotifications(ctx, actor.ID, NotificationFilter{Limit: 20})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(items) != 0 {
			t.Fatalf("watch notifications for ineligible actor %s = %+v", actor.ID, items)
		}
	}

	if _, err := data.UpdateTaskWithClaimOverride(ctx, task.ID, TaskInput{Assignee: &restricted.ID, AssigneeSet: true}, task.Version, owner.ID, true); err != nil {
		t.Fatal(err)
	}
	items, _, err := data.ListNotifications(ctx, restricted.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("assignment notification for restricted actor = %+v", items)
	}
	if _, err := data.CreateComment(ctx, task.ID, owner.ID, "No ping, @Restricted @Disabled"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []Actor{restricted, disabled} {
		items, _, listErr := data.ListNotifications(ctx, actor.ID, NotificationFilter{Limit: 20})
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(items) != 0 {
			t.Fatalf("mention notifications for ineligible actor %s = %+v", actor.ID, items)
		}
	}
}

func TestDependencyUnblockEventNotifiesProjectWatchers(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	data := New(database)
	owner, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Owner"}, "")
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := data.CreateActor(ctx, Actor{Kind: "human", Name: "Watcher"}, "")
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, ProjectInput{Key: ptr("UNBLOCK"), Name: ptr("Unblock")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	prerequisite, err := data.CreateTask(ctx, project.ID, TaskInput{Title: ptr("Prerequisite")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := data.CreateTask(ctx, project.ID, TaskInput{Title: ptr("Dependent")}, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateProjectWatch(ctx, watcher.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.AddTaskDependency(ctx, dependent.ID, prerequisite.ID, dependent.Version, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CompleteTask(ctx, prerequisite.ID, owner.ID, prerequisite.Version); err != nil {
		t.Fatal(err)
	}
	notifications, _, err := data.ListNotifications(ctx, watcher.ID, NotificationFilter{UnreadOnly: true, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, notification := range notifications {
		if notification.EventType == "task.dependency_state_changed" {
			return
		}
	}
	t.Fatalf("unblock notifications = %+v", notifications)
}

func ptr(value string) *string { return &value }
func boolPtr(value bool) *bool { return &value }
