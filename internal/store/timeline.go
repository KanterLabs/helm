package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

var taskTimelineKinds = map[string]struct{}{
	"agent_progress": {},
	"comment":        {},
	"task_change":    {},
}

const timelineCursorVersion = 1

// taskTimelineCursor is encoded as a URL-safe opaque value. It carries the
// complete keyset boundary rather than an offset, so inserting newer activity
// cannot shift an older page. EventCursor is zero for legacy rows that did not
// have an associated event; kind and id complete the deterministic tie-break.
type taskTimelineCursor struct {
	Version     int    `json:"v"`
	CreatedAt   string `json:"at"`
	EventCursor int64  `json:"ec"`
	Kind        string `json:"k"`
	ID          string `json:"id"`
}

type timelineSortKey struct {
	createdAt   time.Time
	createdRaw  string
	eventCursor int64
	kind        string
	kindRank    int
	id          string
}

type timelineCandidate struct {
	item TaskTimelineItem
	key  timelineSortKey
}

func timelineKindRank(kind string) int {
	switch kind {
	case "agent_progress":
		return 3
	case "comment":
		return 2
	case "task_change":
		return 1
	default:
		return 0
	}
}

func newTimelineSortKey(createdAt, kind, id string, eventCursor int64) timelineSortKey {
	parsed, _ := time.Parse(time.RFC3339Nano, createdAt)
	return timelineSortKey{
		createdAt:   parsed,
		createdRaw:  createdAt,
		eventCursor: eventCursor,
		kind:        kind,
		kindRank:    timelineKindRank(kind),
		id:          id,
	}
}

// compareTimelineKeys returns a positive value when a is newer than b. The
// timestamp is the primary activity ordering; the event cursor preserves the
// existing append order for rows written in one clock tick. Legacy rows use a
// stable kind/id tie-break because no event cursor can be safely inferred.
func compareTimelineKeys(a, b timelineSortKey) int {
	if !a.createdAt.IsZero() && !b.createdAt.IsZero() {
		if a.createdAt.After(b.createdAt) {
			return 1
		}
		if a.createdAt.Before(b.createdAt) {
			return -1
		}
	} else if a.createdRaw != b.createdRaw {
		if a.createdRaw > b.createdRaw {
			return 1
		}
		return -1
	}
	if a.eventCursor != b.eventCursor {
		if a.eventCursor > b.eventCursor {
			return 1
		}
		return -1
	}
	if a.kindRank != b.kindRank {
		if a.kindRank > b.kindRank {
			return 1
		}
		return -1
	}
	if a.kind != b.kind {
		if a.kind > b.kind {
			return 1
		}
		return -1
	}
	if a.id > b.id {
		return 1
	}
	if a.id < b.id {
		return -1
	}
	return 0
}

