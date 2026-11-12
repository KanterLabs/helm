package store

import (
	"context"
	"strconv"
	"time"
)

// Neglect listings (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D5, D6; contract and
// failure modes in test/neglect/API.md). listTasksCursor calls these hooks;
// the filter, sort and coverage rules live here.

// TaskActivityMaxInactiveDays bounds inactive_days (about ten years).
const TaskActivityMaxInactiveDays = 3650

// TaskPage is one page of a project task collection.
type TaskPage struct {
	Tasks      []Task
	HasMore    bool
	NextCursor string
	// Activity is set only when the filter used a neglect input.
	Activity *TaskActivityListing
}

// TaskActivityListing is the collection metadata of a neglect listing.
type TaskActivityListing struct {
	// EvaluatedAt is the page chain's instant: inactive means
	// not_after <= EvaluatedAt - InactiveDays*24h.
	EvaluatedAt time.Time
	Coverage    TaskActivityCoverage
}

// TaskActivityCoverage reports whether the listing is the complete answer.
type TaskActivityCoverage struct {
	Complete bool  `json:"complete"`
	Lag      int64 `json:"lag"`
	Pending  int   `json:"pending"`
}

// UsesActivitySignal reports whether a filter uses a neglect input.
func (f TaskFilter) UsesActivitySignal() bool {
	sortName, _ := NormalizeTaskSort(f.Sort)
	return f.InactiveDays != 0 || f.ActivityStatus != "" || sortName == TaskSortNotAfter
}

// ListTaskPage returns one page with activity metadata. Plain filters behave
// exactly like ListTasksCursor; IncludeActivity adds Task.activity.
func (s *Store) ListTaskPage(ctx context.Context, projectID string, filter TaskFilter) (TaskPage, error) {
	return s.listTasksCursor(ctx, projectID, filter, true)
}

// taskActivityListing carries one request's neglect state between the
// listTasksCursor hooks. A nil value means activity is not read at all.
type taskActivityListing struct {
	neglect     bool
	evaluatedAt time.Time
	progress    TaskActivitySignalProgress
}

func validateTaskActivityFilter(filter TaskFilter) error {
	if filter.InactiveDays < 0 || filter.InactiveDays > TaskActivityMaxInactiveDays {
		return invalid("inactive_days must be between 1 and "+strconv.Itoa(TaskActivityMaxInactiveDays), nil)
	}
	switch filter.ActivityStatus {
	case "", TaskActivityKnown, TaskActivityBounded, TaskActivityUnknown:
	case TaskActivityUndetermined:
		if filter.InactiveDays == 0 {
			return invalid("activity_status=undetermined requires inactive_days", nil)
		}
	default:
		return invalid("activity_status must be known, bounded, unknown, or undetermined", nil)
	}
	if filter.UsesActivitySignal() && filter.Cursor != 0 {
		return invalid("neglect listings continue only with the keyset cursor they return", nil)
	}
	return nil
}

// beginTaskActivityListing validates the neglect inputs and, for a neglect
// listing, waits up to filter.ActivityWait for the projector to reach the
// project's newest event (plan D5). It runs before the collection snapshot.
func (s *Store) beginTaskActivityListing(ctx context.Context, projectID string, filter TaskFilter, evaluatedAt time.Time) (*taskActivityListing, error) {
	if err := validateTaskActivityFilter(filter); err != nil {
		return nil, err
	}
	if !filter.UsesActivitySignal() {
		if filter.IncludeActivity {
			return &taskActivityListing{evaluatedAt: evaluatedAt}, nil
		}
		return nil, nil
	}
	const target = `SELECT COALESCE(MAX(cursor), 0) FROM events WHERE project_id=?`
	s.observe(target, []any{projectID})
	var high int64
	if err := s.DB.QueryRowContext(ctx, target, projectID).Scan(&high); err != nil {
		return nil, err
	}
	s.observe("task_activity_signal_wait", nil)
	progress, _, err := s.WaitForTaskActivitySignal(ctx, high, filter.ActivityWait)
	if err != nil {
		return nil, err
	}
	return &taskActivityListing{neglect: true, evaluatedAt: evaluatedAt, progress: progress}, nil
}

