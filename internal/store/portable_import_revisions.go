package store

import (
	"context"
	"database/sql"
	"sort"
)

func bumpPortableTaskCollectionRevisions(ctx context.Context, tx *sql.Tx, plan *portableImportPlan) error {
	changedProjects := make(map[string]struct{})
	addProject := func(projectID string) {
		if projectID != "" {
			changedProjects[projectID] = struct{}{}
		}
	}
	taskProjects := make(map[string]string, len(plan.tasks)*2)
	for _, task := range plan.tasks {
		if task.id != "" && task.projectID != "" {
			taskProjects[task.id] = task.projectID
		}
		if task.source.ID != "" && task.projectID != "" {
			taskProjects[task.source.ID] = task.projectID
		}
	}
	labelProjects := make(map[string]string, len(plan.labels))
	for _, label := range plan.labels {
		if label.id != "" && label.projectID != "" {
			labelProjects[label.id] = label.projectID
		}
		if label.source.ID != "" && label.projectID != "" {
			labelProjects[label.source.ID] = label.projectID
		}
	}
	for _, project := range plan.projects {
		if project.create {
			addProject(project.id)
		}
	}
	for _, column := range plan.columns {
		if column.create {
			addProject(column.projectID)
		}
	}
	for _, label := range plan.labels {
		if label.create {
			addProject(label.projectID)
		}
	}
	for _, task := range plan.tasks {
		if task.create {
			addProject(task.projectID)
		}
	}
	// taskLabels only contains rows that preflight determined to be new; an
	// existing relation is counted as skipped and is not appended to the plan.
	for _, relation := range plan.taskLabels {
		if projectID := taskProjects[relation.TaskID]; projectID != "" {
			addProject(projectID)
		} else {
			addProject(labelProjects[relation.LabelID])
		}
	}
	for _, relation := range plan.dependencies {
		if !relation.create {
			continue
		}
		addProject(taskProjects[relation.taskID])
		if _, known := taskProjects[relation.taskID]; !known {
			addProject(taskProjects[relation.prerequisiteID])
		}
	}
	for _, relation := range plan.links {
		if !relation.create {
			continue
		}
		addProject(taskProjects[relation.sourceTaskID])
		if _, known := taskProjects[relation.sourceTaskID]; !known {
			addProject(taskProjects[relation.targetTaskID])
		}
	}
	for _, comment := range plan.comments {
		if comment.create {
			addProject(taskProjects[comment.taskID])
		}
	}
	for _, note := range plan.agentNotes {
		if note.create {
			addProject(taskProjects[note.taskID])
		}
	}
	for _, event := range plan.events {
		if !event.create {
			continue
		}
		if event.projectID != nil {
			addProject(*event.projectID)
		}
		if event.taskID != nil {
			addProject(taskProjects[*event.taskID])
		}
	}
	for _, work := range plan.work {
		if work.create {
			addProject(taskProjects[work.taskID])
		}
	}
	for _, history := range plan.history {
		if history.create {
			addProject(taskProjects[history.taskID])
		}
	}
	projectIDs := make([]string, 0, len(changedProjects))
	for projectID := range changedProjects {
		projectIDs = append(projectIDs, projectID)
	}
	if len(projectIDs) == 0 {
		return nil
	}
	sort.Strings(projectIDs)
	where, args := portableIDsClause("id", projectIDs)
	_, err := tx.ExecContext(ctx, `UPDATE projects SET task_collection_revision=task_collection_revision+1 WHERE 1=1`+where, args...)
	return err
}
