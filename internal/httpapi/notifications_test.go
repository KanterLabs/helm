package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

func TestNotificationRoutesWatchReadAndPreferences(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("INBOX"), Name: stringPtr("Inbox")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := data.CreateActor(ctx, store.Actor{Kind: "human", Name: "Other"}, "")
	if err != nil {
		t.Fatal(err)
	}
	task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Watched")}, other.ID)
	if err != nil {
		t.Fatal(err)
	}

	watch := request(t, server, http.MethodPost, "/api/v1/projects/"+project.ID+"/watch", map[string]any{}, nil)
	if watch.Code != http.StatusCreated {
		t.Fatalf("create watch status = %d, body=%s", watch.Code, watch.Body.String())
	}
	var watchBody store.Watch
	if err := json.Unmarshal(watch.Body.Bytes(), &watchBody); err != nil {
		t.Fatal(err)
	}
	if watchBody.ActorID != actor.ID || watchBody.ProjectID != project.ID {
		t.Fatalf("watch = %+v", watchBody)
	}
	updated, err := data.CompleteTask(ctx, task.ID, other.ID, task.Version)
	if err != nil {
		t.Fatal(err)
	}
	_ = updated
	inbox := request(t, server, http.MethodGet, "/api/v1/notifications?unread=true", nil, nil)
	if inbox.Code != http.StatusOK || inbox.Body.Len() == 0 {
		t.Fatalf("notifications status = %d, body=%s", inbox.Code, inbox.Body.String())
	}
	var collection struct {
		Data []store.Notification `json:"data"`
	}
	if err := json.Unmarshal(inbox.Body.Bytes(), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Data) != 1 || collection.Data[0].ReadAt != nil {
		t.Fatalf("inbox = %+v", collection.Data)
	}
	read := request(t, server, http.MethodPost, "/api/v1/notifications/"+collection.Data[0].ID+"/read", map[string]any{}, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("mark read alias status = %d, body=%s", read.Code, read.Body.String())
	}
	unread := request(t, server, http.MethodPatch, "/api/v1/notifications/"+collection.Data[0].ID, map[string]any{"read": false}, nil)
	var unreadBody store.Notification
	if unread.Code != http.StatusOK || json.Unmarshal(unread.Body.Bytes(), &unreadBody) != nil || unreadBody.ReadAt != nil {
		t.Fatalf("mark unread status = %d, body=%s", unread.Code, unread.Body.String())
	}
	prefs := request(t, server, http.MethodPatch, "/api/v1/notification-preferences", map[string]any{"mentions": false}, nil)
	if prefs.Code != http.StatusOK || !strings.Contains(prefs.Body.String(), `"mentions":false`) {
		t.Fatalf("preferences status = %d, body=%s", prefs.Code, prefs.Body.String())
	}
	all := request(t, server, http.MethodPost, "/api/v1/notifications/read", map[string]any{"all": true}, nil)
	if all.Code != http.StatusOK || !strings.Contains(all.Body.String(), `"marked_read"`) {
		t.Fatalf("mark all status = %d, body=%s", all.Code, all.Body.String())
	}
}

func TestNotificationReadMutationRedactsWriteOnlyResponse(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	admin, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("REDACT"), Name: stringPtr("Redaction")}, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Notification writer", ProjectIDs: []string{project.ID}}, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateTokenBy(ctx, writer.ID, admin.ID, "notification-write-only", []string{"notifications:write"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(ctx, `INSERT INTO notifications(id, recipient_id, actor_id, event_type, project_id, title, body, payload, dedupe_key, created_at, updated_at) VALUES (?, ?, ?, 'task.updated', ?, ?, ?, ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, "notification-redacted", writer.ID, admin.ID, project.ID, "private notification title", "private notification body", `{"secret":"private notification payload"}`, "dedupe-redacted"); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	read := request(t, server, http.MethodPost, "/api/v1/notifications/notification-redacted/read", map[string]any{}, headers)
	assertRedactedNotificationMutation(t, read, "read")
	if !strings.Contains(read.Body.String(), `"read_at":"`) {
		t.Fatalf("mark-read acknowledgement omitted timestamp: %s", read.Body.String())
	}

	unread := request(t, server, http.MethodPatch, "/api/v1/notifications/notification-redacted", map[string]any{"read": false}, headers)
	assertRedactedNotificationMutation(t, unread, "unread")
	if unread.Body.String() != `{"id":"notification-redacted","read_at":null}` {
		t.Fatalf("mark-unread acknowledgement = %s", unread.Body.String())
	}
}

func assertRedactedNotificationMutation(t *testing.T, response *httptest.ResponseRecorder, operation string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("%s notification status = %d, body=%s", operation, response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s notification acknowledgement: %v", operation, err)
	}
	if len(body) != 2 || string(body["id"]) != `"notification-redacted"` {
		t.Fatalf("%s notification acknowledgement leaked fields: %s", operation, response.Body.String())
	}
	if _, ok := body["read_at"]; !ok {
		t.Fatalf("%s notification acknowledgement omitted read_at: %s", operation, response.Body.String())
	}
	for _, secret := range []string{"private notification title", "private notification body", "private notification payload", "recipient_id", "actor_id", "project_id", "task_id", "event_type", "dedupe_key"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("%s notification acknowledgement leaked %q: %s", operation, secret, response.Body.String())
		}
	}
}

