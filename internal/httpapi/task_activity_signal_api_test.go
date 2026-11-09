package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/KanterLabs/helm/internal/store"
)

// TC-380 HTTP failure modes (test/neglect/API.md 2, 9, 11): input
// validation, the neglect collection shape, and the unchanged existing
// contract. Written before the handler code.

func signalAPIFixture(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	server, data := testServer(t, "disabled")
	headers := map[string]string{"Content-Type": "application/json"}
	if r := request(t, server, http.MethodPost, "/api/v1/projects", map[string]any{"key": "NEG", "name": "Neglect"}, headers); r.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", r.Code, r.Body.String())
	}
	var taskID string
	for index := 0; index < 3; index++ {
		r := request(t, server, http.MethodPost, "/api/v1/projects/NEG/tasks", map[string]any{"title": "Signal task"}, headers)
		if r.Code != http.StatusCreated {
			t.Fatalf("create task: %d %s", r.Code, r.Body.String())
		}
		if strings.Contains(r.Body.String(), `"activity"`) {
			t.Fatalf("create response carries activity: %s", r.Body.String())
		}
		var created struct{ ID string }
		_ = json.Unmarshal(r.Body.Bytes(), &created)
		taskID = created.ID
	}
	projector, err := data.AttachTaskActivitySignalProjector(store.TaskActivitySignalProjectorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(projector.Detach)
	if err := projector.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	return server, data, taskID
}

func decodeSignalBody(t *testing.T, r interface{ Bytes() []byte }) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(r.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", r.Bytes(), err)
	}
	return body
}

func TestTaskActivitySignalListingInputsAreValidated(t *testing.T) {
	server, _, _ := signalAPIFixture(t)
	for _, query := range []string{
		"inactive_days=0", "inactive_days=-3", "inactive_days=abc", "inactive_days=3651", "inactive_days=", "inactive_days=1&inactive_days=2",
		"activity_status=active", "activity_status=pending", "activity_status=", "activity_status=undetermined",
		"inactive_days=14&cursor=1", "sort=not_after&cursor=MQ",
	} {
		r := request(t, server, http.MethodGet, "/api/v1/projects/NEG/tasks?"+query, nil, nil)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "invalid_request") {
			t.Errorf("%s -> %d %s", query, r.Code, r.Body.String())
		}
	}
	for _, query := range []string{"inactive_days=1", "inactive_days=3650", "activity_status=known", "inactive_days=14&activity_status=undetermined",
		"sort=not_after", "sort=not_after&order=desc", "sort=not_after_desc", "inactive_days=14&state=backlog&release=unassigned"} {
		if r := request(t, server, http.MethodGet, "/api/v1/projects/NEG/tasks?"+query, nil, nil); r.Code != http.StatusOK {
			t.Errorf("%s -> %d %s", query, r.Code, r.Body.String())
		}
	}
}

func TestTaskActivitySignalCollectionShapeAndPageChain(t *testing.T) {
	server, _, _ := signalAPIFixture(t)
	plain := decodeSignalBody(t, request(t, server, http.MethodGet, "/api/v1/projects/NEG/tasks", nil, nil).Body)
	if _, ok := plain["evaluated_at"]; ok {
		t.Fatalf("plain listing has evaluated_at: %v", plain)
	}
	if _, ok := plain["activity_coverage"]; ok {
		t.Fatalf("plain listing has activity_coverage: %v", plain)
	}
	for _, item := range plain["data"].([]any) {
		activity, ok := item.(map[string]any)["activity"].(map[string]any)
		if !ok || activity["status"] != "known" || activity["classification_version"] != float64(2) {
			t.Fatalf("plain listing task activity = %v", item)
		}
		for _, field := range []string{"status", "last_meaningful_at", "source", "not_after", "reason", "classification_version"} {
			if _, ok := activity[field]; !ok {
				t.Fatalf("activity lacks %s: %v", field, activity)
			}
		}
	}
	var evaluated []any
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		target := "/api/v1/projects/NEG/tasks?sort=not_after&limit=1"
		if cursor != "" {
			target += "&cursor=" + cursor
		}
		r := request(t, server, http.MethodGet, target, nil, nil)
		if r.Code != http.StatusOK {
			t.Fatalf("page %d: %d %s", pages, r.Code, r.Body.String())
		}
		body := decodeSignalBody(t, r.Body)
		coverage, ok := body["activity_coverage"].(map[string]any)
		if !ok || coverage["complete"] != true || coverage["lag"] != float64(0) || coverage["pending"] != float64(0) {
			t.Fatalf("coverage = %v", body["activity_coverage"])
		}
		evaluated = append(evaluated, body["evaluated_at"])
		cursor, _ = body["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	if len(evaluated) != 3 || evaluated[0] != evaluated[1] || evaluated[1] != evaluated[2] || evaluated[0] == nil {
		t.Fatalf("evaluated_at by page = %v", evaluated)
	}
}

func TestTaskActivitySignalTaskReadKeepsETagAndMutationShape(t *testing.T) {
	server, _, taskID := signalAPIFixture(t)
	read := request(t, server, http.MethodGet, "/api/v1/tasks/"+taskID, nil, nil)
	body := decodeSignalBody(t, read.Body)
	if read.Code != http.StatusOK || read.Header().Get("ETag") != `"v1"` || body["activity"] == nil {
		t.Fatalf("task read %d etag=%q activity=%v", read.Code, read.Header().Get("ETag"), body["activity"])
	}
	patched := request(t, server, http.MethodPatch, "/api/v1/tasks/"+taskID, map[string]any{"priority": "high"},
		map[string]string{"Content-Type": "application/json", "If-Match": `"v1"`})
	if patched.Code != http.StatusOK || patched.Header().Get("ETag") != `"v2"` || strings.Contains(patched.Body.String(), `"activity"`) {
		t.Fatalf("patch %d etag=%q body=%s", patched.Code, patched.Header().Get("ETag"), patched.Body.String())
	}
	reread := request(t, server, http.MethodGet, "/api/v1/tasks/"+taskID, nil, nil)
	if reread.Header().Get("ETag") != `"v2"` {
		t.Fatalf("activity changed the task ETag: %q", reread.Header().Get("ETag"))
	}
	// The archive has its own top-level "activity" (events and agent work);
	// derived rows must not appear on its tasks or anywhere else.
	exported := request(t, server, http.MethodGet, "/api/v1/projects/NEG/export", nil, nil)
	if exported.Code != http.StatusOK || strings.Contains(exported.Body.String(), "classification_version") || strings.Contains(exported.Body.String(), "task_activity_signal") {
		t.Fatalf("export %d carries derived activity", exported.Code)
	}
	var archive struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(exported.Body.Bytes(), &archive); err != nil || len(archive.Tasks) != 3 {
		t.Fatalf("export tasks = %d, %v", len(archive.Tasks), err)
	}
	for _, task := range archive.Tasks {
		if _, ok := task["activity"]; ok {
			t.Fatalf("exported task carries activity: %v", task)
		}
	}
}
