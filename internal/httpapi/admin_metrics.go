package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

const (
	adminActivityHours       = 24
	adminActivityMaxPending  = 5000
	adminMetricsMaxDays      = 90
	requestActivityRetention = 90 * 24 * time.Hour
	requestActivityFlush     = time.Minute
)

// adminActivityTracker batches per-actor, per-hour request counts in memory
// and adds them to request_activity_hourly about once a minute, when an
// administrator opens the metrics page, and on graceful shutdown. Counts are
// keyed by actor, which is why they are never exposed on the
// unauthenticated-loopback /metrics endpoint. The pending batch is bounded;
// if the database is unavailable for long enough to fill it, new actor/hour
// keys are dropped rather than growing memory.
type adminActivityTracker struct {
	mu      sync.Mutex
	pending map[adminActivityKey]*store.RequestActivityDelta
	flushMu sync.Mutex
}

type adminActivityKey struct {
	hour    int64
	actorID string
}

func newAdminActivityTracker() *adminActivityTracker {
	return &adminActivityTracker{pending: make(map[adminActivityKey]*store.RequestActivityDelta)}
}

func (t *adminActivityTracker) record(identity auth.Identity, authenticated bool, status int, duration time.Duration, at time.Time) {
	if t == nil {
		return
	}
	kind, actorID := "anonymous", ""
	if authenticated {
		kind, actorID = identity.Actor.Kind, identity.Actor.ID
	}
	ms := uint64(0)
	if duration > 0 {
		ms = uint64(duration.Milliseconds())
	}
	at = at.UTC()
	hour := at.Truncate(time.Hour)
	key := adminActivityKey{hour: hour.Unix(), actorID: actorID}

	t.mu.Lock()
	defer t.mu.Unlock()
	delta := t.pending[key]
	if delta == nil {
		if len(t.pending) >= adminActivityMaxPending {
			return
		}
		delta = &store.RequestActivityDelta{Hour: hour, ActorID: actorID, ActorKind: kind}
		t.pending[key] = delta
	}
	delta.Requests++
	delta.DurationMSSum += ms
	if status >= 400 {
		delta.Errors++
	}
	if at.After(delta.LastSeenAt) {
		delta.LastSeenAt = at
	}
}

// flush writes the pending batch. On failure the batch is merged back so the
// next flush retries it; AddRequestActivity is all-or-nothing, so nothing is
// double counted.
func (t *adminActivityTracker) flush(ctx context.Context, data *store.Store, now time.Time) error {
	if t == nil || data == nil {
		return nil
	}
	t.flushMu.Lock()
	defer t.flushMu.Unlock()
	t.mu.Lock()
	batch := t.pending
	t.pending = make(map[adminActivityKey]*store.RequestActivityDelta)
	t.mu.Unlock()
	deltas := make([]store.RequestActivityDelta, 0, len(batch))
	for _, delta := range batch {
		deltas = append(deltas, *delta)
	}
	err := data.AddRequestActivity(ctx, deltas, now.Add(-requestActivityRetention))
	if err == nil {
		return nil
	}
	t.mu.Lock()
	for key, delta := range batch {
		if current := t.pending[key]; current != nil {
			current.Requests += delta.Requests
			current.Errors += delta.Errors
			current.DurationMSSum += delta.DurationMSSum
			if delta.LastSeenAt.After(current.LastSeenAt) {
				current.LastSeenAt = delta.LastSeenAt
			}
		} else {
			t.pending[key] = delta
		}
	}
	t.mu.Unlock()
	return err
}

func (s *Server) adminActivityValue() *adminActivityTracker {
	s.adminActivityOnce.Do(func() {
		if s.adminActivity == nil {
			s.adminActivity = newAdminActivityTracker()
		}
	})
	return s.adminActivity
}

// FlushRequestActivity writes any pending request counts. The server calls it
// on graceful shutdown so a deploy or restart does not lose counts.
func (s *Server) FlushRequestActivity(ctx context.Context) error {
	return s.adminActivityValue().flush(ctx, s.Store, time.Now())
}

// RunRequestActivityFlusher writes pending request counts every minute until
// ctx is canceled. Write errors are logged and retried on the next tick.
func (s *Server) RunRequestActivityFlusher(ctx context.Context) {
	ticker := time.NewTicker(requestActivityFlush)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.FlushRequestActivity(ctx); err != nil && ctx.Err() == nil {
				s.logJSON(map[string]any{"level": "error", "msg": "request activity flush failed", "error_class": classifyError(err)})
			}
		}
	}
}

type adminMetricsResponse struct {
	GeneratedAt string                `json:"generated_at"`
	WindowDays  int                   `json:"window_days"`
	ReleaseSHA  string                `json:"release_sha"`
	Requests    store.RequestActivity `json:"requests"`
	store.AdminMetrics
}

// adminMetrics serves GET /api/v1/admin/metrics?days=N for human
// administrators. Activity comes from the event log and request counts from
// request_activity_hourly; it is deliberately not available to bearer tokens.
func (s *Server) adminMetrics(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !requireAdmin(w, identity) {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > adminMetricsMaxDays {
			s.writeError(w, http.StatusBadRequest, "invalid_request", "days must be an integer from 1 to 90", nil)
			return
		}
		days = parsed
	}
	now := time.Now().UTC()
	persisted, err := s.Store.AdminMetricsSince(r.Context(), now.AddDate(0, 0, -(days-1)))
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	// Include requests from the last minute that have not been flushed yet.
	if err := s.FlushRequestActivity(r.Context()); err != nil {
		s.logJSON(map[string]any{"level": "error", "msg": "request activity flush failed", "error_class": classifyError(err)})
	}
	requests, err := s.Store.RequestActivitySince(r.Context(), now.AddDate(0, 0, -(days-1)), now, adminActivityHours)
	if err != nil {
		s.writeInternal(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, adminMetricsResponse{
		GeneratedAt:  now.Format(time.RFC3339),
		WindowDays:   days,
		ReleaseSHA:   s.Cfg.ReleaseSHA,
		Requests:     requests,
		AdminMetrics: persisted,
	})
}
