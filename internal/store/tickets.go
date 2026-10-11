package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Ticket queues. Status is always derived from the task's column semantic
// state, never from agent progress or claim ownership.
const (
	TicketQueueOpen       = "open"
	TicketQueueMine       = "mine"
	TicketQueueTriage     = "needs_triage"
	TicketQueueReady      = "ready"
	TicketQueueInProgress = "in_progress"
	TicketQueueWaiting    = "waiting"
	TicketQueueCompleted  = "completed"
)

// TicketQueues lists every accepted queue value.
var TicketQueues = []string{TicketQueueOpen, TicketQueueMine, TicketQueueTriage, TicketQueueReady, TicketQueueInProgress, TicketQueueWaiting, TicketQueueCompleted}

var ticketQueueStates = map[string]string{
	TicketQueueTriage:     "backlog",
	TicketQueueReady:      "ready",
	TicketQueueInProgress: "active",
	TicketQueueWaiting:    "blocked",
	TicketQueueCompleted:  "completed",
}

// TicketMembership marks a task as a Tickets workspace member.
type TicketMembership struct {
	Origin    string `json:"origin"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

// TicketCounts are server-authoritative queue sizes over the caller's full
// visible membership, independent of the loaded page.
type TicketCounts struct {
	Open        int `json:"open"`
	Mine        int `json:"mine"`
	NeedsTriage int `json:"needs_triage"`
	Ready       int `json:"ready"`
	InProgress  int `json:"in_progress"`
	Waiting     int `json:"waiting"`
	Completed   int `json:"completed"`
}

// TicketFilter selects tickets. A nil ProjectIDs means every non-archived
// project; an empty non-nil slice means none.
type TicketFilter struct {
	ProjectIDs []string
	Queue      string
	ActorID    string
	Query      string
	Offset     int
	Limit      int
}

func ticketStatusForState(state string) string {
	for queue, candidate := range ticketQueueStates {
		if candidate == state {
			return queue
		}
	}
	return TicketQueueTriage
}

func insertTicketTx(ctx context.Context, tx *sql.Tx, taskID, origin, created string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO tickets(task_id, origin, created_at) VALUES (?, ?, ?)`, taskID, origin, created)
	return err
}

func (s *Store) populateTicket(ctx context.Context, task *Task) error {
	var membership TicketMembership
	var state string
	err := s.DB.QueryRowContext(ctx, `SELECT tk.origin, tk.created_at, c.semantic_state FROM tickets tk JOIN tasks t ON t.id = tk.task_id JOIN columns c ON c.id = t.column_id WHERE tk.task_id = ?`, task.ID).Scan(&membership.Origin, &membership.CreatedAt, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	membership.Status = ticketStatusForState(state)
	task.Ticket = &membership
	return nil
}

// CreateTicket creates an ordinary task in the project's Backlog together
// with its ticket membership in one transaction.
func (s *Store) CreateTicket(ctx context.Context, projectID string, input TaskInput, actorID string) (Task, error) {
	if input.Kind != nil && *input.Kind != defaultTaskKind {
		return Task{}, invalid("tickets are ordinary tasks; kind must be task", nil)
	}
	if input.ColumnID != nil || input.ParentTaskID != nil || input.ReleaseID != nil {
		return Task{}, invalid("new tickets always start in Backlog without a parent or focus", nil)
	}
	return s.createTask(ctx, projectID, input, actorID, func(tx *sql.Tx, taskID string) error {
		return insertTicketTx(ctx, tx, taskID, "manual", now())
	})
}

func ticketScope(filter TicketFilter) (string, []any) {
	where := ` FROM tickets tk JOIN tasks t ON t.id = tk.task_id JOIN columns c ON c.id = t.column_id JOIN projects p ON p.id = t.project_id WHERE t.deleted_at IS NULL AND p.archived_at IS NULL`
	args := []any{}
	if filter.ProjectIDs != nil {
		if len(filter.ProjectIDs) == 0 {
			where += ` AND 1=0`
		} else {
			where += ` AND t.project_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(filter.ProjectIDs)), ",") + `)`
			for _, id := range filter.ProjectIDs {
				args = append(args, id)
			}
		}
	}
	if query := strings.TrimSpace(filter.Query); query != "" {
		where += ` AND (lower(t.title) LIKE ? OR lower(t.description) LIKE ? OR upper(p.key || '-' || t.number) = ?)`
		like := "%" + strings.ToLower(query) + "%"
		args = append(args, like, like, strings.ToUpper(query))
	}
	return where, args
}

// ListTickets returns one page in priority order, then oldest ticket first,
// so repeated alerts never reorder the queue.
func (s *Store) ListTickets(ctx context.Context, filter TicketFilter) ([]Task, bool, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	scope, args := ticketScope(filter)
	switch filter.Queue {
	case "", TicketQueueOpen:
		scope += ` AND c.semantic_state <> 'completed'`
	case TicketQueueMine:
		scope += ` AND c.semantic_state <> 'completed' AND t.assignee_id = ?`
		args = append(args, filter.ActorID)
	default:
		state, ok := ticketQueueStates[filter.Queue]
		if !ok {
			return nil, false, invalid("unknown ticket queue", nil)
		}
		scope += ` AND c.semantic_state = ?`
		args = append(args, state)
	}
	query := `SELECT ` + taskColumns + scope + ` ORDER BY CASE t.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END, tk.created_at, t.id LIMIT ? OFFSET ?`
	args = append(args, limit+1, filter.Offset)
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	result := make([]Task, 0, limit)
	for rows.Next() {
		task, err := taskFromRow(rows)
		if err != nil {
			rows.Close()
			return nil, false, err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	readAt := time.Now().UTC()
	for i := range result {
		if err := s.enrichTaskAt(ctx, &result[i], readAt); err != nil {
			return nil, false, err
		}
	}
	if err := s.populateTaskDependencySummaries(ctx, result); err != nil {
		return nil, false, err
	}
	if err := s.populateTaskReleaseReferences(ctx, result); err != nil {
		return nil, false, err
	}
	return result, hasMore, nil
}

// CountTickets counts every queue over the same scope (project and search)
// that ListTickets applies, ignoring pagination.
func (s *Store) CountTickets(ctx context.Context, filter TicketFilter) (TicketCounts, error) {
	scope, args := ticketScope(filter)
	query := `SELECT c.semantic_state, COUNT(1), COALESCE(SUM(CASE WHEN t.assignee_id = ? THEN 1 ELSE 0 END), 0)` + scope + ` GROUP BY c.semantic_state`
	rows, err := s.DB.QueryContext(ctx, query, append([]any{filter.ActorID}, args...)...)
	if err != nil {
		return TicketCounts{}, err
	}
	defer rows.Close()
	var counts TicketCounts
	for rows.Next() {
		var state string
		var total, mine int
		if err := rows.Scan(&state, &total, &mine); err != nil {
			return TicketCounts{}, err
		}
		switch state {
		case "backlog":
			counts.NeedsTriage += total
		case "ready":
			counts.Ready += total
		case "active":
			counts.InProgress += total
		case "blocked":
			counts.Waiting += total
		case "completed":
			counts.Completed += total
			continue
		}
		counts.Open += total
		counts.Mine += mine
	}
	return counts, rows.Err()
}
