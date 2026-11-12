package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/KanterLabs/helm/internal/store"
)

// The neglect-signal projector (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D4) runs in
// the background from server startup to graceful shutdown. Startup and
// /readyz never wait for it; its metrics are appended to the private /metrics
// response and carry no task ids, text, or unbounded labels.

type taskActivitySignalRuntime struct {
	mu        sync.Mutex
	projector *store.TaskActivitySignalProjector
	cancel    context.CancelFunc
	done      chan struct{}
}

func (s *Server) taskActivitySignalRuntime() *taskActivitySignalRuntime {
	return &s.activitySignal
}

// StartTaskActivitySignalProjector attaches the store's projector and runs it
// until ctx is canceled or StopTaskActivitySignalProjector is called. It
// returns at once; the seed runs in the background.
func (s *Server) StartTaskActivitySignalProjector(ctx context.Context) error {
	runtime := s.taskActivitySignalRuntime()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.projector != nil {
		return errors.New("task activity signal projector already running")
	}
	projector, err := s.Store.AttachTaskActivitySignalProjector(store.TaskActivitySignalProjectorOptions{
		OnPassError: func(err error) {
			s.logJSON(map[string]any{"level": "error", "msg": "task activity signal pass failed", "error_class": classifyError(err)})
		},
	})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = projector.Run(runCtx)
	}()
	runtime.projector, runtime.cancel, runtime.done = projector, cancel, done
	return nil
}

// StopTaskActivitySignalProjector cancels the projector and waits, at most
// until ctx ends, for its current pass to unwind. An interrupted pass commits
// nothing; the next start resumes from the stored cursor.
func (s *Server) StopTaskActivitySignalProjector(ctx context.Context) error {
	runtime := s.taskActivitySignalRuntime()
	runtime.mu.Lock()
	projector, cancel, done := runtime.projector, runtime.cancel, runtime.done
	runtime.projector, runtime.cancel, runtime.done = nil, nil, nil
	runtime.mu.Unlock()
	if projector == nil {
		return nil
	}
	cancel()
	defer projector.Detach()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// metricsWithTaskActivitySignal serves the private /metrics endpoint and, on
// a successful GET while the projector runs, appends its metrics.
func (s *Server) metricsWithTaskActivitySignal(w http.ResponseWriter, r *http.Request) {
	recorder := &statusWriter{ResponseWriter: w}
	s.metricsEndpoint(recorder, r)
	if recorder.status != http.StatusOK || r.Method != http.MethodGet {
		return
	}
	runtime := s.taskActivitySignalRuntime()
	runtime.mu.Lock()
	projector := runtime.projector
	runtime.mu.Unlock()
	if projector == nil {
		return
	}
	_, _ = io.WriteString(w, renderTaskActivitySignalMetrics(r.Context(), s.Store, projector.Stats()))
}

func renderTaskActivitySignalMetrics(ctx context.Context, data *store.Store, stats store.TaskActivitySignalProjectorStats) string {
	var output strings.Builder
	gauge := func(name, help string, value any) {
		fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s gauge\n%s %v\n", name, help, name, name, value)
	}
	if progress, err := data.TaskActivitySignalProgress(ctx); err == nil {
		gauge("helm_task_activity_signal_processed_cursor", "Event cursor the neglect-signal projector has processed.", progress.Processed)
		gauge("helm_task_activity_signal_cursor_lag", "Events newer than the processed cursor.", progress.Lag())
	}
	gauge("helm_task_activity_signal_seed_remaining", "Tasks this process's seed pass has still to rebuild (-1 before it starts).", stats.SeedRemaining)
	gauge("helm_task_activity_signal_seed_complete", "1 once this process's seed pass has finished.", boolInt(stats.SeedComplete))
	gauge("helm_task_activity_signal_seed_batch_size", "Tasks rebuilt per seed transaction.", stats.SeedBatch)
	output.WriteString("# HELP helm_task_activity_signal_rebuilds_total Tasks rebuilt from full history, by pass.\n# TYPE helm_task_activity_signal_rebuilds_total counter\n")
	fmt.Fprintf(&output, "helm_task_activity_signal_rebuilds_total{pass=\"seed\"} %d\nhelm_task_activity_signal_rebuilds_total{pass=\"tail\"} %d\n", stats.SeedRebuilds, stats.TailRebuilds)
	output.WriteString("# HELP helm_task_activity_signal_rebuild_duration_seconds Time classifying task histories, by pass.\n# TYPE helm_task_activity_signal_rebuild_duration_seconds summary\n")
	for _, pass := range []struct {
		name    string
		seconds float64
		count   uint64
	}{{"seed", stats.SeedRebuildSeconds, stats.SeedRebuilds}, {"tail", stats.TailRebuildSeconds, stats.TailRebuilds}} {
		fmt.Fprintf(&output, "helm_task_activity_signal_rebuild_duration_seconds_sum{pass=%q} %s\n", pass.name, formatFloat(pass.seconds))
		fmt.Fprintf(&output, "helm_task_activity_signal_rebuild_duration_seconds_count{pass=%q} %d\n", pass.name, pass.count)
	}
	output.WriteString("# HELP helm_task_activity_signal_write_duration_seconds Time the projector held the SQLite writer lock per transaction.\n# TYPE helm_task_activity_signal_write_duration_seconds summary\n")
	fmt.Fprintf(&output, "helm_task_activity_signal_write_duration_seconds_sum %s\nhelm_task_activity_signal_write_duration_seconds_count %d\n", formatFloat(stats.WriteSeconds), stats.Writes)
	gauge("helm_task_activity_signal_write_duration_max_seconds", "Longest single projector write transaction.", formatFloat(stats.WriteMaxSeconds))
	output.WriteString("# HELP helm_task_activity_signal_classification_errors_total Rows stored as unknown/classification_error.\n# TYPE helm_task_activity_signal_classification_errors_total counter\n")
	fmt.Fprintf(&output, "helm_task_activity_signal_classification_errors_total %d\n", stats.ClassificationErrors)
	output.WriteString("# HELP helm_task_activity_signal_pass_errors_total Projector passes that failed and were retried.\n# TYPE helm_task_activity_signal_pass_errors_total counter\n")
	fmt.Fprintf(&output, "helm_task_activity_signal_pass_errors_total %d\n", stats.PassErrors)
	return output.String()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
