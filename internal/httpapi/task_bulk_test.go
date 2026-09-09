package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

type bulkTestResult struct {
	Reference string      `json:"reference"`
	TaskID    string      `json:"task_id"`
	Status    string      `json:"status"`
	Version   int64       `json:"version"`
	Task      *store.Task `json:"task"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type bulkTestResponse struct {
	Mode      string           `json:"mode"`
	Status    string           `json:"status"`
	Requested int              `json:"requested"`
	Applied   int              `json:"applied"`
	Skipped   int              `json:"skipped"`
	Conflicts int              `json:"conflicts"`
	Results   []bulkTestResult `json:"results"`
}

func decodeBulkTestResponse(t *testing.T, responseBody string) bulkTestResponse {
	t.Helper()
	var response bulkTestResponse
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		t.Fatalf("decode bulk response: %v; body=%s", err, responseBody)
	}
	return response
}

func createBulkFixture(t *testing.T, key string, count int) (*Server, *store.Store, store.Project, []store.Task) {
	t.Helper()
	server, data := testServer(t, "disabled")
	ctx := context.Background()
	actor, err := data.EnsureDisabledActor(ctx)
	if err != nil {
		t.Fatalf("ensure disabled actor: %v", err)
	}
	project, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr(key), Name: stringPtr("Bulk " + key)}, actor.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	tasks := make([]store.Task, 0, count)
	for index := 0; index < count; index++ {
		task, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("Bulk task")}, actor.ID)
		if err != nil {
			t.Fatalf("create task %d: %v", index, err)
		}
		tasks = append(tasks, task)
	}
	return server, data, project, tasks
}

func bulkHeaders(key string) map[string]string {
	return map[string]string{"Content-Type": "application/json", "Idempotency-Key": key}
}

func TestBulkTaskPartialReportsMixedOutcomesAndReplaysIdempotently(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "BULK", 2)
	payload := map[string]any{
		"mode": "partial",
		"mutations": []any{
			map[string]any{"task": tasks[0].Key, "version": tasks[0].Version, "operation": "priority", "priority": "urgent"},
			map[string]any{"task": tasks[1].Key, "version": tasks[1].Version + 1, "operation": "priority", "priority": "high"},
			map[string]any{"task": "BULK-999", "version": 1, "operation": "priority", "priority": "low"},
		},
	}
	first := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", payload, bulkHeaders("bulk-partial"))
	if first.Code != http.StatusOK {
		t.Fatalf("partial status = %d, body=%s", first.Code, first.Body.String())
	}
	response := decodeBulkTestResponse(t, first.Body.String())
	if response.Mode != "partial" || response.Status != "partial" || response.Requested != 3 || response.Applied != 1 || response.Conflicts != 1 || response.Skipped != 1 {
		t.Fatalf("partial summary = %+v", response)
	}
	statuses := map[string]string{}
	for _, result := range response.Results {
		statuses[result.Reference] = result.Status
	}
	if statuses[tasks[0].Key] != "applied" || statuses[tasks[1].Key] != "conflict" || statuses["BULK-999"] != "skipped" {
		t.Fatalf("partial item statuses = %+v", statuses)
	}
	updated, err := data.GetTask(context.Background(), tasks[0].ID)
	if err != nil {
		t.Fatalf("read applied task: %v", err)
	}
	if updated.Priority != "urgent" || updated.Version != tasks[0].Version+1 {
		t.Fatalf("applied task = %+v", updated)
	}
	unchanged, err := data.GetTask(context.Background(), tasks[1].ID)
	if err != nil {
		t.Fatalf("read conflict task: %v", err)
	}
	if unchanged.Priority != tasks[1].Priority || unchanged.Version != tasks[1].Version {
		t.Fatalf("conflict task changed = %+v", unchanged)
	}

	events, _, err := data.ListEvents(context.Background(), store.EventFilter{ProjectID: project.ID, Limit: 200})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	updatedEvents := 0
	for _, event := range events {
		if event.TaskID != nil && *event.TaskID == tasks[0].ID && event.Type == "task.updated" {
			updatedEvents++
			var payload map[string]any
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode task.updated payload: %v", err)
			}
			if payload["bulk"] != true {
				t.Fatalf("bulk event payload = %+v", payload)
			}
		}
	}
	if updatedEvents != 1 {
		t.Fatalf("task.updated event count = %d, want one", updatedEvents)
	}

	renamedKey := "BULKRENAMED"
	if _, err := data.UpdateProject(context.Background(), project.ID, store.ProjectInput{Key: &renamedKey}, "actor-disabled-mode"); err != nil {
		t.Fatalf("rename project after bulk mutation: %v", err)
	}
	replay := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", payload, bulkHeaders("bulk-partial"))
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s, want original %d %s", replay.Code, replay.Body.String(), first.Code, first.Body.String())
	}
	updatedAgain, err := data.GetTask(context.Background(), tasks[0].ID)
	if err != nil {
		t.Fatalf("read task after replay: %v", err)
	}
	if updatedAgain.Version != updated.Version {
		t.Fatalf("idempotent replay advanced version from %d to %d", updated.Version, updatedAgain.Version)
	}
}

func TestBulkTaskAtomicRollsBackEarlierItems(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "ATOMIC", 2)
	payload := map[string]any{
		"atomic": true,
		"mutations": []any{
			map[string]any{"task": tasks[0].Key, "version": tasks[0].Version, "operation": "priority", "priority": "urgent"},
			map[string]any{"task": tasks[1].Key, "version": tasks[1].Version + 1, "operation": "priority", "priority": "high"},
		},
	}
	responseRecorder := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", payload, bulkHeaders("bulk-atomic"))
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("atomic status = %d, body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	response := decodeBulkTestResponse(t, responseRecorder.Body.String())
	if response.Mode != "atomic" || response.Status != "failed" || response.Applied != 0 || response.Requested != 2 {
		t.Fatalf("atomic summary = %+v", response)
	}
	if response.Results[0].Status != "skipped" || response.Results[1].Status != "conflict" {
		t.Fatalf("atomic item results = %+v", response.Results)
	}
	for _, task := range tasks {
		current, err := data.GetTask(context.Background(), task.ID)
		if err != nil {
			t.Fatalf("read atomic task %s: %v", task.ID, err)
		}
		if current.Version != task.Version || current.Priority != task.Priority {
			t.Fatalf("atomic rollback changed task = %+v, original=%+v", current, task)
		}
	}
	events, _, err := data.ListEvents(context.Background(), store.EventFilter{ProjectID: project.ID, Limit: 200})
	if err != nil {
		t.Fatalf("list atomic events: %v", err)
	}
	for _, event := range events {
		if event.Type == "task.updated" && event.TaskID != nil && (*event.TaskID == tasks[0].ID || *event.TaskID == tasks[1].ID) {
			t.Fatalf("atomic rollback left task.updated event: %+v", event)
		}
	}
}

func TestBulkTaskEnforcesProjectScopeAndMaximum(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "LIMIT", 1)
	otherProject, err := data.CreateProject(context.Background(), store.ProjectInput{Key: stringPtr("OTHERBULK"), Name: stringPtr("Other bulk")}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	otherTask, err := data.CreateTask(context.Background(), otherProject.ID, store.TaskInput{Title: stringPtr("Outside task")}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create outside task: %v", err)
	}
	outside := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": otherTask.Key, "version": 1, "operation": "priority", "priority": "urgent"}},
	}, bulkHeaders("bulk-outside"))
	if outside.Code != http.StatusOK {
		t.Fatalf("outside item status = %d, body=%s", outside.Code, outside.Body.String())
	}
	outsideResponse := decodeBulkTestResponse(t, outside.Body.String())
	if outsideResponse.Results[0].Status != "skipped" || outsideResponse.Results[0].Error == nil || outsideResponse.Results[0].Error.Code != "forbidden" {
		t.Fatalf("outside item result = %+v", outsideResponse.Results[0])
	}

	items := make([]any, 0, store.BulkTaskMutationLimit+1)
	for index := 0; index < store.BulkTaskMutationLimit+1; index++ {
		items = append(items, map[string]any{"task": tasks[0].Key, "version": tasks[0].Version, "operation": "priority", "priority": "low"})
	}
	tooMany := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{"mutations": items}, bulkHeaders("bulk-too-many"))
	if tooMany.Code != http.StatusBadRequest || !strings.Contains(tooMany.Body.String(), "at most 100") {
		t.Fatalf("too many status = %d, body=%s", tooMany.Code, tooMany.Body.String())
	}
}

func TestBulkTaskAuthorizationHonorsWriteReadClaimAndProjectScopes(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "AUTHBULK", 1)
	ctx := context.Background()
	writer, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Bulk writer", ProjectIDs: []string{project.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}
	_, writerToken, err := data.CreateTokenBy(ctx, writer.ID, "actor-disabled-mode", "bulk writer", []string{"tasks:write"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatalf("create writer token: %v", err)
	}

	writeHeaders := map[string]string{
		"Authorization":   "Bearer " + writerToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-write-only",
	}
	writeResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": tasks[0].Key, "version": tasks[0].Version, "operation": "priority", "priority": "urgent"}},
	}, writeHeaders)
	if writeResponse.Code != http.StatusOK {
		t.Fatalf("write-only bulk status = %d, body=%s", writeResponse.Code, writeResponse.Body.String())
	}
	writeResult := decodeBulkTestResponse(t, writeResponse.Body.String())
	if writeResult.Applied != 1 || writeResult.Results[0].Status != "applied" || writeResult.Results[0].Task == nil {
		t.Fatalf("write-only bulk result = %+v", writeResult)
	}
	if writeResult.Results[0].Task.ID != tasks[0].ID || writeResult.Results[0].Task.Version != tasks[0].Version+1 || writeResult.Results[0].Task.Title != "" {
		t.Fatalf("write-only bulk task was not reduced = %+v", writeResult.Results[0].Task)
	}

	claimOnly, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Bulk claimer", ProjectIDs: []string{project.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatalf("create claim-only agent: %v", err)
	}
	_, claimToken, err := data.CreateTokenBy(ctx, claimOnly.ID, "actor-disabled-mode", "bulk claim-only", []string{"tasks:claim"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatalf("create claim-only token: %v", err)
	}
	claimResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": tasks[0].Key, "version": tasks[0].Version + 1, "operation": "priority", "priority": "low"}},
	}, map[string]string{
		"Authorization":   "Bearer " + claimToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-claim-only",
	})
	if claimResponse.Code != http.StatusForbidden || errorCode(t, claimResponse) != "insufficient_scope" {
		t.Fatalf("claim-only bulk status = %d, body=%s", claimResponse.Code, claimResponse.Body.String())
	}

	lifecycleResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": tasks[0].Key, "version": tasks[0].Version + 1, "operation": "complete"}},
	}, map[string]string{
		"Authorization":   "Bearer " + writerToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-lifecycle-without-claim-scope",
	})
	if lifecycleResponse.Code != http.StatusForbidden || errorCode(t, lifecycleResponse) != "insufficient_scope" {
		t.Fatalf("lifecycle without claim scope = %d, body=%s", lifecycleResponse.Code, lifecycleResponse.Body.String())
	}

	lifecycleAgent, err := data.CreateAgent(ctx, store.Actor{Kind: "agent", Name: "Bulk lifecycle", ProjectIDs: []string{project.ID}}, "actor-disabled-mode", "")
	if err != nil {
		t.Fatalf("create lifecycle agent: %v", err)
	}
	_, lifecycleToken, err := data.CreateTokenBy(ctx, lifecycleAgent.ID, "actor-disabled-mode", "bulk lifecycle", []string{"tasks:write", "tasks:claim"}, []string{project.ID}, nil)
	if err != nil {
		t.Fatalf("create lifecycle token: %v", err)
	}
	unclaimed, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("unclaimed lifecycle")}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create unclaimed lifecycle task: %v", err)
	}
	unclaimedResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": unclaimed.Key, "version": unclaimed.Version, "operation": "complete"}},
	}, map[string]string{
		"Authorization":   "Bearer " + lifecycleToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-unclaimed-lifecycle",
	})
	if unclaimedResponse.Code != http.StatusOK {
		t.Fatalf("unclaimed lifecycle bulk status = %d, body=%s", unclaimedResponse.Code, unclaimedResponse.Body.String())
	}
	unclaimedResult := decodeBulkTestResponse(t, unclaimedResponse.Body.String())
	if unclaimedResult.Results[0].Status != "skipped" || unclaimedResult.Results[0].Error == nil || unclaimedResult.Results[0].Error.Code != "forbidden" {
		t.Fatalf("unclaimed lifecycle result = %+v", unclaimedResult.Results[0])
	}

	claimed, err := data.CreateTask(ctx, project.ID, store.TaskInput{Title: stringPtr("claimed lifecycle")}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create claimed lifecycle task: %v", err)
	}
	claimed, err = data.ClaimTask(ctx, claimed.ID, lifecycleAgent.ID, 0, claimed.Version)
	if err != nil {
		t.Fatalf("claim lifecycle task: %v", err)
	}
	claimedResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": claimed.Key, "version": claimed.Version, "operation": "complete"}},
	}, map[string]string{
		"Authorization":   "Bearer " + lifecycleToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-owned-lifecycle",
	})
	if claimedResponse.Code != http.StatusOK {
		t.Fatalf("owned lifecycle bulk status = %d, body=%s", claimedResponse.Code, claimedResponse.Body.String())
	}
	claimedResult := decodeBulkTestResponse(t, claimedResponse.Body.String())
	if claimedResult.Applied != 1 || claimedResult.Results[0].Status != "applied" {
		t.Fatalf("owned lifecycle result = %+v", claimedResult.Results[0])
	}

	otherProject, err := data.CreateProject(ctx, store.ProjectInput{Key: stringPtr("AUTHOTHER"), Name: stringPtr("Other bulk project")}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	outsideResponse := request(t, server, http.MethodPost, "/api/v1/projects/"+otherProject.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": tasks[0].Key, "version": tasks[0].Version + 1, "operation": "priority", "priority": "low"}},
	}, map[string]string{
		"Authorization":   "Bearer " + writerToken,
		"Content-Type":    "application/json",
		"Idempotency-Key": "bulk-outside-scope",
	})
	if outsideResponse.Code != http.StatusForbidden || errorCode(t, outsideResponse) != "forbidden" {
		t.Fatalf("outside project bulk status = %d, body=%s", outsideResponse.Code, outsideResponse.Body.String())
	}

	current, err := data.GetTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("read task after authorization checks: %v", err)
	}
	if current.Priority != "urgent" || current.Version != tasks[0].Version+1 || current.CompletedAt != nil {
		t.Fatalf("authorization checks mutated task unexpectedly = %+v", current)
	}
}

func TestBulkTaskSupportsAllGuardedOperations(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "OPERATIONS", 7)
	ctx := context.Background()
	source, err := data.GetColumn(ctx, tasks[0].ColumnID)
	if err != nil {
		t.Fatalf("read move source: %v", err)
	}
	backlog, err := data.StateColumn(ctx, project.ID, "backlog")
	if err != nil {
		t.Fatalf("read backlog column: %v", err)
	}
	ready, err := data.StateColumn(ctx, project.ID, "ready")
	if err != nil {
		t.Fatalf("read ready column: %v", err)
	}
	destination := ready
	if source.ID == destination.ID {
		destination = backlog
	}
	assignee, err := data.CreateActor(ctx, store.Actor{Kind: "agent", Name: "Bulk assignee"}, "")
	if err != nil {
		t.Fatalf("create assignee: %v", err)
	}
	label, err := data.CreateLabel(ctx, project.ID, store.LabelInput{Name: "bulk-label", Color: "#6d5efc"}, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	payload := map[string]any{
		"mutations": []any{
			map[string]any{"task": tasks[0].Key, "version": tasks[0].Version, "operation": "move", "destination_column_id": destination.ID, "expected_source_column_id": source.ID, "source": "bulk-test"},
			map[string]any{"task": tasks[1].Key, "version": tasks[1].Version, "operation": "assign", "assignee": assignee.ID},
			map[string]any{"task": tasks[2].Key, "version": tasks[2].Version, "operation": "priority", "priority": "urgent"},
			map[string]any{"task": tasks[3].Key, "version": tasks[3].Version, "operation": "labels", "labels": []string{label.ID}},
			map[string]any{"task": tasks[4].Key, "version": tasks[4].Version, "operation": "due_at", "due_at": "2026-09-04T12:00:00Z"},
			map[string]any{"task": tasks[5].Key, "version": tasks[5].Version, "operation": "block", "reason": "Waiting for bulk validation"},
			map[string]any{"task": tasks[6].Key, "version": tasks[6].Version, "operation": "complete", "comment": "Bulk validation complete"},
		},
	}
	response := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", payload, bulkHeaders("bulk-operations"))
	if response.Code != http.StatusOK {
		t.Fatalf("operations status = %d, body=%s", response.Code, response.Body.String())
	}
	result := decodeBulkTestResponse(t, response.Body.String())
	if result.Applied != len(tasks) || result.Conflicts != 0 || result.Skipped != 0 {
		t.Fatalf("operations summary = %+v", result)
	}
	for _, item := range result.Results {
		if item.Status != "applied" {
			t.Fatalf("operation result = %+v", item)
		}
	}

	moved, err := data.GetTask(ctx, tasks[0].ID)
	if err != nil {
		t.Fatalf("read moved task: %v", err)
	}
	if moved.ColumnID != destination.ID {
		t.Fatalf("moved column = %q, want %q", moved.ColumnID, destination.ID)
	}
	assigned, err := data.GetTask(ctx, tasks[1].ID)
	if err != nil {
		t.Fatalf("read assigned task: %v", err)
	}
	if assigned.Assignee == nil || *assigned.Assignee != assignee.ID {
		t.Fatalf("assigned actor = %v, want %q", assigned.Assignee, assignee.ID)
	}
	prioritized, err := data.GetTask(ctx, tasks[2].ID)
	if err != nil {
		t.Fatalf("read priority task: %v", err)
	}
	if prioritized.Priority != "urgent" {
		t.Fatalf("priority = %q", prioritized.Priority)
	}
	labeled, err := data.GetTask(ctx, tasks[3].ID)
	if err != nil {
		t.Fatalf("read labels task: %v", err)
	}
	if len(labeled.Labels) != 1 || labeled.Labels[0].ID != label.ID {
		t.Fatalf("labels = %+v", labeled.Labels)
	}
	due, err := data.GetTask(ctx, tasks[4].ID)
	if err != nil {
		t.Fatalf("read due task: %v", err)
	}
	if due.DueAt == nil || *due.DueAt != "2026-09-04T12:00:00Z" {
		t.Fatalf("due_at = %v", due.DueAt)
	}
	blocked, err := data.GetTask(ctx, tasks[5].ID)
	if err != nil {
		t.Fatalf("read blocked task: %v", err)
	}
	blockedColumn, err := data.GetColumn(ctx, blocked.ColumnID)
	if err != nil {
		t.Fatalf("read blocked column: %v", err)
	}
	if blockedColumn.SemanticState != "blocked" {
		t.Fatalf("blocked semantic state = %q", blockedColumn.SemanticState)
	}
	completed, err := data.GetTask(ctx, tasks[6].ID)
	if err != nil {
		t.Fatalf("read completed task: %v", err)
	}
	completedColumn, err := data.GetColumn(ctx, completed.ColumnID)
	if err != nil {
		t.Fatalf("read completed column: %v", err)
	}
	if completedColumn.SemanticState != "completed" || completed.CompletedAt == nil {
		t.Fatalf("completed task = %+v, column=%+v", completed, completedColumn)
	}

	events, _, err := data.ListEvents(ctx, store.EventFilter{ProjectID: project.ID, Limit: 200})
	if err != nil {
		t.Fatalf("list operation events: %v", err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		if event.TaskID == nil {
			continue
		}
		for _, task := range tasks {
			if *event.TaskID != task.ID {
				continue
			}
			if event.Type == "task.moved" || event.Type == "task.updated" || event.Type == "task.blocked" || event.Type == "task.completed" {
				seen[event.Type] = true
			}
			if event.Type == "task.moved" && *event.TaskID == tasks[0].ID {
				var payload map[string]any
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatalf("decode bulk move payload: %v", err)
				}
				if payload["placement"] != "last" || payload["rebalanced"] != false {
					t.Fatalf("bulk move ordering payload = %+v", payload)
				}
				if _, ok := payload["ordering_version"].(float64); !ok {
					t.Fatalf("bulk move missing destination ordering version: %+v", payload)
				}
				if _, ok := payload["source_ordering_version"].(float64); !ok {
					t.Fatalf("bulk move missing source ordering version: %+v", payload)
				}
			}
		}
	}
	for _, eventType := range []string{"task.moved", "task.updated", "task.blocked", "task.completed"} {
		if !seen[eventType] {
			t.Fatalf("missing bulk activity event %q", eventType)
		}
	}
	comments, err := data.ListComments(ctx, tasks[6].ID, 20, 0)
	if err != nil {
		t.Fatalf("list completion comments: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "Bulk validation complete" {
		t.Fatalf("completion comments = %+v", comments)
	}
}

func TestBulkTaskMapsChecklistPolicyConflictsPerItem(t *testing.T) {
	server, data, project, tasks := createBulkFixture(t, "CHECKBULKHTTP", 1)
	ctx := context.Background()
	policy := "require"
	if _, err := data.UpdateProject(ctx, project.ID, store.ProjectInput{ChecklistCompletionPolicy: &policy}, "actor-disabled-mode"); err != nil {
		t.Fatalf("set checklist policy: %v", err)
	}
	updated, err := data.AddTaskChecklistItem(ctx, tasks[0].ID, store.ChecklistItemInput{Text: stringPtr("verify release")}, tasks[0].Version, "actor-disabled-mode")
	if err != nil {
		t.Fatalf("add checklist item: %v", err)
	}
	response := request(t, server, http.MethodPost, "/api/v1/projects/"+project.Key+"/tasks/bulk", map[string]any{
		"mutations": []any{map[string]any{"task": updated.Key, "version": updated.Version, "operation": "complete"}},
	}, bulkHeaders("bulk-checklist-policy"))
	if response.Code != http.StatusOK {
		t.Fatalf("bulk checklist status = %d, body=%s", response.Code, response.Body.String())
	}
	result := decodeBulkTestResponse(t, response.Body.String())
	if result.Applied != 0 || result.Conflicts != 1 || result.Results[0].Status != "conflict" || result.Results[0].Error == nil || result.Results[0].Error.Code != "checklist_incomplete" {
		t.Fatalf("bulk checklist result = %+v", result)
	}
	after, err := data.GetTask(ctx, updated.ID)
	if err != nil {
		t.Fatalf("reload checklist task: %v", err)
	}
	if after.Version != updated.Version || after.CompletedAt != nil {
		t.Fatalf("bulk checklist conflict changed task = %+v", after)
	}
}
