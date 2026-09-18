package store

import (
	"context"
	"database/sql"
	"time"
)

// RequestActivityDelta is one in-memory batch entry to add to the hourly
// request rollup. ActorID is empty for unauthenticated requests.
type RequestActivityDelta struct {
	Hour          time.Time
	ActorID       string
	ActorKind     string
	Requests      uint64
	Errors        uint64
	DurationMSSum uint64
	LastSeenAt    time.Time
}

// AddRequestActivity adds a batch to request_activity_hourly in one
// transaction and prunes rows older than pruneBefore. Either the whole batch
// is recorded or none of it is, so a caller can safely retry a failed batch.
func (s *Store) AddRequestActivity(ctx context.Context, deltas []RequestActivityDelta, pruneBefore time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statement, err := tx.PrepareContext(ctx, `INSERT INTO request_activity_hourly(hour, actor_id, actor_kind, requests, errors, duration_ms_sum, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hour, actor_id) DO UPDATE SET
			actor_kind=excluded.actor_kind,
			requests=requests+excluded.requests,
			errors=errors+excluded.errors,
			duration_ms_sum=duration_ms_sum+excluded.duration_ms_sum,
			last_seen_at=MAX(last_seen_at, excluded.last_seen_at)`)
	if err != nil {
		return err
	}
	defer statement.Close()
	for _, delta := range deltas {
		if _, err := statement.ExecContext(ctx,
			delta.Hour.UTC().Truncate(time.Hour).Format(time.RFC3339),
			delta.ActorID, delta.ActorKind,
			int64(delta.Requests), int64(delta.Errors), int64(delta.DurationMSSum),
			delta.LastSeenAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	if !pruneBefore.IsZero() {
		if _, err := tx.ExecContext(ctx, `DELETE FROM request_activity_hourly WHERE hour < ?`, pruneBefore.UTC().Truncate(time.Hour).Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RequestCounts are aggregate request totals split by actor kind.
type RequestCounts struct {
	Total      uint64 `json:"total"`
	Agent      uint64 `json:"agent"`
	Human      uint64 `json:"human"`
	Anonymous  uint64 `json:"anonymous"`
	Errors     uint64 `json:"errors"`
	DurationMS uint64 `json:"-"`
}

// RequestHour is one hour of persisted request activity.
type RequestHour struct {
	Hour string `json:"hour"`
	RequestCounts
}

// RequestDay is one UTC calendar day of persisted request activity.
type RequestDay struct {
	Date string `json:"date"`
	RequestCounts
}

// RequestActor summarizes one actor's persisted requests in a window.
type RequestActor struct {
	ActorID    string `json:"actor_id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Requests   uint64 `json:"requests"`
	Errors     uint64 `json:"errors"`
	AvgMS      uint64 `json:"avg_ms"`
	LastSeenAt string `json:"last_seen_at"`
}

// RequestActivity is the persisted request view for the admin metrics page.
type RequestActivity struct {
	Since string `json:"since"`
	RequestCounts
	AvgMS     uint64         `json:"avg_ms"`
	Hourly    []RequestHour  `json:"hourly"`
	Daily     []RequestDay   `json:"daily"`
	TopActors []RequestActor `json:"top_actors"`
}

const requestKindSums = `COALESCE(SUM(requests), 0),
	COALESCE(SUM(CASE WHEN actor_kind='agent' THEN requests ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN actor_kind='human' THEN requests ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN actor_kind NOT IN ('agent','human') THEN requests ELSE 0 END), 0),
	COALESCE(SUM(errors), 0),
	COALESCE(SUM(duration_ms_sum), 0)`

func scanCounts(row interface{ Scan(...any) error }, prefix []any, counts *RequestCounts) error {
	dest := append(prefix, &counts.Total, &counts.Agent, &counts.Human, &counts.Anonymous, &counts.Errors, &counts.DurationMS)
	return row.Scan(dest...)
}

// RequestActivitySince reads persisted request activity for the UTC days
// starting at since, plus the trailing hours of hourly detail ending at now.
func (s *Store) RequestActivitySince(ctx context.Context, since, now time.Time, hours int) (RequestActivity, error) {
	since = since.UTC().Truncate(24 * time.Hour)
	sinceText := since.Format(time.RFC3339)
	result := RequestActivity{Since: sinceText, Hourly: []RequestHour{}, Daily: []RequestDay{}, TopActors: []RequestActor{}}

	if err := scanCounts(s.DB.QueryRowContext(ctx, `SELECT `+requestKindSums+` FROM request_activity_hourly WHERE hour >= ?`, sinceText), nil, &result.RequestCounts); err != nil {
		return result, err
	}
	if result.Total > 0 {
		result.AvgMS = result.DurationMS / result.Total
	}

	days := map[string]*RequestDay{}
	for day := since; !day.After(now.UTC()); day = day.AddDate(0, 0, 1) {
		result.Daily = append(result.Daily, RequestDay{Date: day.Format("2006-01-02")})
	}
	for index := range result.Daily {
		days[result.Daily[index].Date] = &result.Daily[index]
	}
	if err := s.scanRequestGroups(ctx, `SELECT substr(hour, 1, 10) AS day, `+requestKindSums+` FROM request_activity_hourly WHERE hour >= ? GROUP BY day`, sinceText, func(key string, counts RequestCounts) {
		if entry := days[key]; entry != nil {
			entry.RequestCounts = counts
		}
	}); err != nil {
		return result, err
	}

	current := now.UTC().Truncate(time.Hour)
	first := current.Add(-time.Duration(hours-1) * time.Hour)
	hourly := map[string]*RequestHour{}
	for hour := first; !hour.After(current); hour = hour.Add(time.Hour) {
		result.Hourly = append(result.Hourly, RequestHour{Hour: hour.Format(time.RFC3339)})
	}
	for index := range result.Hourly {
		hourly[result.Hourly[index].Hour] = &result.Hourly[index]
	}
	if err := s.scanRequestGroups(ctx, `SELECT hour, `+requestKindSums+` FROM request_activity_hourly WHERE hour >= ? GROUP BY hour`, first.Format(time.RFC3339), func(key string, counts RequestCounts) {
		if entry := hourly[key]; entry != nil {
			entry.RequestCounts = counts
		}
	}); err != nil {
		return result, err
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT r.actor_id, COALESCE(a.name, ''), MAX(r.actor_kind),
			SUM(r.requests), SUM(r.errors), SUM(r.duration_ms_sum), MAX(r.last_seen_at)
		FROM request_activity_hourly r LEFT JOIN actors a ON a.id=r.actor_id
		WHERE r.hour >= ? AND r.actor_id <> ''
		GROUP BY r.actor_id
		ORDER BY SUM(r.requests) DESC, r.actor_id
		LIMIT 20`, sinceText)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var actor RequestActor
		var duration uint64
		if err := rows.Scan(&actor.ActorID, &actor.Name, &actor.Kind, &actor.Requests, &actor.Errors, &duration, &actor.LastSeenAt); err != nil {
			return result, err
		}
		if actor.Requests > 0 {
			actor.AvgMS = duration / actor.Requests
		}
		result.TopActors = append(result.TopActors, actor)
	}
	return result, rows.Err()
}

func (s *Store) scanRequestGroups(ctx context.Context, query, arg string, apply func(string, RequestCounts)) error {
	rows, err := s.DB.QueryContext(ctx, query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key sql.NullString
		var counts RequestCounts
		if err := scanCounts(rows, []any{&key}, &counts); err != nil {
			return err
		}
		apply(key.String, counts)
	}
	return rows.Err()
}
