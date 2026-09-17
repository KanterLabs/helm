package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const maxReleaseQueueTasks = 2000

// ReleaseWorkQueueFilter controls one bounded, cursor-paginated queue read.
// Cursor is opaque to callers and captures the release/project snapshot that
// produced the previous page.
type ReleaseWorkQueueFilter struct {
	Cursor string
	Limit  int
}

type ReleaseWorkQueue struct {
	Release    ReleaseWorkQueueRelease  `json:"release"`
	Snapshot   ReleaseWorkQueueSnapshot `json:"snapshot"`
	Summary    ReleaseWorkQueueSummary  `json:"summary"`
	Data       []ReleaseWorkQueueItem   `json:"data"`
	NextCursor string                   `json:"next_cursor"`
}

type ReleaseWorkQueueRelease struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

type ReleaseWorkQueueSnapshot struct {
	ProjectRevision int64  `json:"project_revision"`
	ReadAt          string `json:"read_at"`
}

type ReleaseWorkQueueSummary struct {
	Direct                int `json:"direct"`
	Required              int `json:"required"`
	Completed             int `json:"completed"`
	Claimable             int `json:"claimable"`
	Owned                 int `json:"owned"`
	DependencyBlocked     int `json:"dependency_blocked"`
	ManuallyBlocked       int `json:"manually_blocked"`
	ClaimedElsewhere      int `json:"claimed_elsewhere"`
	CrossReleaseConflicts int `json:"cross_release_conflicts"`
}

type ReleaseWorkQueueTask struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Version int64  `json:"version"`
}

type ReleaseWorkQueueItem struct {
	Task         ReleaseWorkQueueTask `json:"task"`
	Relationship string               `json:"relationship"`
	Disposition  string               `json:"disposition"`
	BlockedBy    []TaskReference      `json:"blocked_by"`
}

type releaseWorkQueueCursor struct {
	ReleaseID              string `json:"release_id"`
	ReleaseVersion         int64  `json:"release_version"`
	ProjectID              string `json:"project_id"`
	ProjectRevision        int64  `json:"project_revision"`
	TaskCollectionRevision int64  `json:"task_collection_revision"`
	ReadAt                 string `json:"read_at"`
	Offset                 int    `json:"offset"`
}

type releaseQueueSnapshot struct {
	ProjectRevision        int64
	TaskCollectionRevision int64
}

type releaseQueueTask struct {
	Task           Task
	SemanticState  string
	ColumnPosition int
	Depth          int
}

