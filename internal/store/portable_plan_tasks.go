package store

import (
	"context"
	"fmt"
	"strconv"
)

func planPortableTasks(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedTaskIDs map[string]struct{}, actorMap map[string]string) (portableImportPlan, error) {
	var err error
	usedNumbers := map[string]map[int]struct{}{}
	for _, source := range archive.Tasks {
		projectID := plan.projectMap[source.ProjectID]
		if archive.Version < PortableVersionCurrent {
			if err := rejectLegacyRichTaskRetry(q, source.ID, projectID, plan.report); err != nil {
				return plan, err
			}
		}
		if usedNumbers[projectID] == nil {
			usedNumbers[projectID] = map[int]struct{}{}
			rows, err := q.QueryContext(ctx, `SELECT number FROM tasks WHERE project_id=?`, projectID)
			if err != nil {
				return portableImportPlan{}, err
			}
			for rows.Next() {
				var number int
				if err := rows.Scan(&number); err != nil {
					rows.Close()
					return portableImportPlan{}, err
				}
				usedNumbers[projectID][number] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return portableImportPlan{}, err
			}
			rows.Close()
		}
		targetID, create := source.ID, true
		mappedSource := source
		mappedSource.Checklist = append([]PortableChecklistItem(nil), source.Checklist...)
		for i := range mappedSource.Checklist {
			mappedSource.Checklist[i].CompletedBy, err = portableMapOptionalActor(q, source.Checklist[i].CompletedBy, options.ActorID, &plan.report, actorMap)
			if err != nil {
				return plan, err
			}
		}
		mappedSource.ProjectID, mappedSource.ColumnID = projectID, plan.columnMap[source.ColumnID]
		if source.ReleaseID != nil {
			mappedReleaseID, mapped := plan.releaseMap[*source.ReleaseID]
			if !mapped {
				addPortableIssue(&plan.report, "task", source.ID, "release_id", "destination release is not available")
			} else {
				mappedSource.ReleaseID = &mappedReleaseID
			}
		}
		mappedSource.AssigneeID, err = portableMapOptionalActor(q, source.AssigneeID, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		mappedSource.ClaimedBy, err = portableMapOptionalActor(q, source.ClaimedBy, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		if source.Bug != nil {
			mappedBug := *source.Bug
			mappedBug.ReporterID, err = portableMapActor(q, source.Bug.ReporterID, options.ActorID, &plan.report, actorMap)
			if err != nil {
				return portableImportPlan{}, err
			}
			if mappedBug.ResolvedBy != nil {
				mappedBug.ResolvedBy, err = portableMapOptionalActor(q, mappedBug.ResolvedBy, options.ActorID, &plan.report, actorMap)
				if err != nil {
					return portableImportPlan{}, err
				}
			}
			if mappedBug.DuplicateOf != nil {
				if mapped, ok := plan.taskMap[*mappedBug.DuplicateOf]; ok {
					mappedBug.DuplicateOf = &mapped
				}
			}
			mappedSource.Bug = &mappedBug
		}
		existing, exists, lookupErr := portableExistingTask(q, targetID)
		if lookupErr != nil {
			return portableImportPlan{}, lookupErr
		}
		if exists && existing.ProjectID == projectID && portableTaskFieldsEqual(existing, mappedSource) {
			create = false
			plan.report.Counts.TasksSkipped++
		} else if exists && existing.ProjectID == projectID && portableTaskFieldsEqualIgnoringNumber(existing, mappedSource) {
			// Task numbers are project-local and may have been advanced around
			// existing destination rows. A retry must reuse the same task ID and
			// number remap rather than creating a duplicate task subtree.
			create = false
			plan.report.Counts.TasksSkipped++
			if existing.Number != source.Number {
				portableAddRemap(&plan.report, "task", strconv.Itoa(source.Number), strconv.Itoa(existing.Number), "number", "reused number remap from an earlier import")
			}
			plan.tasks = append(plan.tasks, portableTaskPlan{source: mappedSource, id: targetID, projectID: projectID, columnID: mappedSource.ColumnID, number: existing.Number, assigneeID: mappedSource.AssigneeID, claimedBy: mappedSource.ClaimedBy, claimExpiresAt: mappedSource.ClaimExpiresAt, create: false})
			plan.taskMap[source.ID] = targetID
			continue
		} else {
			if exists && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "task id conflict")
			}
			if exists {
				// Remapped task IDs are deterministic. Search all candidates on
				// retry so an unrelated collision on the first candidate cannot
				// cause a second copy to be inserted.
				candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("task", source.ID, func(id string) (bool, bool, error) {
					candidate, candidateExists, candidateErr := portableExistingTask(q, id)
					if candidateErr != nil {
						return false, false, candidateErr
					}
					return candidateExists, candidateExists && candidate.ProjectID == projectID && portableTaskFieldsEqualIgnoringNumber(candidate, mappedSource), nil
				})
				if lookupErr != nil {
					return portableImportPlan{}, lookupErr
				}
				if candidateMatches {
					candidate, candidateExists, candidateErr := portableExistingTask(q, candidateID)
					if candidateErr != nil {
						return portableImportPlan{}, candidateErr
					}
					if !candidateExists {
						return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
					}
					targetID, create = candidateID, false
					plan.report.Counts.TasksSkipped++
					portableAddRemap(&plan.report, "task", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
					if candidate.Number != source.Number {
						portableAddRemap(&plan.report, "task", strconv.Itoa(source.Number), strconv.Itoa(candidate.Number), "number", "reused number remap from an earlier import")
					}
					plan.tasks = append(plan.tasks, portableTaskPlan{source: mappedSource, id: targetID, projectID: projectID, columnID: mappedSource.ColumnID, number: candidate.Number, assigneeID: mappedSource.AssigneeID, claimedBy: mappedSource.ClaimedBy, claimExpiresAt: mappedSource.ClaimExpiresAt, create: false})
					plan.taskMap[source.ID] = targetID
					continue
				}
				targetID = portableCandidateID("task", source.ID, usedTaskIDs)
			}
			if _, taken := usedTaskIDs[targetID]; taken {
				targetID = portableCandidateID("task", source.ID, usedTaskIDs)
			}
			usedTaskIDs[targetID] = struct{}{}
			if targetID != source.ID {
				portableAddRemap(&plan.report, "task", source.ID, targetID, "id", "source id conflicted with a destination record")
			}
			number := source.Number
			if _, taken := usedNumbers[projectID][number]; taken {
				number = portableNextTaskNumber(usedNumbers[projectID])
				portableAddRemap(&plan.report, "task", strconv.Itoa(source.Number), strconv.Itoa(number), "number", "task number conflicted within the destination project")
			}
			usedNumbers[projectID][number] = struct{}{}
			plan.report.Counts.TasksCreated++
			plan.tasks = append(plan.tasks, portableTaskPlan{source: mappedSource, id: targetID, projectID: projectID, columnID: mappedSource.ColumnID, number: number, assigneeID: mappedSource.AssigneeID, claimedBy: mappedSource.ClaimedBy, claimExpiresAt: mappedSource.ClaimExpiresAt, create: true})
			plan.taskMap[source.ID] = targetID
			continue
		}
		plan.tasks = append(plan.tasks, portableTaskPlan{source: mappedSource, id: targetID, projectID: projectID, columnID: mappedSource.ColumnID, number: source.Number, assigneeID: mappedSource.AssigneeID, claimedBy: mappedSource.ClaimedBy, claimExpiresAt: mappedSource.ClaimExpiresAt, create: create})
		plan.taskMap[source.ID] = targetID
	}
	// Recheck the mapped release/project boundary after destination remapping.
	// Source validation catches malformed cross-project references; this second
	// check protects imports where an existing destination release is reused.
	releaseProjects := make(map[string]string, len(plan.releases))
	for _, release := range plan.releases {
		releaseProjects[release.id] = release.projectID
	}
	for _, task := range plan.tasks {
		if task.source.ReleaseID == nil {
			continue
		}
		releaseProject, ok := releaseProjects[*task.source.ReleaseID]
		if !ok {
			addPortableIssue(&plan.report, "task", task.source.ID, "release_id", "destination release is not available")
		} else if releaseProject != task.projectID {
			addPortableIssue(&plan.report, "task", task.source.ID, "release_id", "destination release belongs to another project")
		}
	}
	if len(plan.report.Errors) > 0 {
		return plan, portableImportError(plan.report, "portable destination validation failed")
	}

	return plan, nil
}