func encodeTaskTimelineCursor(key timelineSortKey) (string, error) {
	payload, err := json.Marshal(taskTimelineCursor{
		Version:     timelineCursorVersion,
		CreatedAt:   key.createdRaw,
		EventCursor: key.eventCursor,
		Kind:        key.kind,
		ID:          key.id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeTaskTimelineCursor(value string) (timelineSortKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return timelineSortKey{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return timelineSortKey{}, invalid("before must be a valid timeline cursor", nil)
	}
	var payload taskTimelineCursor
	if err := json.Unmarshal(decoded, &payload); err != nil || payload.Version != timelineCursorVersion || payload.CreatedAt == "" || payload.ID == "" {
		return timelineSortKey{}, invalid("before must be a valid timeline cursor", nil)
	}
	if _, ok := taskTimelineKinds[payload.Kind]; !ok {
		return timelineSortKey{}, invalid("before must be a valid timeline cursor", nil)
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.CreatedAt); err != nil {
		return timelineSortKey{}, invalid("before must be a valid timeline cursor", nil)
	}
	return newTimelineSortKey(payload.CreatedAt, payload.Kind, payload.ID, payload.EventCursor), nil
}

type timelineActorRow struct {
	id   string
	kind string
	name string
}

func timelineActor(actorID, actorKind, actorName sql.NullString) *TimelineActor {
	if !actorID.Valid || actorID.String == "" || !actorKind.Valid || !actorName.Valid {
		// A retained event can outlive its actor. Keep the activity item visible
		// while omitting a fabricated identity; progress/comment details retain
		// their original actor_id where that field is part of the payload.
		return nil
	}
	return &TimelineActor{ID: actorID.String, Kind: actorKind.String, Name: actorName.String}
}

func decodeTimelineCheckpointRefs(value string) ([]string, error) {
	var refs []string
	if err := json.Unmarshal([]byte(value), &refs); err != nil {
		return nil, fmt.Errorf("decode agent work checkpoint_refs: %w", err)
	}
	if refs == nil {
		refs = []string{}
	}
	return refs, nil
}

type timelineHistoryRow struct {
	id, taskID, operationID, actorID, state, phase, summary, nextAction string
	refsJSON                                                            string
	completed, total                                                    sql.NullInt64
	startedAt, createdAt                                                string
	generatedCommentID                                                  sql.NullString
	progressEventCursor                                                 sql.NullInt64
	timelineEventCursor                                                 int64
	actorKind, actorName                                                sql.NullString
}

type timelineCommentRow struct {
	comment                       Comment
	timelineEventCursor           int64
	actorID, actorKind, actorName sql.NullString
}

type timelineEventRow struct {
	event                         Event
	actorID, actorKind, actorName sql.NullString
}

type timelineProjectEventRow struct {
	event                         Event
	actorID, actorKind, actorName sql.NullString
}

// ListTaskTimeline returns one stable newest-first page of the unified task
// activity stream. Each durable source is read with the same keyset boundary
// and a limit-sized lookahead before the typed rows are merged in Go. This
// keeps legacy comments/events visible without materializing the full history.
func (s *Store) ListTaskTimeline(ctx context.Context, taskID string, filter TaskTimelineFilter) ([]TaskTimelineItem, bool, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Kind != "" {
		if _, ok := taskTimelineKinds[filter.Kind]; !ok {
			return nil, false, invalid("kind is invalid", nil)
		}
	}
	boundary, err := decodeTaskTimelineCursor(filter.Before)
	if err != nil {
		return nil, false, err
	}

	// Deleted tasks are not readable through any task detail route. Keep the
	// store boundary aligned for direct callers as well.
	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id=? AND deleted_at IS NULL)`, taskID).Scan(&exists); err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, notFound("task not found")
	}

	candidates, err := s.listTaskTimelineCandidates(ctx, taskID, filter, boundary)
	if err != nil {
		return nil, false, err
	}
	items, more, err := finishTimelinePage(candidates, filter.Limit)
	if err != nil {
		return nil, false, err
	}
	if err := s.hydrateTimelineAttachments(ctx, items); err != nil {
		return nil, false, err
	}
	return items, more, nil
}

// ListProjectTimeline returns one stable newest-first page containing the
// unified activity stream for every non-deleted task in a project. It reads
// each durable source once with a live-task JOIN and merges those rows in one
// pass, so query work is bounded by the three activity sources rather than by
// the number of tasks on the board.
func (s *Store) ListProjectTimeline(ctx context.Context, projectID string, filter TaskTimelineFilter) ([]TaskTimelineItem, bool, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Kind != "" {
		if _, ok := taskTimelineKinds[filter.Kind]; !ok {
			return nil, false, invalid("kind is invalid", nil)
		}
	}
	boundary, err := decodeTaskTimelineCursor(filter.Before)
	if err != nil {
		return nil, false, err
	}

	var exists int
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=?)`, projectID).Scan(&exists); err != nil {
		return nil, false, err
	}
	if exists == 0 {
		return nil, false, notFound("project not found")
	}

	candidates, err := s.listProjectTimelineCandidates(ctx, projectID, filter, boundary)
	if err != nil {
		return nil, false, err
	}
	items, more, err := finishTimelinePage(candidates, filter.Limit)
	if err != nil {
		return nil, false, err
	}
	if err := s.hydrateTimelineAttachments(ctx, items); err != nil {
		return nil, false, err
	}
	return items, more, nil
}

// ListTimeline is a concise alias for callers that do not need the task
// prefix in the method name.
func (s *Store) ListTimeline(ctx context.Context, taskID string, filter TimelineFilter) ([]TaskTimelineItem, bool, error) {
	return s.ListTaskTimeline(ctx, taskID, filter)
}

// timelineKeysetPredicate mirrors compareTimelineKeys for one fixed source.
// A source has one kind/rank, so the final kind/id comparison is constant for
// all rows except when the boundary has that same kind. Persisted activity
// timestamps are UTC RFC3339Nano strings, which lets the indexed text ordering
// match the normalized cursor ordering without loading rows into Go first.
func timelineKeysetPredicate(createdExpr, eventCursorExpr, idExpr, kind string, boundary timelineSortKey) (string, []any) {
	if boundary.createdRaw == "" {
		return "", nil
	}
	predicate := fmt.Sprintf("%s < ? OR (%s = ? AND %s < ?)", createdExpr, createdExpr, eventCursorExpr)
	args := []any{boundary.createdRaw, boundary.createdRaw, boundary.eventCursor}
	sourceRank := timelineKindRank(kind)
	switch {
	case sourceRank < boundary.kindRank:
		predicate += fmt.Sprintf(" OR (%s = ? AND %s = ?)", createdExpr, eventCursorExpr)
		args = append(args, boundary.createdRaw, boundary.eventCursor)
	case sourceRank == boundary.kindRank:
		predicate += fmt.Sprintf(" OR (%s = ? AND %s = ? AND %s < ?)", createdExpr, eventCursorExpr, idExpr)
		args = append(args, boundary.createdRaw, boundary.eventCursor, boundary.id)
	}
	return "(" + predicate + ")", args
}

func finishTimelinePage(candidates []timelineCandidate, limit int) ([]TaskTimelineItem, bool, error) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareTimelineKeys(candidates[i].key, candidates[j].key) > 0
	})
	for index := range candidates {
		cursor, err := encodeTaskTimelineCursor(candidates[index].key)
		if err != nil {
			return nil, false, err
		}
		candidates[index].item.Cursor = cursor
	}
	hasMore := len(candidates) > limit
	if hasMore {
		candidates = candidates[:limit]
	}
	result := make([]TaskTimelineItem, len(candidates))
	for index := range candidates {
		result[index] = candidates[index].item
	}
	return result, hasMore, nil
}

