package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KanterLabs/helm/internal/store"
)

// Neglect signal on task reads (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D5, D6;
// wire contract in test/neglect/API.md). Task and collection reads carry
// Task.activity; mutation responses do not. Only project task listings with
// a neglect input wait for the projector, and only for this budget.
const taskActivityListingWait = 250 * time.Millisecond

var taskActivityStatusFilters = map[string]struct{}{
	store.TaskActivityKnown: {}, store.TaskActivityBounded: {}, store.TaskActivityUnknown: {}, store.TaskActivityUndetermined: {},
}

// parseTaskActivityFilter reads inactive_days and activity_status into the
// project task filter. Neglect listings continue only with the keyset cursor
// they return, which carries evaluated_at; a retained offset cursor cannot.
func parseTaskActivityFilter(r *http.Request, filter *store.TaskFilter) error {
	value, present, err := queryValue(r, "inactive_days")
	if err != nil {
		return err
	}
	if present {
		days, convErr := strconv.Atoi(strings.TrimSpace(value))
		if convErr != nil || days < 1 || days > store.TaskActivityMaxInactiveDays {
			return errors.New("inactive_days must be an integer between 1 and " + strconv.Itoa(store.TaskActivityMaxInactiveDays))
		}
		filter.InactiveDays = days
	}
	if filter.ActivityStatus, err = parseOptionalEnum(r, "activity_status", taskActivityStatusFilters); err != nil {
		return err
	}
	if filter.ActivityStatus == store.TaskActivityUndetermined && filter.InactiveDays == 0 {
		return errors.New("activity_status=undetermined requires inactive_days")
	}
	_, cursorPresent := r.URL.Query()["cursor"]
	if filter.UsesActivitySignal() && cursorPresent && filter.CursorToken == "" {
		return errors.New("neglect listings continue only with the keyset cursor they return")
	}
	filter.IncludeActivity = true
	filter.ActivityWait = taskActivityListingWait
	return nil
}

// writeTaskPage writes a project task page; a neglect listing adds
// evaluated_at and activity_coverage.
func (s *Server) writeTaskPage(w http.ResponseWriter, page store.TaskPage) {
	next := page.NextCursor
	if !page.HasMore {
		next = ""
	}
	if page.Activity == nil {
		s.writeCollection(w, page.Tasks, next)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"data": page.Tasks, "next_cursor": next,
		"evaluated_at":      page.Activity.EvaluatedAt.UTC().Format(time.RFC3339Nano),
		"activity_coverage": page.Activity.Coverage})
}

// writeTaskCollectionWithActivity populates Task.activity (no waiting) and
// writes an offset-paginated task collection.
func (s *Server) writeTaskCollectionWithActivity(w http.ResponseWriter, r *http.Request, tasks []store.Task, next string) {
	if err := s.Store.PopulateTaskActivity(r.Context(), tasks); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeCollection(w, tasks, next)
}

// writeTaskRead writes one task read with Task.activity; the ETag is the
// task version as before, because the derived field is not versioned.
func (s *Server) writeTaskRead(w http.ResponseWriter, r *http.Request, task store.Task) {
	tasks := []store.Task{task}
	if err := s.Store.PopulateTaskActivity(r.Context(), tasks); err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeTask(w, http.StatusOK, tasks[0])
}
