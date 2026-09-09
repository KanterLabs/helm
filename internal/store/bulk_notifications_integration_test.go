package store

import (
	"encoding/json"
	"errors"
	"testing"
)

func bulkAssignmentInput(actorID string) TaskInput {
	return TaskInput{Assignee: &actorID, AssigneeSet: true}
}

func bulkNotificationPayload(t *testing.T, notification Notification) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(notification.Payload, &payload); err != nil {
		t.Fatalf("decode notification payload: %v", err)
	}
	return payload
}

func bulkTaskUpdatedEvents(t *testing.T, fixture dependencyFixture, taskID string) []Event {
	t.Helper()
	events, _, err := fixture.store.ListEvents(fixture.ctx, EventFilter{ProjectID: fixture.project.ID, Limit: 200})
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	result := make([]Event, 0)
	for _, event := range events {
		if event.Type == "task.updated" && event.TaskID != nil && *event.TaskID == taskID {
			result = append(result, event)
		}
	}
	return result
}

func TestBulkAssignmentEmitsCanonicalMetadataAndOneNotification(t *testing.T) {
	f := newDependencyFixture(t, "BULKNOTIFY")
	recipient, err := f.store.CreateActor(f.ctx, Actor{Kind: "human", Name: "Bulk recipient"}, "")
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	directTask := f.task(t, "direct assignment")
	bulkTask := f.task(t, "bulk assignment")

	if _, err := f.store.UpdateTask(f.ctx, directTask.ID, bulkAssignmentInput(recipient.ID), directTask.Version, f.actor.ID); err != nil {
		t.Fatalf("direct assignment: %v", err)
	}
	directNotifications, _, err := f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list direct notifications: %v", err)
	}
	if len(directNotifications) != 1 {
		t.Fatalf("direct notifications = %d, want one: %+v", len(directNotifications), directNotifications)
	}
	directPayload := bulkNotificationPayload(t, directNotifications[0])
	if directNotifications[0].EventType != "task.updated" || directNotifications[0].Title != "Task assigned to you" {
		t.Fatalf("direct notification = %+v", directNotifications[0])
	}
	if directPayload["assignee"] != recipient.ID || directPayload["previous_assignee"] != "" || directPayload["assignment_changed"] != true {
		t.Fatalf("direct assignment payload = %+v", directPayload)
	}

	batch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: bulkTask.Key, ExpectedVersion: bulkTask.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID),
	}}, false)
	if err != nil {
		t.Fatalf("bulk assignment: %v", err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Status != "applied" {
		t.Fatalf("bulk assignment result = %+v", batch.Results)
	}

	notifications, _, err := f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list bulk notifications: %v", err)
	}
	if len(notifications) != 2 {
		t.Fatalf("notifications after direct and bulk assignment = %d, want two: %+v", len(notifications), notifications)
	}
	var bulkNotification Notification
	for _, notification := range notifications {
		if notification.TaskID != nil && *notification.TaskID == bulkTask.ID {
			bulkNotification = notification
		}
	}
	if bulkNotification.ID == "" {
		t.Fatalf("missing bulk assignment notification: %+v", notifications)
	}
	bulkPayload := bulkNotificationPayload(t, bulkNotification)
	if bulkNotification.EventType != directNotifications[0].EventType || bulkNotification.Title != directNotifications[0].Title {
		t.Fatalf("bulk notification = %+v, direct = %+v", bulkNotification, directNotifications[0])
	}
	if bulkPayload["assignee"] != recipient.ID || bulkPayload["previous_assignee"] != "" || bulkPayload["assignment_changed"] != true {
		t.Fatalf("bulk assignment payload = %+v", bulkPayload)
	}

	events := bulkTaskUpdatedEvents(t, f, bulkTask.ID)
	if len(events) != 1 {
		t.Fatalf("bulk task.updated events = %d, want one", len(events))
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(events[0].Payload, &eventPayload); err != nil {
		t.Fatalf("decode bulk event payload: %v", err)
	}
	if eventPayload["assignee"] != recipient.ID || eventPayload["previous_assignee"] != "" || eventPayload["assignment_changed"] != true || eventPayload["bulk"] != true {
		t.Fatalf("bulk event payload = %+v", eventPayload)
	}
}

func TestBulkAssignmentPartialAndAtomicNotifications(t *testing.T) {
	f := newDependencyFixture(t, "BULKASSIGN")
	recipient, err := f.store.CreateActor(f.ctx, Actor{Kind: "human", Name: "Partial recipient"}, "")
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	first := f.task(t, "partial success")
	second := f.task(t, "partial conflict")
	partial, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: first.Key, ExpectedVersion: first.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID)},
		{Reference: second.Key, ExpectedVersion: second.Version + 1, Operation: "assign", Input: bulkAssignmentInput(recipient.ID)},
	}, false)
	if err != nil {
		t.Fatalf("partial assignment: %v", err)
	}
	if partial.Results[0].Status != "applied" || partial.Results[1].Status != "conflict" {
		t.Fatalf("partial assignment results = %+v", partial.Results)
	}
	notifications, _, err := f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list partial notifications: %v", err)
	}
	if len(notifications) != 1 || notifications[0].TaskID == nil || *notifications[0].TaskID != first.ID {
		t.Fatalf("partial notifications = %+v, want only first task", notifications)
	}

	third := f.task(t, "atomic rollback")
	atomic, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "atomic", []BulkTaskMutation{
		{Reference: third.Key, ExpectedVersion: third.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID)},
		{Reference: second.Key, ExpectedVersion: second.Version + 1, Operation: "assign", Input: bulkAssignmentInput(recipient.ID)},
	}, false)
	if err != nil {
		t.Fatalf("atomic assignment: %v", err)
	}
	if atomic.Results[0].Status != "skipped" || atomic.Results[1].Status != "conflict" || !errors.Is(atomic.Results[1].Err, ErrConflict) {
		t.Fatalf("atomic assignment results = %+v", atomic.Results)
	}
	rolledBack, err := f.store.GetTask(f.ctx, third.ID)
	if err != nil {
		t.Fatalf("read atomic task: %v", err)
	}
	if rolledBack.Assignee != nil || rolledBack.Version != third.Version {
		t.Fatalf("atomic assignment changed task = %+v, want unchanged", rolledBack)
	}
	notifications, _, err = f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list notifications after atomic rollback: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("atomic rollback left notification = %+v", notifications)
	}
	if events := bulkTaskUpdatedEvents(t, f, third.ID); len(events) != 0 {
		t.Fatalf("atomic rollback left task.updated events = %+v", events)
	}
}