// appendFilter adds the activity predicate. Terminal tasks (completed column
// or completed_at) are excluded; only rows at the current classification
// version count, so a task without one is left out and coverage says so.
func (l *taskActivityListing) appendFilter(query string, args []any, filter TaskFilter) (string, []any) {
	if l == nil || (filter.InactiveDays == 0 && filter.ActivityStatus == "") {
		return query, args
	}
	cutoff := l.evaluatedAt.Add(-time.Duration(filter.InactiveDays) * 24 * time.Hour).UTC().Format(taskActivitySignalTimeLayout)
	var condition string
	var conditionArgs []any
	switch {
	case filter.ActivityStatus == TaskActivityUndetermined:
		condition, conditionArgs = `s.status='bounded' AND s.not_after > ?`, []any{cutoff}
	case filter.ActivityStatus == TaskActivityUnknown:
		condition = `s.status='unknown'`
	case filter.ActivityStatus != "" && filter.InactiveDays > 0:
		condition, conditionArgs = `s.status=? AND s.not_after <= ?`, []any{filter.ActivityStatus, cutoff}
	case filter.ActivityStatus != "":
		condition, conditionArgs = `s.status=?`, []any{filter.ActivityStatus}
	default:
		// Plan D2: unknown rows have no bound and are always surfaced.
		condition, conditionArgs = `((s.status IN ('known', 'bounded') AND s.not_after <= ?) OR s.status='unknown')`, []any{cutoff}
	}
	query += ` AND t.completed_at IS NULL AND c.semantic_state <> 'completed' AND EXISTS (SELECT 1 FROM task_activity_signal s
		WHERE s.task_id=t.id AND s.classification_version=? AND ` + condition + `)`
	args = append(args, TaskActivityClassificationVersion)
	return query, append(args, conditionArgs...)
}

// populate sets Task.activity on the page. For a neglect listing a task is
// pending relative to the snapshot the page was read under.
func (s *Store) populateTaskActivityListing(ctx context.Context, l *taskActivityListing, tasks []Task, snapshot taskCollectionSnapshot) (int, error) {
	if l == nil {
		return 0, nil
	}
	if !l.neglect {
		return 0, s.PopulateTaskActivity(ctx, tasks)
	}
	return s.populateTaskActivityAt(ctx, tasks, l.progress, max(l.progress.High, snapshot.EventCursor))
}

// metadata reports coverage: complete only when the projector processed
// every event of the project up to the snapshot, every live task of the
// project has a current row, and no returned task is pending.
func (s *Store) taskActivityListingMetadata(ctx context.Context, l *taskActivityListing, projectID string, snapshot taskCollectionSnapshot, pending int) (*TaskActivityListing, error) {
	if l == nil || !l.neglect {
		return nil, nil
	}
	coverage := TaskActivityCoverage{Lag: max(snapshot.EventCursor-l.progress.Processed, 0), Pending: pending}
	if !l.progress.Initialized {
		coverage.Lag = snapshot.EventCursor
	}
	coverage.Complete = l.progress.Initialized && coverage.Lag == 0 && pending == 0
	if coverage.Complete {
		// One pass over the project's live tasks by primary key; it also
		// catches a row missing after the seed (never expected).
		const unseeded = `SELECT EXISTS (SELECT 1 FROM tasks t WHERE t.project_id=? AND t.deleted_at IS NULL AND ` + taskActivitySignalMissingRow + `)`
		s.observe(unseeded, nil)
		var missing bool
		if err := s.DB.QueryRowContext(ctx, unseeded, projectID, TaskActivityClassificationVersion).Scan(&missing); err != nil {
			return nil, err
		}
		coverage.Complete = !missing
	}
	return &TaskActivityListing{EvaluatedAt: l.evaluatedAt, Coverage: coverage}, nil
}
