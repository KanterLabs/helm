package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func portableEventExists(q portableSQL, id string) (bool, error) {
	var exists int
	err := q.QueryRowContext(context.Background(), `SELECT EXISTS(SELECT 1 FROM events WHERE id=?)`, id).Scan(&exists)
	return exists != 0, err
}

func portableExistingEvent(q portableSQL, id string) (PortableEvent, bool, error) {
	var event PortableEvent
	var actor, project, task, payload sql.NullString
	err := q.QueryRowContext(context.Background(), `SELECT cursor,id,type,actor_id,project_id,task_id,payload,created_at FROM events WHERE id=?`, id).Scan(&event.Cursor, &event.ID, &event.Type, &actor, &project, &task, &payload, &event.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PortableEvent{}, false, nil
	}
	if err != nil {
		return PortableEvent{}, false, err
	}
	event.ActorID, event.ProjectID, event.TaskID = nullableString(actor), nullableString(project), nullableString(task)
	if payload.Valid {
		event.Payload = json.RawMessage(payload.String)
	} else {
		event.Payload = json.RawMessage(`{}`)
	}
	return event, true, nil
}

func portableEventFieldsEqual(a, b PortableEvent) bool {
	return a.ID == b.ID && a.Type == b.Type && stringPointerValue(a.ActorID) == stringPointerValue(b.ActorID) && stringPointerValue(a.ProjectID) == stringPointerValue(b.ProjectID) && stringPointerValue(a.TaskID) == stringPointerValue(b.TaskID) && string(a.Payload) == string(b.Payload) && a.CreatedAt == b.CreatedAt
}

func portableEventFieldsEqualIgnoringID(a, b PortableEvent) bool {
	a.ID, b.ID = "", ""
	return portableEventFieldsEqual(a, b)
}

func portableEventPayloadTargets(key, parentKey, eventType string, maps portableEventPayloadMaps) map[string]string {
	switch key {
	case "item_id", "item_ids":
		if strings.HasPrefix(eventType, "task.checklist_") {
			return maps.checklists
		}
	case "artifact_id":
		return maps.artifacts
	case "audit_id":
		return maps.audits
	case "finding_id":
		return maps.findings
	case "project_id":
		return maps.projects
	case "release_id", "previous_release_id", "new_release_id", "old_release_id":
		return maps.releases
	case "task_id", "dependent_id", "prerequisite_id", "parent_id", "parent_task_id", "previous_parent_id", "previous_parent_task_id", "child_id", "before_task_id", "after_task_id", "duplicate_of":
		return maps.tasks
	case "actor_id", "actor", "released_by", "assignee", "previous_assignee", "assignee_id", "previous_assignee_id", "created_by", "resolved_by", "reporter_id":
		return maps.actors
	case "comment_id", "generated_comment_id":
		return maps.comments
	case "column_id", "from_column_id", "to_column_id":
		return maps.columns
	case "label_id":
		return maps.labels
	case "id":
		// A few event payloads carry a typed nested object instead of a flat
		// *_id field. Only remap an ambiguous id when its object type makes the
		// entity unambiguous.
		switch parentKey {
		case "artifact":
			return maps.artifacts
		case "audit":
			return maps.audits
		case "finding":
			return maps.findings
		case "project":
			return maps.projects
		case "release":
			return maps.releases
		case "task", "dependent", "prerequisite":
			return maps.tasks
		case "actor":
			return maps.actors
		case "comment":
			return maps.comments
		case "column":
			return maps.columns
		case "label":
			return maps.labels
		case "from", "to":
			if eventType == "task.moved" {
				return maps.columns
			}
		}
	}
	return nil
}

func portableEventPayloadActorKey(key, parentKey string) bool {
	if key == "actor_id" || key == "actor" || key == "released_by" || key == "assignee" || key == "previous_assignee" || key == "assignee_id" || key == "previous_assignee_id" || key == "created_by" || key == "resolved_by" || key == "reporter_id" {
		return true
	}
	return key == "id" && parentKey == "actor"
}

func portableRemapEventPayloadValue(q portableSQL, value any, key, parentKey, eventType, importer string, report *PortableImportReport, actorMap map[string]string, maps portableEventPayloadMaps) (any, bool, error) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for childKey, child := range typed {
			remapped, childChanged, err := portableRemapEventPayloadValue(q, child, childKey, key, eventType, importer, report, actorMap, maps)
			if err != nil {
				return nil, false, err
			}
			typed[childKey] = remapped
			changed = changed || childChanged
		}
		return typed, changed, nil
	case []any:
		changed := false
		for index, child := range typed {
			remapped, childChanged, err := portableRemapEventPayloadValue(q, child, key, parentKey, eventType, importer, report, actorMap, maps)
			if err != nil {
				return nil, false, err
			}
			typed[index] = remapped
			changed = changed || childChanged
		}
		return typed, changed, nil
	case string:
		targets := portableEventPayloadTargets(key, parentKey, eventType, maps)
		if targets != nil && portableEventPayloadActorKey(key, parentKey) {
			mapped, err := portableMapActor(q, typed, importer, report, actorMap)
			if err != nil {
				return nil, false, err
			}
			return mapped, mapped != typed, nil
		}
		if targets != nil {
			if mapped, ok := targets[typed]; ok {
				return mapped, mapped != typed, nil
			}
		}
	}
	return value, false, nil
}

func portableRemapEventPayload(q portableSQL, payload json.RawMessage, eventType, importer string, report *PortableImportReport, actorMap map[string]string, maps portableEventPayloadMaps) (json.RawMessage, error) {
	if len(payload) == 0 {
		return payload, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	remapped, changed, err := portableRemapEventPayloadValue(q, value, "", "", eventType, importer, report, actorMap, maps)
	if err != nil {
		return nil, err
	}
	if !changed {
		return payload, nil
	}
	encoded, err := json.Marshal(remapped)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}
