package store

import (
	"context"
	"fmt"
)

func planPortableEvents(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedEventIDs map[string]struct{}, actorMap map[string]string) (portableImportPlan, error) {
	var err error
	for _, source := range archive.Activity.Events {
		targetID := source.ID
		mapped := source
		if source.ActorID != nil {
			mapped.ActorID, err = portableMapOptionalActor(q, source.ActorID, options.ActorID, &plan.report, actorMap)
			if err != nil {
				return portableImportPlan{}, err
			}
		}
		if source.ProjectID != nil {
			target := plan.projectMap[*source.ProjectID]
			mapped.ProjectID = &target
		}
		if source.TaskID != nil {
			target := plan.taskMap[*source.TaskID]
			mapped.TaskID = &target
			if mapped.ProjectID == nil {
				if task, ok := archiveTaskByID(archive.Tasks, *source.TaskID); ok {
					project := plan.projectMap[task.ProjectID]
					mapped.ProjectID = &project
				}
			}
		}
		mapped.Payload, err = portableRemapEventPayload(q, source.Payload, source.Type, options.ActorID, &plan.report, actorMap, portableEventPayloadMaps{
			projects:  plan.projectMap,
			artifacts: plan.github.artifactMap,
			audits:    plan.github.runMap,
			findings:  plan.github.findingMap,
			releases:  plan.releaseMap,
			tasks:     plan.taskMap,
			actors:    actorMap,
			comments:  plan.commentMap,
			columns:   plan.columnMap,
			labels:    plan.labelMap,
		})
		if err != nil {
			return portableImportPlan{}, err
		}
		existing, exists, err := portableExistingEvent(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		create := true
		if exists && portableEventFieldsEqual(existing, mapped) {
			create = false
			plan.report.Counts.EventsSkipped++
			if source.Cursor > 0 {
				plan.eventCursorMap[source.Cursor] = existing.Cursor
			}
		} else {
			if exists && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "event id conflict")
			}
			if exists {
				// Event IDs are stable across installations. Reuse a matching
				// deterministic candidate on retries, even when an earlier
				// candidate was occupied by unrelated destination data.
				candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("event", source.ID, func(id string) (bool, bool, error) {
					candidate, candidateExists, candidateErr := portableExistingEvent(q, id)
					if candidateErr != nil {
						return false, false, candidateErr
					}
					return candidateExists, candidateExists && portableEventFieldsEqualIgnoringID(candidate, mapped), nil
				})
				if lookupErr != nil {
					return portableImportPlan{}, lookupErr
				}
				if candidateMatches {
					candidate, candidateExists, candidateErr := portableExistingEvent(q, candidateID)
					if candidateErr != nil {
						return portableImportPlan{}, candidateErr
					}
					if !candidateExists {
						return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
					}
					targetID, create = candidateID, false
					plan.report.Counts.EventsSkipped++
					portableAddRemap(&plan.report, "event", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
					if source.Cursor > 0 {
						plan.eventCursorMap[source.Cursor] = candidate.Cursor
					}
					plan.events = append(plan.events, portableEventPlan{source: mapped, id: targetID, actorID: mapped.ActorID, projectID: mapped.ProjectID, taskID: mapped.TaskID, create: false})
					continue
				}
				targetID = portableCandidateID("event", source.ID, usedEventIDs)
			}
			if _, taken := usedEventIDs[targetID]; taken {
				targetID = portableCandidateID("event", source.ID, usedEventIDs)
			}
			usedEventIDs[targetID] = struct{}{}
			if targetID != source.ID {
				portableAddRemap(&plan.report, "event", source.ID, targetID, "id", "source id conflicted with a destination record")
			}
			plan.report.Counts.EventsCreated++
		}
		plan.events = append(plan.events, portableEventPlan{source: mapped, id: targetID, actorID: mapped.ActorID, projectID: mapped.ProjectID, taskID: mapped.TaskID, create: create})
	}
	return plan, nil
}
