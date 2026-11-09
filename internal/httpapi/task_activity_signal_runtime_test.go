package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

// TC-379 runtime failure modes (test/neglect/PROJECTOR.md 3, 14, 15): the
// projector starts without delaying readiness, reports bounded metrics on the
// private metrics path, and stops on graceful shutdown.
func TestTaskActivitySignalRuntimeReadinessMetricsAndShutdown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "helm.db")
	database, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	data := store.New(database)
	cfg := config.Config{AuthMode: "disabled", PublicOrigin: "http://helm.test", DB: path}
	server := New(data, auth.NewManager(data, cfg), cfg)
	project := request(t, server, http.MethodPost, "/api/v1/projects", map[string]any{"key": "SIG", "name": "Signal"}, map[string]string{"Content-Type": "application/json"})
	if project.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", project.Code, project.Body.String())
	}
	var taskIDs []string
	for index := 0; index < 3; index++ {
		created := request(t, server, http.MethodPost, "/api/v1/projects/SIG/tasks", map[string]any{"title": "Signal task"}, map[string]string{"Content-Type": "application/json"})
		if created.Code != http.StatusCreated {
			t.Fatalf("create task: %d %s", created.Code, created.Body.String())
		}
		taskIDs = append(taskIDs, regexp.MustCompile(`"id":"([0-9a-f]{32})"`).FindStringSubmatch(created.Body.String())[1])
	}

	metrics := func() string {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "127.0.0.1:4321"
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("metrics status %d", response.Code)
		}
		return response.Body.String()
	}
	if strings.Contains(metrics(), "helm_task_activity_signal_") {
		t.Fatal("projector metrics rendered before the projector started")
	}

	started := time.Now()
	if err := server.StartTaskActivitySignalProjector(ctx); err != nil {
		t.Fatal(err)
	}
	if err := server.StartTaskActivitySignalProjector(ctx); err == nil {
		t.Fatal("a second projector started in one process")
	}
	ready := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if ready.Code != http.StatusOK || time.Since(started) > 5*time.Second {
		t.Fatalf("readiness %d after %s", ready.Code, time.Since(started))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		progress, err := data.TaskActivitySignalProgress(ctx)
		if err == nil && progress.SeededVersion == store.TaskActivityClassificationVersion && progress.Lag() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("seed did not complete: %+v %v", progress, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	body := metrics()
	for _, name := range []string{
		"helm_task_activity_signal_processed_cursor ",
		"helm_task_activity_signal_cursor_lag 0",
		"helm_task_activity_signal_seed_remaining 0",
		"helm_task_activity_signal_seed_complete 1",
		"helm_task_activity_signal_seed_batch_size ",
		`helm_task_activity_signal_rebuilds_total{pass="seed"} 3`,
		`helm_task_activity_signal_rebuild_duration_seconds_count{pass="seed"} 3`,
		`helm_task_activity_signal_rebuild_duration_seconds_sum{pass="tail"} `,
		"helm_task_activity_signal_write_duration_seconds_count ",
		"helm_task_activity_signal_write_duration_max_seconds ",
		"helm_task_activity_signal_classification_errors_total 0",
		"helm_task_activity_signal_pass_errors_total 0",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("metrics missing %q", name)
		}
	}
	labels := regexp.MustCompile(`\{([^}]*)\}`)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "helm_task_activity_signal_") {
			continue
		}
		for _, id := range taskIDs {
			if strings.Contains(line, id) {
				t.Fatalf("metric line leaks a task id: %s", line)
			}
		}
		for _, match := range labels.FindAllStringSubmatch(line, -1) {
			if match[1] != `pass="seed"` && match[1] != `pass="tail"` {
				t.Fatalf("unbounded label in %s", line)
			}
		}
	}

	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := server.StopTaskActivitySignalProjector(stopCtx); err != nil {
		t.Fatalf("stop projector: %v", err)
	}
	if err := server.StopTaskActivitySignalProjector(stopCtx); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}
