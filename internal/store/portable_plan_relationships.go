package store

import (
	"context"
	"fmt"
)

func planPortableRelationships(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, actorMap map[string]string, dependencyCycleConflict *bool) (portableImportPlan, error) {
	for _, relation := range archive.Relationships.TaskLabels {
		taskID, labelID := plan.taskMap[relation.TaskID], plan.labelMap[relation.LabelID]
		exists, err := portableTaskLabelExists(q, taskID, labelID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if exists {
			plan.report.Counts.TaskLabelsSkipped++
		} else {
			plan.report.Counts.TaskLabelsCreated++
		}
		if !exists {
			plan.taskLabels = append(plan.taskLabels, PortableTaskLabel{TaskID: taskID, LabelID: labelID})
		}
	}
	dependencyPrerequisiteCounts := map[string]int{}
	dependencyDependentCounts := map[string]int{}
	dependencyCountsLoaded := map[string]bool{}
	loadDependencyCounts := func(taskID string) error {
		if dependencyCountsLoaded[taskID] {
			return nil
		}
		var prerequisiteCount, dependentCount int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE task_id=?`, taskID).Scan(&prerequisiteCount); err != nil {
			return err
		}
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_dependencies WHERE prerequisite_task_id=?`, taskID).Scan(&dependentCount); err != nil {
			return err
		}
		dependencyPrerequisiteCounts[taskID] = prerequisiteCount
		dependencyDependentCounts[taskID] = dependentCount
		dependencyCountsLoaded[taskID] = true
		return nil
	}
	dependencyGraph := map[string]map[string]struct{}{}
	rows, err := q.QueryContext(ctx, `SELECT task_id,prerequisite_task_id FROM task_dependencies`)
	if err != nil {
		return portableImportPlan{}, err
	}
	for rows.Next() {
		var taskID, prerequisiteID string
		if err := rows.Scan(&taskID, &prerequisiteID); err != nil {
			rows.Close()
			return portableImportPlan{}, err
		}
		if dependencyGraph[taskID] == nil {
			dependencyGraph[taskID] = map[string]struct{}{}
		}
		dependencyGraph[taskID][prerequisiteID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return portableImportPlan{}, err
	}
	rows.Close()
	columnStates := map[string]string{}
	for _, column := range plan.columns {
		columnStates[column.id] = column.source.SemanticState
	}
	dependencyNodes := map[string]portableDependencyNode{}
	for _, task := range plan.tasks {
		if task.create {
			dependencyNodes[task.id] = portablePlannedDependencyNode(task, columnStates)
		}
	}
	loadDependencyNode := func(taskID string) (portableDependencyNode, bool, error) {
		if node, planned := dependencyNodes[taskID]; planned {
			return node, true, nil
		}
		return portableExistingDependencyNode(ctx, q, taskID)
	}
	dependencyLifecycleIssueSeen := map[string]struct{}{}
	for _, relation := range archive.Relationships.Dependencies {
		taskID, prerequisiteID := plan.taskMap[relation.TaskID], plan.taskMap[relation.PrerequisiteID]
		createdBy, err := portableMapOptionalActor(q, relation.CreatedBy, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		exists, err := portableDependencyExists(q, taskID, prerequisiteID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if exists {
			plan.report.Counts.DependenciesSkipped++
			plan.dependencies = append(plan.dependencies, portableDependencyPlan{source: relation, taskID: taskID, prerequisiteID: prerequisiteID, createdBy: createdBy, create: false})
			continue
		}
		dependentNode, dependentOK, err := loadDependencyNode(taskID)
		if err != nil {
			return portableImportPlan{}, err
		}
		prerequisiteNode, prerequisiteOK, err := loadDependencyNode(prerequisiteID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if !dependentOK {
			addPortableIssue(&plan.report, "dependency", relation.TaskID, "task_id", "destination task is not available")
		}
		if !prerequisiteOK {
			addPortableIssue(&plan.report, "dependency", relation.PrerequisiteID, "prerequisite_task_id", "destination prerequisite is not available")
		}
		if !dependentOK || !prerequisiteOK {
			continue
		}
		if !dependentNode.Live || !prerequisiteNode.Live || dependentNode.ProjectID != prerequisiteNode.ProjectID {
			addPortableIssue(&plan.report, "dependency", relation.TaskID, "task_id", "dependencies must stay within one live project")
			continue
		}
		if portableDependencyGraphHasPath(dependencyGraph, prerequisiteID, taskID) {
			addPortableIssue(&plan.report, "dependency", relation.TaskID, "prerequisite_task_id", "dependency would create a cycle in the destination graph")
			*dependencyCycleConflict = true
			continue
		}
		if err := loadDependencyCounts(taskID); err != nil {
			return portableImportPlan{}, err
		}
		if err := loadDependencyCounts(prerequisiteID); err != nil {
			return portableImportPlan{}, err
		}
		limitExceeded := false
		if dependencyPrerequisiteCounts[taskID] >= maxDirectTaskDependencies {
			addPortableIssue(&plan.report, "dependency", relation.TaskID, "task_id", fmt.Sprintf("destination task already has %d direct prerequisites; the limit is %d", dependencyPrerequisiteCounts[taskID], maxDirectTaskDependencies))
			limitExceeded = true
		}
		if dependencyDependentCounts[prerequisiteID] >= maxDirectTaskDependencies {
			addPortableIssue(&plan.report, "dependency", relation.PrerequisiteID, "prerequisite_task_id", fmt.Sprintf("destination prerequisite already has %d direct dependents; the limit is %d", dependencyDependentCounts[prerequisiteID], maxDirectTaskDependencies))
			limitExceeded = true
		}
		if limitExceeded {
			// Do not queue an INSERT that SQLite will reject. The accumulated
			// report is returned by both dry-run and write paths before any
			// mutation occurs.
			continue
		}
		if dependencyGraph[taskID] == nil {
			dependencyGraph[taskID] = map[string]struct{}{}
		}
		dependencyGraph[taskID][prerequisiteID] = struct{}{}
		dependencyPrerequisiteCounts[taskID]++
		dependencyDependentCounts[prerequisiteID]++
		plan.report.Counts.DependenciesCreated++
		plan.dependencies = append(plan.dependencies, portableDependencyPlan{source: relation, taskID: taskID, prerequisiteID: prerequisiteID, createdBy: createdBy, create: true})
		if dependentNode.SemanticState == "active" || dependentNode.SemanticState == "completed" || dependentNode.ClaimActive {
			for candidateID := range dependencyGraph[taskID] {
				candidate, candidateOK, candidateErr := loadDependencyNode(candidateID)
				if candidateErr != nil {
					return portableImportPlan{}, candidateErr
				}
				issueKey := taskID + "\x00" + candidateID
				if !candidateOK || !candidate.Live || candidate.ProjectID != dependentNode.ProjectID || !candidate.Completed || candidate.SemanticState != "completed" {
					if _, alreadyReported := dependencyLifecycleIssueSeen[issueKey]; !alreadyReported {
						addPortableIssue(&plan.report, "dependency", relation.TaskID, "task_id", "dependency prerequisites are not satisfied for this task state")
						dependencyLifecycleIssueSeen[issueKey] = struct{}{}
					}
				}
			}
		}
	}
	linkConflict := false
	for _, relation := range archive.Relationships.TaskLinks {
		sourceID, targetID := plan.taskMap[relation.SourceTaskID], plan.taskMap[relation.TargetTaskID]
		existingTarget, linkTypeExists, err := portableExistingLinkTarget(q, sourceID, relation.LinkType)
		if err != nil {
			return portableImportPlan{}, err
		}
		if linkTypeExists && existingTarget == targetID {
			plan.report.Counts.LinksSkipped++
			plan.links = append(plan.links, portableLinkPlan{source: relation, sourceTaskID: sourceID, targetTaskID: targetID, create: false})
			continue
		}
		if linkTypeExists {
			if options.Conflict == portableConflictFail {
				addPortableIssue(&plan.report, "task_link", relation.SourceTaskID, "target_task_id", fmt.Sprintf("destination already links source task %s with type %q to task %s", sourceID, relation.LinkType, existingTarget))
				linkConflict = true
				continue
			}
			plan.report.Counts.LinksSkipped++
			portableAddRemap(&plan.report, "task_link", relation.TargetTaskID, existingTarget, "target_task_id", "destination already has a different target for this source and link type; destination relationship was retained")
			plan.report.Warnings = append(plan.report.Warnings, fmt.Sprintf("task link %s/%s was retained because the destination already has target %s", relation.SourceTaskID, relation.LinkType, existingTarget))
			plan.links = append(plan.links, portableLinkPlan{source: relation, sourceTaskID: sourceID, targetTaskID: targetID, create: false})
			continue
		}
		plan.report.Counts.LinksCreated++
		plan.links = append(plan.links, portableLinkPlan{source: relation, sourceTaskID: sourceID, targetTaskID: targetID, create: true})
	}
	if linkConflict {
		return plan, portableConflictError(plan.report, "task link conflicts with a destination relationship")
	}
	return plan, nil
}
