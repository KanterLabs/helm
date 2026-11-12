package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Classification version 2 (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D2). The
// qualifying and excluded rules are version 1's
// (skills/helm/scripts/helm_focus_activity.py) except for the D2 table below;
// test/neglect/PARITY.md and the parity test hold the two together.

// taskActivityV1Ignored is version 1's _IGNORED set.
var taskActivityV1Ignored = map[string]bool{
	"task.claimed": true, "task.claim_renewed": true, "task.claim_released": true,
	"task.heartbeat": true, "task.assigned": true, "task.release_changed": true, "task.released": true,
	"task.checklist_reordered": true, "task.reordered": true,
}

// taskActivityTableExcluded and taskActivityTableQualifying are the plan D2
// table: the only departures from version 1. Keep them identical to
// TABLE_EXCLUDED / TABLE_QUALIFYING in test/neglect/corpus_run.py.
var taskActivityTableExcluded = map[string]bool{
	"comment.updated": true, "comment.deleted": true,
	"task.parent_linked": true, "task.parent_unlinked": true,
	"task.deleted": true, "attachment.updated": true, "attachment.deleted": true,
	"task.dependency_state_changed": true,
}

var taskActivityTableQualifying = map[string]bool{
	"task.restored": true, "bug.triaged": true, "attachment.uploaded": true,
}

var taskActivityLifecycle = map[string]bool{
	"task.completed": true, "task.blocked": true, "task.reopened": true,
	"bug.resolved": true, "bug.reopened": true, "task.resolved": true,
}

var errTaskActivityMalformed = errors.New("malformed timeline row")

// taskActivityProgressSignature mirrors version 1's _PROGRESS_FIELDS. Empty
// and absent lists compare equal because the API omits empty attachments.
type taskActivityProgressSignature struct {
	phase, completed, total, refs, nextAction, attachments string
}

type taskActivityState struct {
	previousProgress *taskActivityProgressSignature
	checklist        map[string]bool // item key -> last completed value is true
}

// classifyTaskActivity classifies a task's complete history. items may be in
// any order (they are sorted by time then id, as version 1 sorts);
// undecodable counts stored rows the reader could not decode. Precedence: any
// unparseable time, then any malformed or undecodable row, makes the result
// unknown; otherwise known, or bounded when an ambiguous row is at or after
// the last qualifying one. It never fails.
func classifyTaskActivity(taskCreatedAt string, items []TaskTimelineItem, undecodable int) TaskActivitySignal {
	type timedItem struct {
		at   time.Time
		item TaskTimelineItem
	}
	rows := make([]timedItem, 0, len(items))
	for _, item := range items {
		at, ok := parseTaskActivityTime(item.CreatedAt)
		if !ok {
			return unknownTaskActivity(TaskActivityReasonUnparseableTime)
		}
		rows = append(rows, timedItem{at: at, item: item})
	}
	if undecodable > 0 {
		return unknownTaskActivity(TaskActivityReasonClassificationError)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].at.Equal(rows[j].at) {
			return rows[i].at.Before(rows[j].at)
		}
		return rows[i].item.ID < rows[j].item.ID
	})
	latestAt, latestKnown := parseTaskActivityTime(taskCreatedAt)
	latestSource := "task.created"
	var ambiguousAt time.Time
	ambiguousReason := ""
	state := taskActivityState{checklist: map[string]bool{}}
	for _, row := range rows {
		source, reason, err := state.classify(row.item)
		if err != nil {
			return unknownTaskActivity(TaskActivityReasonClassificationError)
		}
		if source != "" {
			latestAt, latestKnown, latestSource = row.at, true, source
		}
		if reason != "" {
			ambiguousAt, ambiguousReason = row.at, reason // sorted: newest wins
		}
	}
	if !latestKnown {
		return unknownTaskActivity(TaskActivityReasonUnparseableTime)
	}
	if ambiguousReason != "" && !ambiguousAt.Before(latestAt) {
		return TaskActivitySignal{Status: TaskActivityBounded, NotAfter: &ambiguousAt, Reason: ambiguousReason,
			ClassificationVersion: TaskActivityClassificationVersion}
	}
	return TaskActivitySignal{Status: TaskActivityKnown, LastMeaningfulAt: &latestAt, NotAfter: &latestAt,
		Source: latestSource, ClassificationVersion: TaskActivityClassificationVersion}
}

func unknownTaskActivity(reason string) TaskActivitySignal {
	return TaskActivitySignal{Status: TaskActivityUnknown, Reason: reason, ClassificationVersion: TaskActivityClassificationVersion}
}

