package store

import (
	"encoding/json"
	"strings"
)

func validatePortableRelationsAndActivity(archive *PortableArchive, report *PortableImportReport, projectSet map[string]PortableProject, taskSet map[string]PortableTask) {
	labelSet := map[string]PortableLabel{}
	labelNameSet := map[string]string{}
	for _, label := range archive.Labels {
		if !portableSafeID(label.ID) {
			addPortableIssue(report, "label", label.ID, "id", "id is invalid")
		}
		if _, exists := labelSet[label.ID]; exists {
			addPortableIssue(report, "label", label.ID, "id", "duplicate label id")
		}
		labelSet[label.ID] = label
		if _, ok := projectSet[label.ProjectID]; !ok {
			addPortableIssue(report, "label", label.ID, "project_id", "project does not exist in archive")
		}
		if label.Name == "" || len(label.Name) > 100 || !colorPattern.MatchString(label.Color) {
			addPortableIssue(report, "label", label.ID, "label", "label name or color is invalid")
		}
		labelName := label.ProjectID + "\x00" + strings.ToLower(strings.TrimSpace(label.Name))
		if previous, exists := labelNameSet[labelName]; exists && previous != label.ID {
			addPortableIssue(report, "label", label.ID, "name", "duplicate label name in project")
		}
		labelNameSet[labelName] = label.ID
		if !portableTimestamp(label.CreatedAt, false) || !portableTimestamp(label.UpdatedAt, false) {
			addPortableIssue(report, "label", label.ID, "timestamp", "timestamps must be RFC3339")
		}
	}
	actorSet := map[string]struct{}{}
	for _, actor := range archive.Actors {
		if !portableSafeID(actor.ID) {
			addPortableIssue(report, "actor", actor.ID, "id", "actor id is invalid")
		}
		if _, exists := actorSet[actor.ID]; exists {
			addPortableIssue(report, "actor", actor.ID, "id", "duplicate actor id")
		}
		actorSet[actor.ID] = struct{}{}
		if actor.Kind != "human" && actor.Kind != "agent" {
			addPortableIssue(report, "actor", actor.ID, "kind", "actor kind must be human or agent")
		}
		if strings.TrimSpace(actor.Name) == "" || len(actor.Name) > 200 {
			addPortableIssue(report, "actor", actor.ID, "name", "actor name is invalid")
		}
		if len(actor.Description) > 20000 {
			addPortableIssue(report, "actor", actor.ID, "description", "actor description is too long")
		}
		if actor.DisabledAt != nil && !portableTimestamp(*actor.DisabledAt, false) {
			addPortableIssue(report, "actor", actor.ID, "disabled_at", "disabled_at must be RFC3339")
		}
	}
	relationSeen := map[string]struct{}{}
	for _, relation := range archive.Relationships.TaskLabels {
		task, taskOK := taskSet[relation.TaskID]
		if !portableSafeID(relation.TaskID) || !portableSafeID(relation.LabelID) {
			addPortableIssue(report, "task_label", relation.TaskID, "id", "relationship identifiers are invalid")
		}
		if !taskOK {
			addPortableIssue(report, "task_label", relation.TaskID, "task_id", "task does not exist in archive")
		}
		label, labelOK := labelSet[relation.LabelID]
		if !labelOK {
			addPortableIssue(report, "task_label", relation.LabelID, "label_id", "label does not exist in archive")
		} else if taskOK && task.ProjectID != label.ProjectID {
			addPortableIssue(report, "task_label", relation.TaskID, "label_id", "task labels must stay within one project")
		}
		key := "label\x00" + relation.TaskID + "\x00" + relation.LabelID
		if _, exists := relationSeen[key]; exists {
			addPortableIssue(report, "task_label", key, "id", "duplicate relationship")
		}
		relationSeen[key] = struct{}{}
	}
	graph := map[string][]string{}
	for _, relation := range archive.Relationships.Dependencies {
		dependent, dependentOK := taskSet[relation.TaskID]
		prerequisite, prerequisiteOK := taskSet[relation.PrerequisiteID]
		if !portableSafeID(relation.TaskID) || !portableSafeID(relation.PrerequisiteID) {
			addPortableIssue(report, "dependency", relation.TaskID, "task_id", "task identifiers are invalid")
		}
		if !dependentOK || !prerequisiteOK {
			addPortableIssue(report, "dependency", relation.TaskID, "task_id", "both tasks must exist in archive")
			continue
		}
		if dependent.ProjectID != prerequisite.ProjectID {
			addPortableIssue(report, "dependency", relation.TaskID, "project_id", "dependencies must stay within one project")
		}
		if relation.TaskID == relation.PrerequisiteID {
			addPortableIssue(report, "dependency", relation.TaskID, "prerequisite_task_id", "dependency cannot reference itself")
		}
		key := "dependency\x00" + relation.TaskID + "\x00" + relation.PrerequisiteID
		if _, exists := relationSeen[key]; exists {
			addPortableIssue(report, "dependency", key, "id", "duplicate relationship")
		}
		relationSeen[key] = struct{}{}
		graph[relation.TaskID] = append(graph[relation.TaskID], relation.PrerequisiteID)
		if relation.CreatedBy != nil && !portableSafeID(*relation.CreatedBy) {
			addPortableIssue(report, "dependency", key, "created_by", "created_by is invalid")
		}
		if !portableTimestamp(relation.CreatedAt, false) {
			addPortableIssue(report, "dependency", key, "created_at", "created_at must be RFC3339")
		}
	}
	linkSeen, linkTypeSeen := map[string]struct{}{}, map[string]string{}
	for _, relation := range archive.Relationships.TaskLinks {
		sourceTask, sourceOK := taskSet[relation.SourceTaskID]
		if !sourceOK {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "source_task_id", "source task does not exist")
		}
		targetTask, targetOK := taskSet[relation.TargetTaskID]
		if !targetOK {
			addPortableIssue(report, "task_link", relation.TargetTaskID, "target_task_id", "target task does not exist")
		} else if sourceOK && sourceTask.ProjectID != targetTask.ProjectID {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "target_task_id", "task links must stay within one project")
		}
		if relation.SourceTaskID == relation.TargetTaskID {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "target_task_id", "link cannot reference itself")
		}
		if strings.TrimSpace(relation.LinkType) == "" || len(relation.LinkType) > 200 {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "link_type", "link_type is invalid")
		}
		if !portableTimestamp(relation.CreatedAt, false) {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "created_at", "created_at must be RFC3339")
		}
		if !portableSafeID(relation.SourceTaskID) || !portableSafeID(relation.TargetTaskID) {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "task_id", "task identifiers are invalid")
		}
		key := relation.SourceTaskID + "\x00" + relation.TargetTaskID + "\x00" + relation.LinkType
		if _, exists := linkSeen[key]; exists {
			addPortableIssue(report, "task_link", key, "id", "duplicate relationship")
		}
		linkSeen[key] = struct{}{}
		typeKey := relation.SourceTaskID + "\x00" + relation.LinkType
		if previous, exists := linkTypeSeen[typeKey]; exists && previous != relation.TargetTaskID {
			addPortableIssue(report, "task_link", relation.SourceTaskID, "link_type", "link_type may target only one task per source")
		}
		linkTypeSeen[typeKey] = relation.TargetTaskID
	}
	if hasPortableCycle(graph) {
		addPortableIssue(report, "dependency", "", "relationships", "dependencies contain a cycle")
	}
	commentSet := map[string]struct{}{}
	for _, comment := range archive.Comments {
		if !portableSafeID(comment.ID) || !portableSafeID(comment.TaskID) || !portableSafeID(comment.ActorID) {
			addPortableIssue(report, "comment", comment.ID, "id", "comment identifiers are invalid")
		}
		if _, ok := taskSet[comment.TaskID]; !ok {
			addPortableIssue(report, "comment", comment.ID, "task_id", "task does not exist in archive")
		}
		if strings.TrimSpace(comment.Body) == "" || len(comment.Body) > 20000 {
			addPortableIssue(report, "comment", comment.ID, "body", "comment body is invalid")
		}
		if !portableTimestamp(comment.CreatedAt, false) || !portableTimestamp(comment.UpdatedAt, false) {
			addPortableIssue(report, "comment", comment.ID, "timestamp", "timestamps must be RFC3339")
		}
		if _, exists := commentSet[comment.ID]; exists {
			addPortableIssue(report, "comment", comment.ID, "id", "duplicate comment id")
		}
		commentSet[comment.ID] = struct{}{}
	}
	noteSet := map[string]struct{}{}
	activeNotes := map[string]int{}
	for _, note := range archive.AgentNotes {
		if !portableSafeID(note.ID) || !portableSafeID(note.TaskID) || !portableSafeID(note.ActorID) {
			addPortableIssue(report, "agent_note", note.ID, "id", "agent note identifiers are invalid")
		}
		if _, ok := taskSet[note.TaskID]; !ok {
			addPortableIssue(report, "agent_note", note.ID, "task_id", "task does not exist in archive")
		}
		if _, _, _, err := validateAgentNote(note.Category, note.Body, note.Evidence); err != nil {
			addPortableIssue(report, "agent_note", note.ID, "content", "agent note content is invalid")
		}
		if note.Version < 1 {
			addPortableIssue(report, "agent_note", note.ID, "version", "version must be positive")
		}
		if !portableTimestamp(note.CreatedAt, false) || !portableTimestamp(note.UpdatedAt, false) || (note.ResolvedAt != nil && !portableTimestamp(*note.ResolvedAt, false)) {
			addPortableIssue(report, "agent_note", note.ID, "timestamp", "timestamps must be RFC3339")
		}
		if note.ResolvedAt == nil {
			activeNotes[note.TaskID]++
			if activeNotes[note.TaskID] > MaxActiveAgentNotes {
				addPortableIssue(report, "agent_note", note.ID, "task_id", "task has more than six active agent notes")
			}
		}
		if _, exists := noteSet[note.ID]; exists {
			addPortableIssue(report, "agent_note", note.ID, "id", "duplicate agent note id")
		}
		noteSet[note.ID] = struct{}{}
	}
	eventSet, eventCursorSet := map[string]struct{}{}, map[int64]struct{}{}
	for _, event := range archive.Activity.Events {
		if !portableSafeID(event.ID) || strings.TrimSpace(event.Type) == "" || len(event.Type) > 200 {
			addPortableIssue(report, "event", event.ID, "id", "event id or type is invalid")
		}
		if _, exists := eventSet[event.ID]; exists {
			addPortableIssue(report, "event", event.ID, "id", "duplicate event id")
		}
		eventSet[event.ID] = struct{}{}
		if event.Cursor < 0 {
			addPortableIssue(report, "event", event.ID, "cursor", "cursor must be non-negative")
		}
		if event.Cursor > 0 {
			if _, exists := eventCursorSet[event.Cursor]; exists {
				addPortableIssue(report, "event", event.ID, "cursor", "duplicate event cursor")
			}
			eventCursorSet[event.Cursor] = struct{}{}
		}
		if event.ActorID != nil && !portableSafeID(*event.ActorID) {
			addPortableIssue(report, "event", event.ID, "actor_id", "actor id is invalid")
		}
		if event.ProjectID != nil {
			if _, ok := projectSet[*event.ProjectID]; !ok {
				addPortableIssue(report, "event", event.ID, "project_id", "project does not exist in archive")
			}
		}
		if event.TaskID != nil {
			task, ok := taskSet[*event.TaskID]
			if !ok {
				addPortableIssue(report, "event", event.ID, "task_id", "task does not exist in archive")
			} else if event.ProjectID != nil && *event.ProjectID != task.ProjectID {
				addPortableIssue(report, "event", event.ID, "project_id", "event project must match event task project")
			}
		}
		if len(event.Payload) == 0 || !json.Valid(event.Payload) {
			addPortableIssue(report, "event", event.ID, "payload", "payload must be valid JSON")
		}
		if !portableTimestamp(event.CreatedAt, false) {
			addPortableIssue(report, "event", event.ID, "created_at", "created_at must be RFC3339")
		}
	}
	workTaskSet := map[string]struct{}{}
	for index := range archive.Activity.AgentWork {
		work := &archive.Activity.AgentWork[index]
		if _, exists := workTaskSet[work.TaskID]; exists {
			addPortableIssue(report, "agent_work", work.TaskID, "task_id", "duplicate live work snapshot")
		}
		workTaskSet[work.TaskID] = struct{}{}
		if _, ok := taskSet[work.TaskID]; !ok {
			addPortableIssue(report, "agent_work", work.TaskID, "task_id", "task does not exist in archive")
		}
		if !portableSafeID(work.TaskID) || !portableSafeID(work.ActorID) {
			addPortableIssue(report, "agent_work", work.TaskID, "id", "agent work identifiers are invalid")
		}
		validated, valid := portableValidateAgentWork(report, "agent_work", work.TaskID, AgentWorkInput{OperationID: work.OperationID, State: work.State, Phase: work.Phase, Summary: work.Summary, NextAction: work.NextAction, CheckpointRefs: work.CheckpointRefs, CheckpointCompleted: work.CheckpointCompleted, CheckpointTotal: work.CheckpointTotal})
		if valid {
			work.OperationID, work.State, work.Phase, work.Summary, work.NextAction, work.CheckpointRefs = validated.OperationID, validated.State, validated.Phase, validated.Summary, validated.NextAction, validated.CheckpointRefs
			work.CheckpointCompleted, work.CheckpointTotal = validated.CheckpointCompleted, validated.CheckpointTotal
		}
		if !portableTimestamp(work.StartedAt, false) || !portableTimestamp(work.UpdatedAt, false) {
			addPortableIssue(report, "agent_work", work.TaskID, "timestamp", "timestamps must be RFC3339")
		}
	}
	historySet := map[string]struct{}{}
	for index := range archive.Activity.AgentWorkHistory {
		history := &archive.Activity.AgentWorkHistory[index]
		if _, exists := historySet[history.ID]; exists {
			addPortableIssue(report, "agent_work_history", history.ID, "id", "duplicate history id")
		}
		historySet[history.ID] = struct{}{}
		if _, ok := taskSet[history.TaskID]; !ok {
			addPortableIssue(report, "agent_work_history", history.ID, "task_id", "task does not exist in archive")
		}
		if !portableSafeID(history.ID) || !portableSafeID(history.TaskID) || !portableSafeID(history.ActorID) {
			addPortableIssue(report, "agent_work_history", history.ID, "id", "history identifiers are invalid")
		}
		if !portableTimestamp(history.StartedAt, false) || !portableTimestamp(history.CreatedAt, false) {
			addPortableIssue(report, "agent_work_history", history.ID, "timestamp", "timestamps must be RFC3339")
		}
		validated, valid := portableValidateAgentWork(report, "agent_work_history", history.ID, AgentWorkInput{OperationID: history.OperationID, State: history.State, Phase: history.Phase, Summary: history.Summary, NextAction: history.NextAction, CheckpointRefs: history.CheckpointRefs, CheckpointCompleted: history.CheckpointCompleted, CheckpointTotal: history.CheckpointTotal})
		if valid {
			history.OperationID, history.State, history.Phase, history.Summary, history.NextAction, history.CheckpointRefs = validated.OperationID, validated.State, validated.Phase, validated.Summary, validated.NextAction, validated.CheckpointRefs
			history.CheckpointCompleted, history.CheckpointTotal = validated.CheckpointCompleted, validated.CheckpointTotal
		}
		if history.GeneratedCommentID != nil {
			if _, exists := commentSet[*history.GeneratedCommentID]; !exists {
				addPortableIssue(report, "agent_work_history", history.ID, "generated_comment_id", "generated comment does not exist in archive")
			}
		}
	}
}