func TestBulkAssignmentReplayAndUnchangedValueDoNotNotifyAgain(t *testing.T) {
	f := newDependencyFixture(t, "BULKREPLAY")
	recipient, err := f.store.CreateActor(f.ctx, Actor{Kind: "human", Name: "Replay recipient"}, "")
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	task := f.task(t, "replayed assignment")
	first, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: task.Key, ExpectedVersion: task.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID),
	}}, false)
	if err != nil || first.Results[0].Status != "applied" {
		t.Fatalf("first assignment = %+v, err=%v", first.Results, err)
	}
	updated, err := f.store.GetTask(f.ctx, task.ID)
	if err != nil {
		t.Fatalf("read assigned task: %v", err)
	}

	replay, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: task.Key, ExpectedVersion: task.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID),
	}}, false)
	if err != nil || replay.Results[0].Status != "conflict" {
		t.Fatalf("replay = %+v, err=%v", replay.Results, err)
	}
	notifications, _, err := f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list notifications after replay: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("replay duplicated notifications = %+v", notifications)
	}

	unchanged, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{{
		Reference: task.Key, ExpectedVersion: updated.Version, Operation: "assign", Input: bulkAssignmentInput(recipient.ID),
	}}, false)
	if err != nil || unchanged.Results[0].Status != "applied" {
		t.Fatalf("unchanged assignment = %+v, err=%v", unchanged.Results, err)
	}
	notifications, _, err = f.store.ListNotifications(f.ctx, recipient.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list notifications after unchanged assignment: %v", err)
	}
	if len(notifications) != 1 {
		t.Fatalf("unchanged assignment duplicated notifications = %+v", notifications)
	}
	events := bulkTaskUpdatedEvents(t, f, task.ID)
	if len(events) != 2 {
		t.Fatalf("task.updated events = %d, want successful write and unchanged write", len(events))
	}
	var unchangedPayload map[string]any
	if err := json.Unmarshal(events[1].Payload, &unchangedPayload); err != nil {
		t.Fatalf("decode unchanged event payload: %v", err)
	}
	if unchangedPayload["assignee"] != recipient.ID || unchangedPayload["previous_assignee"] != recipient.ID || unchangedPayload["assignment_changed"] != false {
		t.Fatalf("unchanged assignment event payload = %+v", unchangedPayload)
	}
}