// parseTaskActivityTime accepts what version 1's parse_time accepts from
// server rows: an RFC 3339 instant with a zone (T or space separator).
// Naive or garbage values are unparseable.
func parseTaskActivityTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// classify returns the qualifying source or the ambiguity reason of one row
// (both empty for excluded rows), updating replay state as version 1 does.
func (state *taskActivityState) classify(item TaskTimelineItem) (string, string, error) {
	switch item.Kind {
	case "comment":
		switch {
		case item.Actor != nil && item.Actor.Kind == "human":
			return "human.comment", "", nil
		case item.Actor != nil && item.Actor.Kind == "agent":
			return "", "", nil
		default:
			return "", TaskActivityReasonActorUnavailable, nil
		}
	case "agent_progress":
		if item.Progress == nil {
			return "", "", errTaskActivityMalformed
		}
		signature, err := taskActivityProgressSignatureOf(item.Progress)
		if err != nil {
			return "", "", err
		}
		previous := state.previousProgress
		state.previousProgress = &signature
		if previous == nil {
			return "agent.progress_started", "", nil
		}
		if *previous != signature {
			return "agent.progress_changed", "", nil
		}
		return "", "", nil
	case "task_change":
		if item.Change == nil {
			return "", "", errTaskActivityMalformed
		}
		payload, err := decodeTaskActivityPayload(item.Change.Payload)
		if err != nil {
			return "", "", err
		}
		return state.classifyChange(item.Change.EventType, payload)
	default:
		return "", TaskActivityReasonUnknownEventType, nil
	}
}

func (state *taskActivityState) classifyChange(event string, payload map[string]any) (string, string, error) {
	switch {
	case taskActivityV1Ignored[event] || taskActivityTableExcluded[event]:
		return "", "", nil
	case event == "task.created" || taskActivityLifecycle[event] || taskActivityTableQualifying[event]:
		return event, "", nil
	case event == "task.updated":
		fields, present := payload["changed_fields"]
		names, err := taskActivityChangedFields(fields)
		if err != nil {
			return "", "", err
		}
		if names["title"] || names["description"] || names["column_id"] || taskActivityTruthy(payload["completed_at"]) {
			return "task.scope_or_state_changed", "", nil
		}
		if !present {
			return "", TaskActivityReasonLegacyUpdate, nil
		}
		return "", "", nil
	case event == "task.moved":
		if !reflect.DeepEqual(payload["from_column_id"], payload["to_column_id"]) {
			return event, "", nil
		}
		return "", "", nil
	case strings.HasPrefix(event, "task.dependency_"):
		return event, "", nil
	case event == "task.checklist_item_updated":
		key, err := taskActivityChecklistKey(payload)
		if err != nil {
			return "", "", err
		}
		completed := payload["completed"] == true
		qualifies := completed && !state.checklist[key]
		state.checklist[key] = completed
		if qualifies {
			return "task.checklist_completed", "", nil
		}
		return "", "", nil
	case event == "task.checklist_item_added":
		key, err := taskActivityChecklistKey(payload)
		if err != nil {
			return "", "", err
		}
		state.checklist[key] = payload["completed"] == true
		return "task.checklist_scope_changed", "", nil
	case event == "task.checklist_item_removed":
		return "task.checklist_scope_changed", "", nil
	default:
		return "", TaskActivityReasonUnknownEventType, nil
	}
}

// decodeTaskActivityPayload accepts a JSON object, null or an absent payload
// (both treated as {} like version 1); anything else is malformed.
func decodeTaskActivityPayload(raw json.RawMessage) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]any{}, nil
	}
	if trimmed[0] != '{' {
		return nil, errTaskActivityMalformed
	}
	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, errTaskActivityMalformed
	}
	return payload, nil
}

// taskActivityChangedFields mirrors set(payload.get('changed_fields') or []).
func taskActivityChangedFields(value any) (map[string]bool, error) {
	names := map[string]bool{}
	switch fields := value.(type) {
	case nil, string: // a string is a set of characters in v1: never a field name
	case []any:
		for _, field := range fields {
			if name, ok := field.(string); ok {
				names[name] = true
			}
		}
	default:
		return nil, errTaskActivityMalformed
	}
	return names, nil
}

// taskActivityChecklistKey mirrors v1's payload.get("item_id", "") dict key.
func taskActivityChecklistKey(payload map[string]any) (string, error) {
	value, present := payload["item_id"]
	if !present {
		value = ""
	}
	switch value.(type) {
	case []any, map[string]any: // unhashable in v1
		return "", errTaskActivityMalformed
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errTaskActivityMalformed
	}
	return string(encoded), nil
}

// taskActivityTruthy is Python truthiness for a decoded JSON value.
func taskActivityTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func taskActivityProgressSignatureOf(progress *TaskTimelineProgress) (taskActivityProgressSignature, error) {
	number := func(value *int) string {
		if value == nil {
			return "null"
		}
		encoded, _ := json.Marshal(*value)
		return string(encoded)
	}
	refs := progress.CheckpointRefs
	if refs == nil {
		refs = []string{}
	}
	encodedRefs, err := json.Marshal(refs)
	if err != nil {
		return taskActivityProgressSignature{}, errTaskActivityMalformed
	}
	attachments := ""
	if len(progress.Attachments) > 0 {
		encoded, err := json.Marshal(progress.Attachments)
		if err != nil {
			return taskActivityProgressSignature{}, errTaskActivityMalformed
		}
		attachments = string(encoded)
	}
	return taskActivityProgressSignature{phase: progress.Phase, completed: number(progress.CheckpointCompleted),
		total: number(progress.CheckpointTotal), refs: string(encodedRefs), nextAction: progress.NextAction,
		attachments: attachments}, nil
}