func encodeReleaseWorkQueueCursor(cursor releaseWorkQueueCursor) string {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeReleaseWorkQueueCursor(value string) (releaseWorkQueueCursor, error) {
	var cursor releaseWorkQueueCursor
	if strings.TrimSpace(value) == "" {
		return cursor, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, invalid("cursor is invalid", nil)
	}
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.ReleaseID == "" || cursor.ProjectID == "" || cursor.ReadAt == "" || cursor.Offset < 0 || cursor.ReleaseVersion <= 0 || cursor.ProjectRevision < 0 || cursor.TaskCollectionRevision < 0 {
		return cursor, invalid("cursor is invalid", nil)
	}
	readAt, err := time.Parse(time.RFC3339Nano, cursor.ReadAt)
	if err != nil || readAt.After(time.Now().UTC()) {
		return cursor, invalid("cursor is invalid", nil)
	}
	return cursor, nil
}

func (s *Store) releaseQueueProjectSnapshot(ctx context.Context, projectID string) (releaseQueueSnapshot, error) {
	var snapshot releaseQueueSnapshot
	if err := s.DB.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT MAX(cursor) FROM events WHERE project_id=?), 0),
		COALESCE((SELECT task_collection_revision FROM projects WHERE id=?), 0)`, projectID, projectID).Scan(&snapshot.ProjectRevision, &snapshot.TaskCollectionRevision); err != nil {
		return releaseQueueSnapshot{}, err
	}
	return snapshot, nil
}

func releaseQueueChanged(expected releaseWorkQueueCursor, current releaseWorkQueueCursor) error {
	details := map[string]any{
		"restart":                   true,
		"expected_release_version":  expected.ReleaseVersion,
		"current_release_version":   current.ReleaseVersion,
		"expected_project_revision": expected.ProjectRevision,
		"current_project_revision":  current.ProjectRevision,
	}
	return &Error{Kind: errors.Join(ErrReleaseQueueChanged, ErrConflict), Message: "release work queue changed; restart pagination from the first page", Details: details}
}

func queueTaskFromRow(scanner interface{ Scan(...any) error }) (releaseQueueTask, error) {
	var result releaseQueueTask
	var assignee, claimed, claimExpiry, due, completed, parent, release sql.NullString
	if err := scanner.Scan(&result.Task.ID, &result.Task.Number, &result.Task.ProjectID, &result.Task.Kind, &result.Task.ColumnID, &result.Task.Title, &result.Task.Description, &result.Task.Priority, &result.Task.Position, &assignee, &claimed, &claimExpiry, &due, &result.Task.Version, &completed, &result.Task.CreatedAt, &result.Task.UpdatedAt, &parent, &release, &result.SemanticState, &result.ColumnPosition); err != nil {
		return releaseQueueTask{}, err
	}
	result.Task.Assignee = nullableString(assignee)
	result.Task.ClaimedBy = nullableString(claimed)
	result.Task.ClaimExpiresAt = nullableString(claimExpiry)
	result.Task.DueAt = nullableString(due)
	result.Task.CompletedAt = nullableString(completed)
	result.Task.ParentTaskID = nullableString(parent)
	result.Task.ParentID = result.Task.ParentTaskID
	result.Task.ReleaseID = nullableString(release)
	return result, nil
}

func (s *Store) loadReleaseQueueTasks(ctx context.Context, releaseID, projectID string) ([]releaseQueueTask, map[string][]string, error) {
	cte := `WITH RECURSIVE required(task_id) AS (
		SELECT id FROM tasks WHERE release_id=? AND project_id=? AND deleted_at IS NULL
		UNION
		SELECT dependency.prerequisite_task_id
		FROM task_dependencies dependency
		JOIN required current ON current.task_id=dependency.task_id
		JOIN tasks dependent ON dependent.id=dependency.task_id AND dependent.project_id=? AND dependent.deleted_at IS NULL
		JOIN tasks prerequisite ON prerequisite.id=dependency.prerequisite_task_id AND prerequisite.project_id=? AND prerequisite.deleted_at IS NULL
	)
	SELECT ` + taskColumns + `, c.semantic_state, c.position
	FROM required
	JOIN tasks t ON t.id=required.task_id AND t.project_id=? AND t.deleted_at IS NULL
	JOIN columns c ON c.id=t.column_id
	ORDER BY t.id`
	rows, err := s.DB.QueryContext(ctx, cte, releaseID, projectID, projectID, projectID, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	result := make([]releaseQueueTask, 0)
	byID := make(map[string]struct{})
	for rows.Next() {
		if len(result) >= maxReleaseQueueTasks+1 {
			break
		}
		item, scanErr := queueTaskFromRow(rows)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		result = append(result, item)
		byID[item.Task.ID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(result) > maxReleaseQueueTasks {
		return nil, nil, invalid("release work queue exceeds the bounded scope", map[string]any{"limit": maxReleaseQueueTasks})
	}

	dependencies := make(map[string][]string, len(result))
	edgeRows, err := s.DB.QueryContext(ctx, `WITH RECURSIVE required(task_id) AS (
		SELECT id FROM tasks WHERE release_id=? AND project_id=? AND deleted_at IS NULL
		UNION
		SELECT dependency.prerequisite_task_id
		FROM task_dependencies dependency
		JOIN required current ON current.task_id=dependency.task_id
		JOIN tasks dependent ON dependent.id=dependency.task_id AND dependent.project_id=? AND dependent.deleted_at IS NULL
		JOIN tasks prerequisite ON prerequisite.id=dependency.prerequisite_task_id AND prerequisite.project_id=? AND prerequisite.deleted_at IS NULL
	)
	SELECT dependency.task_id, dependency.prerequisite_task_id
	FROM task_dependencies dependency JOIN required ON required.task_id=dependency.task_id`, releaseID, projectID, projectID, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()
	for edgeRows.Next() {
		var taskID, prerequisiteID string
		if err := edgeRows.Scan(&taskID, &prerequisiteID); err != nil {
			return nil, nil, err
		}
		if _, ok := byID[taskID]; !ok {
			continue
		}
		if _, ok := byID[prerequisiteID]; !ok {
			continue
		}
		dependencies[taskID] = append(dependencies[taskID], prerequisiteID)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}
	return result, dependencies, nil
}

func (s *Store) GetReleaseWorkQueue(ctx context.Context, releaseID, actorID string, filter ReleaseWorkQueueFilter) (ReleaseWorkQueue, error) {
	release, err := s.GetRelease(ctx, releaseID)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	cursor, err := decodeReleaseWorkQueueCursor(filter.Cursor)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	projectSnapshot, err := s.releaseQueueProjectSnapshot(ctx, release.ProjectID)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	readAt := time.Now().UTC()
	offset := 0
	if filter.Cursor != "" {
		if cursor.ReleaseID != release.ID || cursor.ProjectID != release.ProjectID {
			return ReleaseWorkQueue{}, invalid("cursor does not match the requested release", nil)
		}
		if cursor.ReleaseVersion != release.Version || cursor.ProjectRevision != projectSnapshot.ProjectRevision || cursor.TaskCollectionRevision != projectSnapshot.TaskCollectionRevision {
			current := releaseWorkQueueCursor{ReleaseID: release.ID, ReleaseVersion: release.Version, ProjectID: release.ProjectID, ProjectRevision: projectSnapshot.ProjectRevision, TaskCollectionRevision: projectSnapshot.TaskCollectionRevision}
			return ReleaseWorkQueue{}, releaseQueueChanged(cursor, current)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, cursor.ReadAt)
		if parseErr != nil {
			return ReleaseWorkQueue{}, invalid("cursor is invalid", nil)
		}
		readAt = parsed.UTC()
		offset = cursor.Offset
	}

	tasks, dependencies, err := s.loadReleaseQueueTasks(ctx, release.ID, release.ProjectID)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	if err := s.enrichReleaseQueueTasks(ctx, tasks, readAt); err != nil {
		return ReleaseWorkQueue{}, err
	}
	byID := make(map[string]*releaseQueueTask, len(tasks))
	direct := make(map[string]bool, len(tasks))
	for i := range tasks {
		byID[tasks[i].Task.ID] = &tasks[i]
		direct[tasks[i].Task.ID] = tasks[i].Task.ReleaseID != nil && *tasks[i].Task.ReleaseID == release.ID
	}
	for _, item := range tasks {
		if !direct[item.Task.ID] {
			continue
		}
		queue := []string{item.Task.ID}
		seen := map[string]bool{item.Task.ID: true}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			currentDepth := byID[current].Depth
			for _, prerequisiteID := range dependencies[current] {
				if !seen[prerequisiteID] {
					seen[prerequisiteID] = true
					queue = append(queue, prerequisiteID)
				}
				if byID[prerequisiteID].Depth < currentDepth+1 {
					byID[prerequisiteID].Depth = currentDepth + 1
				}
			}
		}
	}
	for _, item := range tasks {
		if item.Depth != 0 {
			continue
		}
		// A dependency-free direct task has depth zero by definition. A
		// prerequisite shared by direct tasks receives its distance above.
		if !direct[item.Task.ID] {
			item.Depth = 1
		}
	}

	stateAt := readAt
	queueSummary := ReleaseWorkQueueSummary{}
	queueItems := make([]ReleaseWorkQueueItem, 0, len(tasks))
	for i := range tasks {
		item := &tasks[i]
		queueItem := ReleaseWorkQueueItem{
			Task:         ReleaseWorkQueueTask{ID: item.Task.ID, Key: item.Task.Key, Version: item.Task.Version},
			Relationship: "prerequisite",
			Disposition:  "claimable",
			BlockedBy:    []TaskReference{},
		}
		if direct[item.Task.ID] {
			queueItem.Relationship = "direct"
			queueSummary.Direct++
		}
		queueSummary.Required++
		complete := item.SemanticState == "completed" && item.Task.CompletedAt != nil
		if complete {
			queueSummary.Completed++
			queueItem.Disposition = "completed"
		} else {
			for _, prerequisiteID := range dependencies[item.Task.ID] {
				prerequisite := byID[prerequisiteID]
				if prerequisite == nil || (prerequisite.SemanticState == "completed" && prerequisite.Task.CompletedAt != nil) {
					continue
				}
				queueItem.BlockedBy = append(queueItem.BlockedBy, queueTaskReference(prerequisite.Task))
			}
			crossRelease := false
			for _, blocked := range queueItem.BlockedBy {
				prerequisite := byID[blocked.ID]
				if prerequisite != nil && prerequisite.Task.Release != nil && prerequisite.Task.Release.Status == "planned" && !direct[prerequisite.Task.ID] {
					crossRelease = true
					break
				}
			}
			activeClaim := queueTaskClaimActive(item.Task, stateAt)
			owned := activeClaim && item.Task.ClaimedBy != nil && *item.Task.ClaimedBy == actorID
			switch {
			case owned:
				queueItem.Disposition = "owned"
				queueSummary.Owned++
			case crossRelease:
				queueItem.Disposition = "cross_release_conflict"
				queueSummary.CrossReleaseConflicts++
			case item.SemanticState == "blocked":
				queueItem.Disposition = "manually_blocked"
				queueSummary.ManuallyBlocked++
			case len(queueItem.BlockedBy) > 0:
				queueItem.Disposition = "dependency_blocked"
				queueSummary.DependencyBlocked++
			case activeClaim:
				queueItem.Disposition = "claimed_elsewhere"
				queueSummary.ClaimedElsewhere++
			default:
				queueItem.Disposition = "claimable"
				queueSummary.Claimable++
			}
		}
		queueItems = append(queueItems, queueItem)
	}
	queueSummary.Required = len(queueItems)
	sort.SliceStable(queueItems, func(i, j int) bool {
		leftGroup := releaseQueueDispositionGroup(queueItems[i].Disposition)
		rightGroup := releaseQueueDispositionGroup(queueItems[j].Disposition)
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		left := byID[queueItems[i].Task.ID]
		right := byID[queueItems[j].Task.ID]
		if left.Depth != right.Depth {
			return left.Depth > right.Depth
		}
		if left.Task.Priority != right.Task.Priority {
			return releaseQueuePriority(left.Task.Priority) < releaseQueuePriority(right.Task.Priority)
		}
		if left.ColumnPosition != right.ColumnPosition {
			return left.ColumnPosition < right.ColumnPosition
		}
		if left.Task.Position != right.Task.Position {
			return left.Task.Position < right.Task.Position
		}
		if left.Task.Number != right.Task.Number {
			return left.Task.Number < right.Task.Number
		}
		return left.Task.ID < right.Task.ID
	})

	if offset > len(queueItems) {
		offset = len(queueItems)
	}
	end := offset + limit
	if end > len(queueItems) {
		end = len(queueItems)
	}
	page := append([]ReleaseWorkQueueItem(nil), queueItems[offset:end]...)
	nextCursor := ""
	if end < len(queueItems) {
		cursor := releaseWorkQueueCursor{ReleaseID: release.ID, ReleaseVersion: release.Version, ProjectID: release.ProjectID, ProjectRevision: projectSnapshot.ProjectRevision, TaskCollectionRevision: projectSnapshot.TaskCollectionRevision, ReadAt: readAt.Format(time.RFC3339Nano), Offset: end}
		nextCursor = encodeReleaseWorkQueueCursor(cursor)
	}
	currentRelease, err := s.GetRelease(ctx, release.ID)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	currentSnapshot, err := s.releaseQueueProjectSnapshot(ctx, release.ProjectID)
	if err != nil {
		return ReleaseWorkQueue{}, err
	}
	if currentRelease.Version != release.Version || currentSnapshot != projectSnapshot {
		expected := releaseWorkQueueCursor{ReleaseID: release.ID, ReleaseVersion: release.Version, ProjectID: release.ProjectID, ProjectRevision: projectSnapshot.ProjectRevision, TaskCollectionRevision: projectSnapshot.TaskCollectionRevision}
		current := releaseWorkQueueCursor{ReleaseID: release.ID, ReleaseVersion: currentRelease.Version, ProjectID: release.ProjectID, ProjectRevision: currentSnapshot.ProjectRevision, TaskCollectionRevision: currentSnapshot.TaskCollectionRevision}
		return ReleaseWorkQueue{}, releaseQueueChanged(expected, current)
	}
	return ReleaseWorkQueue{
		Release:    ReleaseWorkQueueRelease{ID: release.ID, Name: release.Name, Version: release.Version},
		Snapshot:   ReleaseWorkQueueSnapshot{ProjectRevision: projectSnapshot.ProjectRevision, ReadAt: readAt.Format(time.RFC3339Nano)},
		Summary:    queueSummary,
		Data:       page,
		NextCursor: nextCursor,
	}, nil
}

func (s *Store) ListReleaseWorkQueue(ctx context.Context, releaseID, actorID string, filter ReleaseWorkQueueFilter) (ReleaseWorkQueue, error) {
	return s.GetReleaseWorkQueue(ctx, releaseID, actorID, filter)
}

func (s *Store) ReleaseWorkQueue(ctx context.Context, releaseID, actorID string, filter ReleaseWorkQueueFilter) (ReleaseWorkQueue, error) {
	return s.GetReleaseWorkQueue(ctx, releaseID, actorID, filter)
}

func (s *Store) enrichReleaseQueueTasks(ctx context.Context, tasks []releaseQueueTask, readAt time.Time) error {
	plain := make([]Task, len(tasks))
	for i := range tasks {
		plain[i] = tasks[i].Task
		if err := s.enrichTaskAt(ctx, &plain[i], readAt); err != nil {
			return err
		}
	}
	if err := s.populateTaskReleaseReferences(ctx, plain); err != nil {
		return err
	}
	for i := range tasks {
		tasks[i].Task = plain[i]
	}
	return nil
}

func queueTaskReference(task Task) TaskReference {
	return TaskReference{ID: task.ID, Key: task.Key, Title: task.Title, CompletedAt: task.CompletedAt, Satisfied: task.CompletedAt != nil}
}

func queueTaskClaimActive(task Task, at time.Time) bool {
	if task.ClaimedBy == nil || task.ClaimExpiresAt == nil {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, *task.ClaimExpiresAt)
	return err == nil && expires.After(at)
}

func releaseQueueDispositionGroup(disposition string) int {
	switch disposition {
	case "owned":
		return 0
	case "claimable":
		return 1
	default:
		return 2
	}
}

func releaseQueuePriority(priority string) int {
	switch priority {
	case "urgent":
		return 0
	case "high":
		return 1
	case "normal":
		return 2
	default:
		return 3
	}
}
