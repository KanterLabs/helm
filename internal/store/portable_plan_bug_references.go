package store

import (
	"context"
	"fmt"
	"strconv"
)

func planPortableBugReferences(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan) (portableImportPlan, error) {
	// A bug can refer forward to another task, so remap duplicate_of after the
	// complete task map exists.
	taskIDRemaps := map[string]string{}
	for index := range plan.tasks {
		if plan.tasks[index].source.Bug == nil || plan.tasks[index].source.Bug.DuplicateOf == nil {
			continue
		}
		if mapped, ok := plan.taskMap[*plan.tasks[index].source.Bug.DuplicateOf]; ok {
			plan.tasks[index].source.Bug.DuplicateOf = &mapped
		}
	}
	// Candidate matching above happens while task IDs are being resolved. A
	// forward duplicate_of reference is not mapped until the complete task map
	// exists, so retry matching those bug tasks once more after the remap pass.
	// This prevents a deterministic retry from inserting a second bug when its
	// duplicate target was itself remapped.
	for changed := true; changed; {
		changed = false
		for index := range plan.tasks {
			if !plan.tasks[index].create || plan.tasks[index].source.Bug == nil || plan.tasks[index].source.Bug.DuplicateOf == nil {
				continue
			}
			sourceID := plan.tasks[index].source.ID
			candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("task", sourceID, func(id string) (bool, bool, error) {
				candidate, candidateExists, candidateErr := portableExistingTask(q, id)
				if candidateErr != nil {
					return false, false, candidateErr
				}
				return candidateExists, candidateExists && candidate.ProjectID == plan.tasks[index].projectID && portableTaskFieldsEqualIgnoringNumber(candidate, plan.tasks[index].source), nil
			})
			if lookupErr != nil {
				return portableImportPlan{}, lookupErr
			}
			if !candidateMatches {
				continue
			}
			candidate, candidateExists, candidateErr := portableExistingTask(q, candidateID)
			if candidateErr != nil {
				return portableImportPlan{}, candidateErr
			}
			if !candidateExists {
				return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
			}
			oldID := plan.tasks[index].id
			plan.tasks[index].id, plan.tasks[index].number, plan.tasks[index].create = candidateID, candidate.Number, false
			taskIDRemaps[oldID] = candidateID
			plan.taskMap[sourceID] = candidateID
			plan.report.Counts.TasksCreated--
			plan.report.Counts.TasksSkipped++
			portableAddRemap(&plan.report, "task", sourceID, candidateID, "id", "reused deterministic remap after relationship remapping")
			if candidate.Number != plan.tasks[index].source.Number {
				portableAddRemap(&plan.report, "task", strconv.Itoa(plan.tasks[index].source.Number), strconv.Itoa(candidate.Number), "number", "reused number remap after relationship remapping")
			}
			changed = true
		}
		for index := range plan.tasks {
			if plan.tasks[index].source.Bug == nil || plan.tasks[index].source.Bug.DuplicateOf == nil {
				continue
			}
			for {
				mapped, ok := taskIDRemaps[*plan.tasks[index].source.Bug.DuplicateOf]
				if !ok || mapped == *plan.tasks[index].source.Bug.DuplicateOf {
					break
				}
				plan.tasks[index].source.Bug.DuplicateOf = &mapped
			}
		}
	}

	return plan, nil
}
