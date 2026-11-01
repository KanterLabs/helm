package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

const intakeCursorPrefix = "ic1."

type intakeCursor struct {
	Version   int    `json:"v"`
	Filter    string `json:"filter"`
	Revision  string `json:"revision"`
	ReadAt    string `json:"read_at"`
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type IntakePage struct {
	Data       []Task
	NextCursor string
	Counts     IntakeCounts
}

func intakeProjectScope(query string, args []any, projectIDs []string) (string, []any) {
	if projectIDs == nil {
		return query, args
	}
	if len(projectIDs) == 0 {
		return query + ` AND 1=0`, args
	}
	query += ` AND p.id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(projectIDs)), ",") + `)`
	for _, id := range projectIDs {
		args = append(args, id)
	}
	return query, args
}

// Every cursor is bounded, opaque and tied to its query and effective reader
// authority. It never grants access; fresh authorization still filters every read.
func intakeFilterFingerprint(filter IntakeFilter) string {
	filter.ProjectIDs = appendPreservingNil(filter.ProjectIDs)
	sort.Strings(filter.ProjectIDs)
	filter.Limit = 0
	filter.Cursor = ""
	if filter.Scope == "" {
		filter.Scope = "needs_triage"
	}
	if filter.Severity == "none" {
		filter.Severity = "untriaged"
	}
	payload, _ := json.Marshal(filter)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func appendPreservingNil(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func decodeIntakeCursor(value, fingerprint string) (intakeCursor, error) {
	var cursor intakeCursor
	if len(value) > 4096 || !strings.HasPrefix(value, intakeCursorPrefix) {
		return cursor, invalid("invalid intake cursor", nil)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, intakeCursorPrefix))
	if err != nil {
		return cursor, invalid("invalid intake cursor", nil)
	}
	if err = json.Unmarshal(payload, &cursor); err != nil || cursor.Version != 1 || len(cursor.Revision) != 64 || cursor.ID == "" || len(cursor.ID) > 200 {
		return cursor, invalid("invalid intake cursor", nil)
	}
	if cursor.Filter != fingerprint {
		return cursor, invalid("cursor does not match intake filters or reader authority", nil)
	}
	readAt, err := time.Parse(time.RFC3339Nano, cursor.ReadAt)
	if err != nil || readAt.After(time.Now().UTC()) {
		return cursor, invalid("invalid intake cursor timestamp", nil)
	}
	if _, err = time.Parse(time.RFC3339Nano, cursor.CreatedAt); err != nil {
		return cursor, invalid("invalid intake cursor ordering", nil)
	}
	return cursor, nil
}

// The snapshot uses existing per-project event and task-collection revisions.
// Include the authorized project set and archival metadata so additions,
// archival and filing also invalidate a continuation. No raw IDs escape in the
// bounded hash. Unreadable project writes do not invalidate scoped readers.
func (s *Store) intakeSnapshot(ctx context.Context, projectIDs []string) (string, error) {
	query, args := intakeProjectScope(`SELECT p.id,COALESCE(p.archived_at,''),p.updated_at,p.task_collection_revision,COALESCE((SELECT MAX(e.cursor) FROM events e WHERE e.project_id=p.id),0) FROM projects p WHERE 1=1`, nil, projectIDs)
	rows, err := s.DB.QueryContext(ctx, query+` ORDER BY p.id`, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for rows.Next() {
		var id, archived, updated string
		var tasks, events int64
		if err := rows.Scan(&id, &archived, &updated, &tasks, &events); err != nil {
			return "", err
		}
		if err := encoder.Encode([]any{id, archived, updated, tasks, events}); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func intakeCollectionChanged() error {
	return &Error{Kind: ErrTaskCollectionChanged, Message: "intake collection changed; restart pagination from the first page", Details: map[string]any{"restart": true}}
}

// ReadIntake fences rows, enrichment and counts with the same snapshot. A
// mutation during the read or since the previous page returns an explicit
// restart instead of silently skipping or duplicating still-visible work.
func (s *Store) ReadIntake(ctx context.Context, filter IntakeFilter) (IntakePage, error) {
	var page IntakePage
	var cursor intakeCursor
	fingerprint := intakeFilterFingerprint(filter)
	readAt := time.Now().UTC()
	if filter.Cursor != "" {
		var err error
		cursor, err = decodeIntakeCursor(filter.Cursor, fingerprint)
		if err != nil {
			return page, err
		}
		readAt, _ = time.Parse(time.RFC3339Nano, cursor.ReadAt)
	}
	revision, err := s.intakeSnapshot(ctx, filter.ProjectIDs)
	if err != nil {
		return page, err
	}
	if filter.Cursor != "" && cursor.Revision != revision {
		return page, intakeCollectionChanged()
	}
	tasks, more, err := s.listIntake(ctx, filter, cursor, readAt)
	if err != nil {
		return page, err
	}
	counts, err := s.CountIntake(ctx, filter)
	if err != nil {
		return page, err
	}
	current, err := s.intakeSnapshot(ctx, filter.ProjectIDs)
	if err != nil {
		return page, err
	}
	if current != revision {
		return page, intakeCollectionChanged()
	}
	page = IntakePage{Data: tasks, Counts: counts}
	if more && len(tasks) > 0 {
		last := tasks[len(tasks)-1]
		payload, err := json.Marshal(intakeCursor{Version: 1, Filter: fingerprint, Revision: revision, ReadAt: readAt.UTC().Format(time.RFC3339Nano), CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			return IntakePage{}, err
		}
		page.NextCursor = intakeCursorPrefix + base64.RawURLEncoding.EncodeToString(payload)
	}
	return page, nil
}