func TestBulkAssignmentNotificationsRespectScopeAndNoOverlap(t *testing.T) {
	f := newDependencyFixture(t, "BULKSCOPE")
	outsideProject, err := f.store.CreateProject(f.ctx, ProjectInput{Key: dependencyStringPtr("BULKOUT"), Name: dependencyStringPtr("Outside")}, f.actor.ID)
	if err != nil {
		t.Fatalf("create outside project: %v", err)
	}
	eligible, err := f.store.CreateAgent(f.ctx, Actor{Kind: "agent", Name: "Scoped recipient", ProjectIDs: []string{f.project.ID}}, f.actor.ID, "")
	if err != nil {
		t.Fatalf("create eligible recipient: %v", err)
	}
	noOverlap, err := f.store.CreateAgent(f.ctx, Actor{Kind: "agent", Name: "Out of scope recipient", ProjectIDs: []string{outsideProject.ID}}, f.actor.ID, "")
	if err != nil {
		t.Fatalf("create out-of-scope recipient: %v", err)
	}
	eligibleTask := f.task(t, "scoped assignment")
	noOverlapTask := f.task(t, "out-of-scope assignment")
	if _, err := f.store.CreateTaskWatch(f.ctx, eligible.ID, eligibleTask.Key); err != nil {
		t.Fatalf("create overlapping task watch: %v", err)
	}
	if _, err := f.store.CreateTaskWatch(f.ctx, noOverlap.ID, eligibleTask.Key); err != nil {
		t.Fatalf("create out-of-scope task watch: %v", err)
	}

	batch, err := f.store.ApplyBulkTaskMutations(f.ctx, f.project.ID, f.actor.ID, "partial", []BulkTaskMutation{
		{Reference: eligibleTask.Key, ExpectedVersion: eligibleTask.Version, Operation: "assign", Input: bulkAssignmentInput(eligible.ID)},
		{Reference: noOverlapTask.Key, ExpectedVersion: noOverlapTask.Version, Operation: "assign", Input: bulkAssignmentInput(noOverlap.ID)},
	}, false)
	if err != nil {
		t.Fatalf("scoped bulk assignments: %v", err)
	}
	if batch.Results[0].Status != "applied" || batch.Results[1].Status != "applied" {
		t.Fatalf("scoped assignment results = %+v", batch.Results)
	}

	visible, _, err := f.store.ListNotifications(f.ctx, eligible.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list eligible notifications: %v", err)
	}
	if len(visible) != 1 || visible[0].TaskID == nil || *visible[0].TaskID != eligibleTask.ID {
		t.Fatalf("overlapping recipient notifications = %+v, want one direct notification", visible)
	}
	targetScoped, _, err := f.store.ListNotifications(f.ctx, eligible.ID, NotificationFilter{ProjectIDs: []string{f.project.ID}, Limit: 20})
	if err != nil {
		t.Fatalf("list target-scoped notifications: %v", err)
	}
	if len(targetScoped) != 1 {
		t.Fatalf("target-scoped notifications = %+v, want one", targetScoped)
	}
	outsideScoped, _, err := f.store.ListNotifications(f.ctx, eligible.ID, NotificationFilter{ProjectIDs: []string{outsideProject.ID}, Limit: 20})
	if err != nil {
		t.Fatalf("list outside-scoped notifications: %v", err)
	}
	if len(outsideScoped) != 0 {
		t.Fatalf("outside-scoped notifications leaked = %+v", outsideScoped)
	}
	blocked, _, err := f.store.ListNotifications(f.ctx, noOverlap.ID, NotificationFilter{Limit: 20})
	if err != nil {
		t.Fatalf("list out-of-scope notifications: %v", err)
	}
	if len(blocked) != 0 {
		t.Fatalf("out-of-scope recipient notifications = %+v", blocked)
	}
}
