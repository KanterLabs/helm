package store

import (
	"context"
	"fmt"
)

func planPortableWork(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedHistoryIDs map[string]struct{}, actorMap map[string]string) (portableImportPlan, error) {
	for _, source := range archive.Activity.AgentWork {
		taskID := plan.taskMap[source.TaskID]
		actorID, err := portableMapActor(q, source.ActorID, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		mappedWork := source
		mappedWork.TaskID, mappedWork.ActorID = taskID, actorID
		existingWork, exists, err := portableExistingWork(q, taskID)
		if err != nil {
			return portableImportPlan{}, err
		}
		create := !exists
		if exists && portableWorkFieldsEqual(existingWork, mappedWork) {
			plan.report.Counts.AgentWorkSkipped++
		} else if exists {
			if options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "agent work snapshot conflicts with a destination record")
			}
			plan.report.Warnings = append(plan.report.Warnings, fmt.Sprintf("agent work snapshot for task %s was retained because the destination already has a different snapshot", taskID))
			plan.report.Counts.AgentWorkSkipped++
		} else {
			plan.report.Counts.AgentWorkCreated++
		}
		plan.work = append(plan.work, portableWorkPlan{source: source, taskID: taskID, actorID: actorID, create: create})
	}
	for _, source := range archive.Activity.AgentWorkHistory {
		taskID := plan.taskMap[source.TaskID]
		actorID, err := portableMapActor(q, source.ActorID, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		generatedCommentID := source.GeneratedCommentID
		if generatedCommentID != nil {
			mapped := plan.commentMap[*generatedCommentID]
			if mapped != "" {
				generatedCommentID = &mapped
			}
		}
		mappedHistory := source
		mappedHistory.TaskID, mappedHistory.ActorID = taskID, actorID
		mappedHistory.GeneratedCommentID = generatedCommentID
		if source.ProgressEventCursor != nil {
			if mappedCursor, ok := plan.eventCursorMap[*source.ProgressEventCursor]; ok {
				mappedHistory.ProgressEventCursor = &mappedCursor
			}
		}
		targetID, create := source.ID, true
		existingHistory, existingHistoryOK, err := portableExistingHistory(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		mappedHistory.ID = targetID
		if existingHistoryOK && portableHistoryFieldsEqual(existingHistory, mappedHistory) {
			create = false
			plan.report.Counts.HistorySkipped++
		} else {
			if existingHistoryOK && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "agent work history id conflict")
			}
			if existingHistoryOK {
				candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("agent_work_history", source.ID, func(id string) (bool, bool, error) {
					candidate, candidateExists, candidateErr := portableExistingHistory(q, id)
					if candidateErr != nil {
						return false, false, candidateErr
					}
					return candidateExists, candidateExists && portableHistoryFieldsEqualIgnoringID(candidate, mappedHistory), nil
				})
				if lookupErr != nil {
					return portableImportPlan{}, lookupErr
				}
				if candidateMatches {
					_, candidateExists, candidateErr := portableExistingHistory(q, candidateID)
					if candidateErr != nil {
						return portableImportPlan{}, candidateErr
					}
					if !candidateExists {
						return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
					}
					targetID, create = candidateID, false
					plan.report.Counts.HistorySkipped++
					portableAddRemap(&plan.report, "agent_work_history", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
				}
			}
			if create {
				if existingHistoryOK {
					targetID = portableCandidateID("agent_work_history", source.ID, usedHistoryIDs)
				}
				if _, taken := usedHistoryIDs[targetID]; taken {
					targetID = portableCandidateID("agent_work_history", source.ID, usedHistoryIDs)
				}
				usedHistoryIDs[targetID] = struct{}{}
				if targetID != source.ID {
					portableAddRemap(&plan.report, "agent_work_history", source.ID, targetID, "id", "source id conflicted with a destination record")
				}
				plan.report.Counts.HistoryCreated++
			}
		}
		mappedHistory.ID = targetID
		plan.history = append(plan.history, portableHistoryPlan{source: source, id: targetID, taskID: taskID, actorID: actorID, generatedCommentID: generatedCommentID, create: create})
	}
	return plan, nil
}
