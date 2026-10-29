package store

import "strings"

func validatePortableMetadata(archive *PortableArchive, report *PortableImportReport, tasks map[string]PortableTask) {
	if archive.Version < PortableVersionCurrent {
		report.Warnings = append(report.Warnings, portableLegacyWarning)
		if archive.Version < 3 {
			report.Warnings = append(report.Warnings, "portable versions 1-2 omit GitHub PR associations and audit history")
		}
		for _, p := range archive.Projects {
			if p.ChecklistCompletionPolicy != "" {
				addPortableIssue(report, "project", p.ID, "checklist_completion_policy", "completion policy requires portable version 4")
			}
		}
		for _, task := range archive.Tasks {
			if task.ParentTaskID != nil || len(task.Checklist) > 0 {
				addPortableIssue(report, "task", task.ID, "checklist", "hierarchy and checklist records require portable version 4")
			}
		}
		return
	}
	for _, p := range archive.Projects {
		if p.ChecklistCompletionPolicy != "warn" && p.ChecklistCompletionPolicy != "require" {
			addPortableIssue(report, "project", p.ID, "checklist_completion_policy", "policy must be warn or require")
		}
	}
	graph := map[string][]string{}
	fanout := map[string]int{}
	ids := map[string]bool{}
	for _, task := range archive.Tasks {
		issue := func(field, msg string) { addPortableIssue(report, "task", task.ID, field, msg) }
		if task.ParentTaskID != nil {
			parent, ok := tasks[*task.ParentTaskID]
			if !ok || parent.ProjectID != task.ProjectID || parent.ID == task.ID {
				issue("parent_task_id", "parent must be a different task in the same archived project")
			}
			graph[task.ID] = []string{*task.ParentTaskID}
			fanout[*task.ParentTaskID]++
			if fanout[*task.ParentTaskID] > MaxTaskHierarchyChildren {
				issue("parent_task_id", "parent has more than 200 children")
			}
		}
		if len(task.Checklist) > 100 {
			issue("checklist", "at most 100 checklist items are allowed")
		}
		textBytes := 0
		for _, item := range task.Checklist {
			textBytes += len(item.Text)
			if !portableSafeID(item.ID) || ids[item.ID] {
				issue("checklist.id", "checklist IDs must be valid and globally unique")
			}
			ids[item.ID] = true
			if strings.TrimSpace(item.Text) == "" || len([]rune(item.Text)) > 1000 || item.Position < 0 {
				issue("checklist", "item text or position is invalid")
			}
			if !portableTimestamp(item.CreatedAt, false) || !portableTimestamp(item.UpdatedAt, false) {
				issue("checklist", "item timestamps are invalid")
			}
			if item.CompletedAt != nil && !portableTimestamp(*item.CompletedAt, false) {
				issue("checklist.completed_at", "completion timestamp is invalid")
			}
			if item.CompletedBy != nil && !portableSafeID(*item.CompletedBy) {
				issue("checklist.completed_by", "completion actor is invalid")
			}
			if item.Completed && (item.CompletedAt == nil) || !item.Completed && (item.CompletedAt != nil || item.CompletedBy != nil) {
				issue("checklist", "completion metadata does not match completed state")
			}
		}
		if textBytes > 100000 {
			issue("checklist", "checklist text exceeds 100000 bytes")
		}
	}
	if hasPortableCycle(graph) {
		addPortableIssue(report, "archive", "", "parent_task_id", "task hierarchy contains a cycle")
		return
	}
	for id := range graph {
		cursor := id
		for depth := 0; ; depth++ {
			parents := graph[cursor]
			if len(parents) == 0 {
				break
			}
			if depth >= MaxTaskHierarchyDepth {
				addPortableIssue(report, "task", id, "parent_task_id", "hierarchy exceeds depth 20")
				break
			}
			cursor = parents[0]
		}
	}
}