func TestScopedNotificationInboxHonorsProjectCeiling(t *testing.T) {
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("INBOXA"), Name: stringPtr("Inbox A")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("INBOXB"), Name: stringPtr("Inbox B")}, actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Inbox reader", ProjectIDs: []string{first.ID}}, actor.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "inbox-reader", []string{"notifications:read", "notifications:write"}, []string{first.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id        string
		projectID any
		title     string
		dedupe    string
	}{
		{id: "notification-first", projectID: first.ID, title: first.Name, dedupe: "dedupe-" + first.ID},
		{id: "notification-second", projectID: second.ID, title: second.Name, dedupe: "dedupe-" + second.ID},
		{id: "notification-global", projectID: nil, title: "Global", dedupe: "dedupe-global"},
	} {
		if _, err := data.DB.ExecContext(ctx, `INSERT INTO notifications(id, recipient_id, event_type, project_id, title, body, payload, dedupe_key, created_at, updated_at) VALUES (?, ?, 'task.updated', ?, ?, 'body', '{}', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, item.id, agent.ID, item.projectID, item.title, item.dedupe); err != nil {
			t.Fatal(err)
		}
	}
	inbox := request(t, server, http.MethodGet, "/api/v1/notifications", nil, map[string]string{"Authorization": "Bearer " + token})
	if inbox.Code != http.StatusOK {
		t.Fatalf("scoped inbox status = %d, body=%s", inbox.Code, inbox.Body.String())
	}
	var collection struct {
		Data []store.Notification `json:"data"`
	}
	if err := json.Unmarshal(inbox.Body.Bytes(), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Data) != 1 || collection.Data[0].ProjectID == nil || *collection.Data[0].ProjectID != first.ID {
		t.Fatalf("scoped inbox = %+v", collection.Data)
	}
	outsideRead := request(t, server, http.MethodPatch, "/api/v1/notifications/notification-second", map[string]any{"read": true}, map[string]string{"Authorization": "Bearer " + token})
	if outsideRead.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope single read status = %d, body=%s", outsideRead.Code, outsideRead.Body.String())
	}
	outsideAlias := request(t, server, http.MethodPost, "/api/v1/notifications/notification-second/read", map[string]any{}, map[string]string{"Authorization": "Bearer " + token})
	if outsideAlias.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope read alias status = %d, body=%s", outsideAlias.Code, outsideAlias.Body.String())
	}
	all := request(t, server, http.MethodPost, "/api/v1/notifications/read", map[string]any{"all": true}, map[string]string{"Authorization": "Bearer " + token})
	if all.Code != http.StatusOK || !strings.Contains(all.Body.String(), `"marked_read":1`) {
		t.Fatalf("scoped mark all = %d, body=%s", all.Code, all.Body.String())
	}
	bulk := request(t, server, http.MethodPost, "/api/v1/notifications/read", map[string]any{"ids": []string{"notification-second", "notification-global"}}, map[string]string{"Authorization": "Bearer " + token})
	if bulk.Code != http.StatusOK || !strings.Contains(bulk.Body.String(), `"marked_read":0`) {
		t.Fatalf("scoped out-of-scope bulk = %d, body=%s", bulk.Code, bulk.Body.String())
	}
	var firstRead, secondRead, globalRead *string
	if err := data.DB.QueryRowContext(ctx, `SELECT read_at FROM notifications WHERE id=?`, "notification-first").Scan(&firstRead); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.QueryRowContext(ctx, `SELECT read_at FROM notifications WHERE id=?`, "notification-second").Scan(&secondRead); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.QueryRowContext(ctx, `SELECT read_at FROM notifications WHERE id=?`, "notification-global").Scan(&globalRead); err != nil {
		t.Fatal(err)
	}
	if firstRead == nil || secondRead != nil || globalRead != nil {
		t.Fatalf("scoped read mutation leaked: first=%v second=%v global=%v", firstRead, secondRead, globalRead)
	}
	if _, err := data.CreateProjectWatch(ctx, agent.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := data.CreateProjectWatch(ctx, agent.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	watches := request(t, server, http.MethodGet, "/api/v1/watches", nil, map[string]string{"Authorization": "Bearer " + token})
	if watches.Code != http.StatusOK || !strings.Contains(watches.Body.String(), first.ID) || strings.Contains(watches.Body.String(), second.ID) {
		t.Fatalf("scoped watches = %d, body=%s", watches.Code, watches.Body.String())
	}
	_, noOverlapToken, err := data.CreateTokenBy(ctx, agent.ID, actor.ID, "inbox-no-overlap", []string{"notifications:read"}, []string{second.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	noOverlap := request(t, server, http.MethodGet, "/api/v1/notifications", nil, map[string]string{"Authorization": "Bearer " + noOverlapToken})
	if noOverlap.Code != http.StatusOK || !strings.Contains(noOverlap.Body.String(), `"data":[]`) {
		t.Fatalf("no-overlap inbox = %d, body=%s", noOverlap.Code, noOverlap.Body.String())
	}
}
