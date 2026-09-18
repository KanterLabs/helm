package httpapi

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

const (
	adminActivityHours     = 24
	adminActivityMaxActors = 500
	adminMetricsMaxDays    = 90
)

// adminActivityTracker keeps process-local request counts for the admin
// metrics view. Unlike metricsRegistry it is keyed by actor so an
// administrator can see which agent is busy, which is why it is never exposed
// on the unauthenticated-loopback /metrics endpoint. Memory is bounded by the
// fixed hourly ring and the actor cap; counters reset on restart.
type adminActivityTracker struct {
	mu        sync.Mutex
	startedAt time.Time
	hours     [adminActivityHours]adminActivityHour
	actors    map[string]*adminActivityActor
	totals    adminActivityCounts
}

type adminActivityCounts struct {
	Total      uint64 `json:"total"`
	Agent      uint64 `json:"agent"`
	Human      uint64 `json:"human"`
	Anonymous  uint64 `json:"anonymous"`
	Errors     uint64 `json:"errors"`
	DurationMS uint64 `json:"-"`
}

type adminActivityHour struct {
	start  time.Time
	counts adminActivityCounts
}

type adminActivityActor struct {
	kind       string
	requests   uint64
	errors     uint64
	durationMS uint64
	lastSeen   time.Time
}

func newAdminActivityTracker() *adminActivityTracker {
	return &adminActivityTracker{startedAt: time.Now().UTC(), actors: make(map[string]*adminActivityActor)}
}

func (t *adminActivityTracker) record(identity auth.Identity, authenticated bool, status int, duration time.Duration, at time.Time) {
	if t == nil {
		return
	}
	kind := "anonymous"
	if authenticated {
		kind = identity.Actor.Kind
	}
	isError := status >= 400
	ms := uint64(0)
	if duration > 0 {
		ms = uint64(duration.Milliseconds())
	}
	hourStart := at.UTC().Truncate(time.Hour)
	slot := &t.hours[hourStart.Unix()/3600%adminActivityHours]

	t.mu.Lock()
	defer t.mu.Unlock()
	if !slot.start.Equal(hourStart) {
		*slot = adminActivityHour{start: hourStart}
	}
	for _, counts := range []*adminActivityCounts{&t.totals, &slot.counts} {
		counts.Total++
		counts.DurationMS += ms
		switch kind {
		case "agent":
			counts.Agent++
		case "human":
			counts.Human++
		default:
			counts.Anonymous++
		}
		if isError {
			counts.Errors++
		}
	}
	if !authenticated || identity.Actor.ID == "" {
		return
	}
	actor := t.actors[identity.Actor.ID]
	if actor == nil {
		if len(t.actors) >= adminActivityMaxActors {
			return
		}
		actor = &adminActivityActor{kind: kind}
		t.actors[identity.Actor.ID] = actor
	}
	actor.requests++
	actor.durationMS += ms
	actor.lastSeen = at.UTC()
	if isError {
		actor.errors++
	}
}

type adminRequestHour struct {
	Hour string `json:"hour"`
	adminActivityCounts
}

type adminRequestActor struct {
	ActorID    string `json:"actor_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Requests   uint64 `json:"requests"`
	Errors     uint64 `json:"errors"`
	AvgMS      uint64 `json:"avg_ms"`
	LastSeenAt string `json:"last_seen_at"`
}

type adminRequestMetrics struct {
	Since string `json:"since"`
	adminActivityCounts
	AvgMS     uint64              `json:"avg_ms"`
	Hourly    []adminRequestHour  `json:"hourly"`
	TopActors []adminRequestActor `json:"top_actors"`
}

func (t *adminActivityTracker) snapshot(now time.Time) adminRequestMetrics {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := adminRequestMetrics{Since: t.startedAt.Format(time.RFC3339), adminActivityCounts: t.totals, Hourly: []adminRequestHour{}, TopActors: []adminRequestActor{}}
	if t.totals.Total > 0 {
		result.AvgMS = t.totals.DurationMS / t.totals.Total
	}
	current := now.UTC().Truncate(time.Hour)
	for offset := adminActivityHours - 1; offset >= 0; offset-- {
		hourStart := current.Add(-time.Duration(offset) * time.Hour)
		entry := adminRequestHour{Hour: hourStart.Format(time.RFC3339)}
		if slot := t.hours[hourStart.Unix()/3600%adminActivityHours]; slot.start.Equal(hourStart) {
			entry.adminActivityCounts = slot.counts
		}
		result.Hourly = append(result.Hourly, entry)
	}
	for id, actor := range t.actors {
		entry := adminRequestActor{ActorID: id, Kind: actor.kind, Requests: actor.requests, Errors: actor.errors, LastSeenAt: actor.lastSeen.Format(time.RFC3339)}
		if actor.requests > 0 {
			entry.AvgMS = actor.durationMS / actor.requests
		}
		result.TopActors = append(result.TopActors, entry)
	}
	sort.Slice(result.TopActors, func(i, j int) bool {
		if result.TopActors[i].Requests != result.TopActors[j].Requests {
			return result.TopActors[i].Requests > result.TopActors[j].Requests
		}
		return result.TopActors[i].ActorID < result.TopActors[j].ActorID
	})
	if len(result.TopActors) > 20 {
		result.TopActors = result.TopActors[:20]
	}
	return result
}

func (s *Server) adminActivityValue() *adminActivityTracker {
	s.adminActivityOnce.Do(func() {
		if s.adminActivity == nil {
			s.adminActivity = newAdminActivityTracker()
		}
	})
	return s.adminActivity
}

type adminMetricsResponse struct {
	GeneratedAt string              `json:"generated_at"`
	WindowDays  int                 `json:"window_days"`
	ReleaseSHA  string              `json:"release_sha"`
	Requests    adminRequestMetrics `json:"requests"`
	store.AdminMetrics
}

// adminMetrics serves GET /api/v1/admin/metrics?days=N for human
// administrators. It combines persisted activity with process-local request
// counters and is deliberately not available to bearer tokens.
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
	requests := s.adminActivityValue().snapshot(now)
	if len(requests.TopActors) > 0 {
		for index := range requests.TopActors {
			if actor, err := s.Store.GetActor(r.Context(), requests.TopActors[index].ActorID); err == nil {
				requests.TopActors[index].Name = actor.Name
			}
		}
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
