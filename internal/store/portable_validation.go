package store

import (
	"fmt"
	"strings"
	"time"
)

func portableImportError(report PortableImportReport, message string) error {
	if len(report.Errors) == 0 {
		report.Errors = []PortableImportIssue{{Entity: "archive", Message: message}}
	}
	return &Error{Kind: ErrInvalid, Message: message, Details: report}
}

func portableConflictError(report PortableImportReport, message string) error {
	return &Error{Kind: ErrConflict, Message: message, Details: report}
}

func portableSafeID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func portableTimestamp(value string, optional bool) bool {
	if strings.TrimSpace(value) == "" {
		return optional
	}
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func portableCalendarDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func addPortableIssue(report *PortableImportReport, entity, id, field, message string) {
	if len(report.Errors) >= 200 {
		return
	}
	report.Errors = append(report.Errors, PortableImportIssue{Entity: entity, ID: id, Field: field, Message: message})
}

// portableAgentWorkValidationField keeps the shared runtime validator's
// precise error on the same field in an import report. The validator itself
// lives in agent_work.go so live API writes and portable writes cannot drift
// on operation IDs, text lengths, checkpoint references, or progress pairs.
func portableAgentWorkValidationField(message string) string {
	switch {
	case strings.HasPrefix(message, "operation_id"):
		return "operation_id"
	case strings.HasPrefix(message, "state"):
		return "state"
	case strings.HasPrefix(message, "phase"):
		return "phase"
	case strings.HasPrefix(message, "summary"):
		return "summary"
	case strings.HasPrefix(message, "next_action"):
		return "next_action"
	case strings.HasPrefix(message, "checkpoint_refs"):
		return "checkpoint_refs"
	case strings.HasPrefix(message, "checkpoint_completed"), strings.HasPrefix(message, "checkpoint_total"), strings.HasPrefix(message, "checkpoint progress"):
		return "checkpoint"
	default:
		return "activity"
	}
}

func portableValidateAgentWork(report *PortableImportReport, entity, id string, input AgentWorkInput) (AgentWorkInput, bool) {
	validated, err := validateAgentWorkInput(input)
	if err == nil {
		return validated, true
	}
	addPortableIssue(report, entity, id, portableAgentWorkValidationField(err.Error()), err.Error())
	return input, false
}

func validatePortableArchive(archive *PortableArchive, report *PortableImportReport) {
	archive.normalize()
	report.Format, report.Version = archive.Format, archive.Version
	if archive.Format != PortableFormat {
		addPortableIssue(report, "archive", "", "format", "format must be helm.portable")
	}
	if archive.Version != 1 && archive.Version != PortableVersion && archive.Version != PortableVersionGithub {
		addPortableIssue(report, "archive", "", "version", fmt.Sprintf("version %d is not supported", archive.Version))
	}
	if archive.ExportedAt != "" && !portableTimestamp(archive.ExportedAt, false) {
		addPortableIssue(report, "archive", "", "exported_at", "exported_at must be RFC3339")
	}
	if len(archive.Projects) == 0 {
		addPortableIssue(report, "archive", "", "projects", "at least one project is required")
	}
	if len(archive.Projects) > portableMaxProjects {
		addPortableIssue(report, "archive", "", "projects", "too many projects")
	}
	if len(archive.Columns) > portableMaxColumns {
		addPortableIssue(report, "archive", "", "columns", "too many columns")
	}
	if len(archive.Releases) > portableMaxReleases {
		addPortableIssue(report, "archive", "", "releases", "too many releases")
	}
	if len(archive.Tasks) > portableMaxTasks {
		addPortableIssue(report, "archive", "", "tasks", "too many tasks")
	}
	if len(archive.Labels) > portableMaxLabels {
		addPortableIssue(report, "archive", "", "labels", "too many labels")
	}
	if len(archive.Comments) > portableMaxComments {
		addPortableIssue(report, "archive", "", "comments", "too many comments")
	}
	if len(archive.AgentNotes) > portableMaxAgentNotes {
		addPortableIssue(report, "archive", "", "agent_notes", "too many agent notes")
	}
	if len(archive.Relationships.TaskLabels)+len(archive.Relationships.Dependencies)+len(archive.Relationships.TaskLinks) > portableMaxRelations {
		addPortableIssue(report, "archive", "", "relationships", "too many relationships")
	}
	if len(archive.Activity.Events) > portableMaxEvents {
		addPortableIssue(report, "archive", "", "activity.events", "too many events")
	}
	if len(archive.Activity.AgentWork)+len(archive.Activity.AgentWorkHistory) > portableMaxActivity {
		addPortableIssue(report, "archive", "", "activity", "too many activity records")
	}
	projectSet, columnSet, taskSet, releaseSet := map[string]PortableProject{}, map[string]PortableColumn{}, map[string]PortableTask{}, map[string]PortableRelease{}
	validatePortableProjectsAndTasks(archive, report, projectSet, columnSet, taskSet, releaseSet)
	validatePortableRelationsAndActivity(archive, report, projectSet, taskSet)
	validatePortableGithub(archive, report, projectSet, taskSet)
}

func hasPortableCycle(graph map[string][]string) bool {
	state := map[string]uint8{}
	var visit func(string) bool
	visit = func(node string) bool {
		if state[node] == 1 {
			return true
		}
		if state[node] == 2 {
			return false
		}
		state[node] = 1
		for _, next := range graph[node] {
			if visit(next) {
				return true
			}
		}
		state[node] = 2
		return false
	}
	for node := range graph {
		if visit(node) {
			return true
		}
	}
	return false
}