func timelineHistoryCandidate(row timelineHistoryRow) (timelineCandidate, error) {
	refs, err := decodeTimelineCheckpointRefs(row.refsJSON)
	if err != nil {
		return timelineCandidate{}, err
	}
	progress := &TaskTimelineProgress{
		OperationID:    row.operationID,
		ActorID:        row.actorID,
		State:          row.state,
		Phase:          row.phase,
		Summary:        row.summary,
		NextAction:     row.nextAction,
		CheckpointRefs: refs,
		StartedAt:      row.startedAt,
	}
	if row.completed.Valid {
		value := int(row.completed.Int64)
		progress.CheckpointCompleted = &value
	}
	if row.total.Valid {
		value := int(row.total.Int64)
		progress.CheckpointTotal = &value
	}
	item := TaskTimelineItem{
		ID:        row.id,
		Kind:      "agent_progress",
		TaskID:    row.taskID,
		Actor:     timelineActor(sql.NullString{String: row.actorID, Valid: row.actorID != ""}, row.actorKind, row.actorName),
		CreatedAt: row.createdAt,
		Progress:  progress,
		Comment:   nil,
		Change:    nil,
	}
	return timelineCandidate{item: item, key: newTimelineSortKey(row.createdAt, item.Kind, item.ID, row.timelineEventCursor)}, nil
}

func timelineCommentCandidate(row timelineCommentRow) timelineCandidate {
	item := TaskTimelineItem{
		ID:        row.comment.ID,
		Kind:      "comment",
		TaskID:    row.comment.TaskID,
		Actor:     timelineActor(row.actorID, row.actorKind, row.actorName),
		CreatedAt: row.comment.CreatedAt,
		Progress:  nil,
		Comment:   &row.comment,
		Change:    nil,
	}
	return timelineCandidate{item: item, key: newTimelineSortKey(row.comment.CreatedAt, item.Kind, item.ID, row.timelineEventCursor)}
}

func timelineEventCandidate(event Event, actorID, actorKind, actorName sql.NullString, taskID string) timelineCandidate {
	item := TaskTimelineItem{
		ID:        event.ID,
		Kind:      "task_change",
		TaskID:    taskID,
		Actor:     timelineActor(actorID, actorKind, actorName),
		CreatedAt: event.CreatedAt,
		Progress:  nil,
		Comment:   nil,
		Change: &TaskTimelineChange{
			EventID:   event.ID,
			EventType: event.Type,
			Payload:   event.Payload,
		},
	}
	return timelineCandidate{item: item, key: newTimelineSortKey(event.CreatedAt, item.Kind, item.ID, event.Cursor)}
}

func timelineCommentID(event Event) string {
	if event.Type != "comment.created" || len(event.Payload) == 0 {
		return ""
	}
	var payload struct {
		CommentID string `json:"comment_id"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.CommentID)
}

func scanEventWithActor(scanner interface{ Scan(...any) error }, actorID, actorKind, actorName *sql.NullString) (Event, error) {
	var event Event
	var actor, project, task, payload sql.NullString
	if err := scanner.Scan(&event.Cursor, &event.ID, &event.Type, &actor, &project, &task, &payload, &event.CreatedAt, actorID, actorKind, actorName); err != nil {
		return Event{}, err
	}
	event.ActorID, event.ProjectID, event.TaskID = nullableString(actor), nullableString(project), nullableString(task)
	if payload.Valid {
		event.Payload = json.RawMessage(payload.String)
	} else {
		event.Payload = json.RawMessage(`{}`)
	}
	return event, nil
}

// TimelineCursorForTest exposes the opaque encoding to package-local tests
// without making the wire cursor parser part of the public API contract.
func TimelineCursorForTest(createdAt, kind, id string, eventCursor int64) string {
	cursor, _ := encodeTaskTimelineCursor(newTimelineSortKey(createdAt, kind, id, eventCursor))
	return cursor
}

// ParseTimelineCursorForTest mirrors the HTTP before validation for focused
// store tests. It intentionally returns only the normalized timestamp and
// event sequence needed by callers asserting stable boundaries.
func ParseTimelineCursorForTest(value string) (string, int64, error) {
	key, err := decodeTaskTimelineCursor(value)
	if err != nil {
		return "", 0, err
	}
	return key.createdRaw, key.eventCursor, nil
}
