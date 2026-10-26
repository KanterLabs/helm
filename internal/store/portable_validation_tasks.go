package store

import (
	"math"
	"strings"
)

func validatePortableProjectsAndTasks(archive *PortableArchive, report *PortableImportReport, projectSet map[string]PortableProject, columnSet map[string]PortableColumn, taskSet map[string]PortableTask, releaseSet map[string]PortableRelease) {
	projectKeySet, projectSlugSet := map[string]string{}, map[string]string{}
	for _, project := range archive.Projects {
		if !portableSafeID(project.ID) {
			addPortableIssue(report, "project", project.ID, "id", "id is required and must not contain whitespace")
		}
		if _, exists := projectSet[project.ID]; exists {
			addPortableIssue(report, "project", project.ID, "id", "duplicate project id")
		}
		projectSet[project.ID] = project
		if !projectKeyPattern.MatchString(project.Key) {
			addPortableIssue(report, "project", project.ID, "key", "key is invalid")
		}
		if !projectSlugPattern.MatchString(project.Slug) {
			addPortableIssue(report, "project", project.ID, "slug", "slug is invalid")
		}
		if !colorPattern.MatchString(project.Color) {
			addPortableIssue(report, "project", project.ID, "color", "color is invalid")
		}
		if project.Name == "" || len(project.Name) > 200 {
			addPortableIssue(report, "project", project.ID, "name", "name is invalid")
		}
		if len(project.Description) > 20000 {
			addPortableIssue(report, "project", project.ID, "description", "description is too long")
		}
		projectKey := strings.ToUpper(strings.TrimSpace(project.Key))
		if previous, exists := projectKeySet[projectKey]; exists && previous != project.ID {
			addPortableIssue(report, "project", project.ID, "key", "duplicate project key in archive")
		}
		projectKeySet[projectKey] = project.ID
		projectSlug := strings.ToLower(strings.TrimSpace(project.Slug))
		if previous, exists := projectSlugSet[projectSlug]; exists && previous != project.ID {
			addPortableIssue(report, "project", project.ID, "slug", "duplicate project slug in archive")
		}
		projectSlugSet[projectSlug] = project.ID
		if !portableTimestamp(project.CreatedAt, false) || !portableTimestamp(project.UpdatedAt, false) || (project.ArchivedAt != nil && !portableTimestamp(*project.ArchivedAt, false)) {
			addPortableIssue(report, "project", project.ID, "timestamp", "timestamps must be RFC3339")
		}
	}
	// Build the complete task index before validating individual task payloads;
	// bug duplicate_of references are allowed to point forward in archive order.
	for _, task := range archive.Tasks {
		if _, exists := taskSet[task.ID]; exists {
			addPortableIssue(report, "task", task.ID, "id", "duplicate task id")
		}
		taskSet[task.ID] = task
	}
	releaseNameSet := map[string]string{}
	for _, release := range archive.Releases {
		if !portableSafeID(release.ID) {
			addPortableIssue(report, "release", release.ID, "id", "id is required and must not contain whitespace")
		}
		if _, exists := releaseSet[release.ID]; exists {
			addPortableIssue(report, "release", release.ID, "id", "duplicate release id")
		}
		releaseSet[release.ID] = release
		if _, ok := projectSet[release.ProjectID]; !ok {
			addPortableIssue(report, "release", release.ID, "project_id", "project does not exist in archive")
		}
		if strings.TrimSpace(release.Name) == "" || len(release.Name) > 200 || reservedReleaseName(release.Name) {
			addPortableIssue(report, "release", release.ID, "name", "name is invalid")
		}
		if len(release.Description) > 10000 {
			addPortableIssue(report, "release", release.ID, "description", "description is too long")
		}
		if release.TargetDate != nil && !portableCalendarDate(*release.TargetDate) {
			addPortableIssue(report, "release", release.ID, "target_date", "target_date must be YYYY-MM-DD")
		}
		if release.ReleasedAt != nil && !portableTimestamp(*release.ReleasedAt, false) {
			addPortableIssue(report, "release", release.ID, "released_at", "released_at must be RFC3339")
		}
		if release.ReleasedBy != nil && !portableSafeID(*release.ReleasedBy) {
			addPortableIssue(report, "release", release.ID, "released_by", "released_by id is invalid")
		}
		status := portableReleaseStatus(release)
		if release.Status != "" && release.Status != status {
			addPortableIssue(report, "release", release.ID, "status", "status must agree with released_at")
		}
		if release.Status != "" && release.Status != "planned" && release.Status != "released" {
			addPortableIssue(report, "release", release.ID, "status", "status must be planned or released")
		}
		if release.Version <= 0 {
			addPortableIssue(report, "release", release.ID, "version", "version must be positive")
		}
		if !portableTimestamp(release.CreatedAt, false) || !portableTimestamp(release.UpdatedAt, false) {
			addPortableIssue(report, "release", release.ID, "timestamp", "timestamps must be RFC3339")
		}
		nameKey := release.ProjectID + "\x00" + strings.ToLower(strings.TrimSpace(release.Name))
		if previous, exists := releaseNameSet[nameKey]; exists && previous != release.ID {
			addPortableIssue(report, "release", release.ID, "name", "duplicate release name in project")
		}
		releaseNameSet[nameKey] = release.ID
	}
	for _, column := range archive.Columns {
		if !portableSafeID(column.ID) {
			addPortableIssue(report, "column", column.ID, "id", "id is invalid")
		}
		if _, exists := columnSet[column.ID]; exists {
			addPortableIssue(report, "column", column.ID, "id", "duplicate column id")
		}
		columnSet[column.ID] = column
		if _, ok := projectSet[column.ProjectID]; !ok {
			addPortableIssue(report, "column", column.ID, "project_id", "project does not exist in archive")
		}
		if column.Name == "" || len(column.Name) > 100 {
			addPortableIssue(report, "column", column.ID, "name", "name is invalid")
		}
		if !validState(column.SemanticState) {
			addPortableIssue(report, "column", column.ID, "semantic_state", "semantic_state is invalid")
		}
		if column.Position < 0 {
			addPortableIssue(report, "column", column.ID, "position", "position must be non-negative")
		}
		if !portableTimestamp(column.CreatedAt, false) || !portableTimestamp(column.UpdatedAt, false) {
			addPortableIssue(report, "column", column.ID, "timestamp", "timestamps must be RFC3339")
		}
	}
	for _, task := range archive.Tasks {
		if !portableSafeID(task.ID) {
			addPortableIssue(report, "task", task.ID, "id", "id is invalid")
		}
		if _, ok := projectSet[task.ProjectID]; !ok {
			addPortableIssue(report, "task", task.ID, "project_id", "project does not exist in archive")
		}
		column, ok := columnSet[task.ColumnID]
		if !ok {
			addPortableIssue(report, "task", task.ID, "column_id", "column does not exist in archive")
		} else if column.ProjectID != task.ProjectID {
			addPortableIssue(report, "task", task.ID, "column_id", "column belongs to another project")
		}
		if task.Number <= 0 {
			addPortableIssue(report, "task", task.ID, "number", "number must be positive")
		}
		if task.Kind != defaultTaskKind && task.Kind != bugKind {
			addPortableIssue(report, "task", task.ID, "kind", "kind must be task or bug")
		}
		if !validPriority(task.Priority) {
			addPortableIssue(report, "task", task.ID, "priority", "priority is invalid")
		}
		if task.AssigneeID != nil && !portableSafeID(*task.AssigneeID) {
			addPortableIssue(report, "task", task.ID, "assignee_id", "assignee id is invalid")
		}
		if task.ClaimedBy != nil && !portableSafeID(*task.ClaimedBy) {
			addPortableIssue(report, "task", task.ID, "claimed_by", "claimed_by id is invalid")
		}
		if task.ReleaseID != nil {
			if !portableSafeID(*task.ReleaseID) {
				addPortableIssue(report, "task", task.ID, "release_id", "release id is invalid")
			} else if release, exists := releaseSet[*task.ReleaseID]; !exists {
				addPortableIssue(report, "task", task.ID, "release_id", "release does not exist in archive")
			} else if release.ProjectID != task.ProjectID {
				addPortableIssue(report, "task", task.ID, "release_id", "release belongs to another project")
			}
		}
		if task.Title == "" || len(task.Title) > 500 {
			addPortableIssue(report, "task", task.ID, "title", "title is invalid")
		}
		if len(task.Description) > 100000 || math.IsNaN(task.Position) || math.IsInf(task.Position, 0) || task.Position < 0 || task.Position > 1e12 {
			addPortableIssue(report, "task", task.ID, "position", "task position is invalid")
		}
		if task.Version <= 0 {
			addPortableIssue(report, "task", task.ID, "version", "version must be positive")
		}
		if !portableTimestamp(task.CreatedAt, false) || !portableTimestamp(task.UpdatedAt, false) || (task.DueAt != nil && !portableTimestamp(*task.DueAt, false)) || (task.ClaimExpiresAt != nil && !portableTimestamp(*task.ClaimExpiresAt, false)) || (task.CompletedAt != nil && !portableTimestamp(*task.CompletedAt, false)) {
			addPortableIssue(report, "task", task.ID, "timestamp", "timestamps must be RFC3339")
		}
		if task.Kind == bugKind {
			if task.Bug == nil || strings.TrimSpace(task.Bug.ActualBehavior) == "" {
				addPortableIssue(report, "task", task.ID, "bug", "bug details with actual_behavior are required")
			}
			if task.Bug != nil {
				if !portableSafeID(task.Bug.ReporterID) {
					addPortableIssue(report, "task", task.ID, "bug.reporter_id", "reporter id is invalid")
				}
				if task.Bug.Severity != nil && !validBugSeverity(*task.Bug.Severity) {
					addPortableIssue(report, "task", task.ID, "bug.severity", "severity is invalid")
				}
				if task.Bug.Resolution != nil && !validBugResolution(*task.Bug.Resolution) {
					addPortableIssue(report, "task", task.ID, "bug.resolution", "resolution is invalid")
				}
				if task.Bug.ResolvedAt != nil && !portableTimestamp(*task.Bug.ResolvedAt, false) {
					addPortableIssue(report, "task", task.ID, "bug.resolved_at", "resolved_at must be RFC3339")
				}
				if task.Bug.ResolvedBy != nil && !portableSafeID(*task.Bug.ResolvedBy) {
					addPortableIssue(report, "task", task.ID, "bug.resolved_by", "resolved_by is invalid")
				}
				if task.Bug.DuplicateOf != nil && *task.Bug.DuplicateOf == task.ID {
					addPortableIssue(report, "task", task.ID, "bug.duplicate_of", "duplicate_of cannot reference itself")
				}
				if task.Bug.DuplicateOf != nil {
					if duplicate, ok := taskSet[*task.Bug.DuplicateOf]; !ok {
						addPortableIssue(report, "task", task.ID, "bug.duplicate_of", "duplicate_of task does not exist in archive")
					} else if duplicate.ProjectID != task.ProjectID {
						addPortableIssue(report, "task", task.ID, "bug.duplicate_of", "duplicate_of must reference a task in the same project")
					}
				}
			}
		} else if task.Bug != nil {
			addPortableIssue(report, "task", task.ID, "bug", "bug details require kind bug")
		}
	}
}
