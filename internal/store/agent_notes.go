package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const MaxActiveAgentNotes = 6

var agentNoteCategories = map[string]bool{
	"known_issue": true, "rejected_approach": true, "constraint": true, "workaround": true,
}

func validateAgentNote(category, body string, evidence []string) (string, string, []string, error) {
	category, body = strings.TrimSpace(category), strings.TrimSpace(body)
	if !agentNoteCategories[category] {
		return "", "", nil, invalid("agent note category is invalid", nil)
	}
	if body == "" || len(body) > 500 {
		return "", "", nil, invalid("agent note body must be between 1 and 500 characters", nil)
	}
	if len(evidence) > 6 {
		return "", "", nil, invalid("agent note evidence is limited to 6 references", nil)
	}
	clean := make([]string, 0, len(evidence))
	seen := map[string]bool{}
	for _, ref := range evidence {
		ref = strings.TrimSpace(ref)
		if ref == "" || len(ref) > 256 || strings.ContainsAny(ref, "\r\n\x00") {
			return "", "", nil, invalid("agent note evidence reference is invalid", nil)
		}
		if !seen[ref] {
			clean = append(clean, ref)
			seen[ref] = true
		}
	}
	return category, body, clean, nil
}

func scanAgentNote(scanner interface{ Scan(...any) error }) (AgentNote, error) {
	var note AgentNote
	var evidence string
	var resolved sql.NullString
	if err := scanner.Scan(&note.ID, &note.TaskID, &note.ActorID, &note.Category, &note.Body, &evidence, &note.Version, &note.CreatedAt, &note.UpdatedAt, &resolved); err != nil {
		return AgentNote{}, err
	}
	if err := json.Unmarshal([]byte(evidence), &note.Evidence); err != nil {
		return AgentNote{}, err
	}
	if note.Evidence == nil {
		note.Evidence = []string{}
	}
	note.ResolvedAt = nullableString(resolved)
	return note, nil
}

func (s *Store) ListAgentNotes(ctx context.Context, taskID string, includeResolved bool) ([]AgentNote, error) {
	where := " AND resolved_at IS NULL"
	if includeResolved {
		where = ""
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,task_id,actor_id,category,body,evidence_json,version,created_at,updated_at,resolved_at FROM agent_notes WHERE task_id=?`+where+` ORDER BY created_at DESC,id DESC LIMIT 200`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AgentNote{}
	for rows.Next() {
		note, err := scanAgentNote(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, note)
	}
	return result, rows.Err()
}

func (s *Store) GetAgentNote(ctx context.Context, noteID string) (AgentNote, error) {
	note, err := scanAgentNote(s.DB.QueryRowContext(ctx, `SELECT id,task_id,actor_id,category,body,evidence_json,version,created_at,updated_at,resolved_at FROM agent_notes WHERE id=?`, noteID))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentNote{}, notFound("agent note not found")
	}
	return note, err
}

func (s *Store) CreateAgentNote(ctx context.Context, taskID, actorID, category, body string, evidence []string) (AgentNote, error) {
	category, body, evidence, err := validateAgentNote(category, body, evidence)
	if err != nil {
		return AgentNote{}, err
	}
	id, timestamp := newID(), now()
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var projectID string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM tasks WHERE id=? AND deleted_at IS NULL`, taskID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
			return notFound("task not found")
		} else if err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_notes WHERE task_id=? AND resolved_at IS NULL`, taskID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxActiveAgentNotes {
			return conflict("agent note limit reached; resolve or update an active note", nil)
		}
		encoded, _ := json.Marshal(evidence)
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_notes(id,task_id,actor_id,category,body,evidence_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, taskID, actorID, category, body, string(encoded), timestamp, timestamp); err != nil {
			return err
		}
		_, err := insertEvent(ctx, tx, "agent_note.created", actorID, projectID, taskID, map[string]any{"note_id": id, "category": category})
		return err
	})
	if err != nil {
		return AgentNote{}, err
	}
	return s.GetAgentNote(ctx, id)
}

func (s *Store) UpdateAgentNote(ctx context.Context, taskID, noteID, actorID, category, body string, evidence []string, expectedVersion int64, allowAdmin bool) (AgentNote, error) {
	category, body, evidence, err := validateAgentNote(category, body, evidence)
	if err != nil {
		return AgentNote{}, err
	}
	if expectedVersion <= 0 {
		return AgentNote{}, ErrPrecondition
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		var owner, projectID string
		var version int64
		var resolved sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT n.actor_id,n.version,n.resolved_at,t.project_id FROM agent_notes n JOIN tasks t ON t.id=n.task_id WHERE n.id=? AND n.task_id=? AND t.deleted_at IS NULL`, noteID, taskID).Scan(&owner, &version, &resolved, &projectID); errors.Is(err, sql.ErrNoRows) {
			return notFound("agent note not found")
		} else if err != nil {
			return err
		}
		if resolved.Valid {
			return conflict("resolved agent note cannot be edited", nil)
		}
		if owner != actorID && !allowAdmin {
			return forbidden("only the agent note author may edit this note")
		}
		if version != expectedVersion {
			return conflict("agent note has changed", nil)
		}
		encoded, _ := json.Marshal(evidence)
		timestamp := now()
		result, err := tx.ExecContext(ctx, `UPDATE agent_notes SET category=?,body=?,evidence_json=?,version=version+1,updated_at=? WHERE id=? AND task_id=? AND version=? AND resolved_at IS NULL`, category, body, string(encoded), timestamp, noteID, taskID, expectedVersion)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return conflict("agent note has changed", nil)
		}
		_, err = insertEvent(ctx, tx, "agent_note.updated", actorID, projectID, taskID, map[string]any{"note_id": noteID, "version": expectedVersion + 1})
		return err
	})
	if err != nil {
		return AgentNote{}, err
	}
	return s.GetAgentNote(ctx, noteID)
}

func (s *Store) ResolveAgentNote(ctx context.Context, taskID, noteID, actorID string, expectedVersion int64, allowAdmin bool) error {
	if expectedVersion <= 0 {
		return ErrPrecondition
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var owner, projectID string
		var version int64
		var resolved sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT n.actor_id,n.version,n.resolved_at,t.project_id FROM agent_notes n JOIN tasks t ON t.id=n.task_id WHERE n.id=? AND n.task_id=? AND t.deleted_at IS NULL`, noteID, taskID).Scan(&owner, &version, &resolved, &projectID); errors.Is(err, sql.ErrNoRows) {
			return notFound("agent note not found")
		} else if err != nil {
			return err
		}
		if resolved.Valid {
			return conflict("agent note is already resolved", nil)
		}
		if owner != actorID && !allowAdmin {
			return forbidden("only the agent note author may resolve this note")
		}
		if version != expectedVersion {
			return conflict("agent note has changed", nil)
		}
		timestamp := now()
		result, err := tx.ExecContext(ctx, `UPDATE agent_notes SET version=version+1,updated_at=?,resolved_at=? WHERE id=? AND task_id=? AND version=? AND resolved_at IS NULL`, timestamp, timestamp, noteID, taskID, expectedVersion)
		if err != nil {
			return err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			return conflict("agent note has changed", nil)
		}
		_, err = insertEvent(ctx, tx, "agent_note.resolved", actorID, projectID, taskID, map[string]any{"note_id": noteID, "version": expectedVersion + 1})
		return err
	})
}
