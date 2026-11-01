package store

import (
	"context"
)

func planPortableComments(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedCommentIDs, usedAgentNoteIDs map[string]struct{}, actorMap map[string]string) (portableImportPlan, error) {
	for _, source := range archive.Comments {
		taskID := plan.taskMap[source.TaskID]
		actorID, err := portableMapActor(q, source.ActorID, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		targetID, create := source.ID, true
		mapped := source
		mapped.TaskID, mapped.ActorID = taskID, actorID
		existing, exists, err := portableExistingComment(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if exists && portableCommentFieldsEqual(existing, mapped) {
			create = false
			plan.report.Counts.CommentsSkipped++
		} else {
			if exists && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "comment id conflict")
			}
			if exists {
				// Keep retries idempotent when the source comment ID was remapped
				// during an earlier import, including when its first candidate was
				// already occupied by unrelated destination data.
				candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("comment", source.ID, func(id string) (bool, bool, error) {
					candidate, candidateExists, candidateErr := portableExistingComment(q, id)
					if candidateErr != nil {
						return false, false, candidateErr
					}
					return candidateExists, candidateExists && portableCommentFieldsEqual(candidate, mapped), nil
				})
				if lookupErr != nil {
					return portableImportPlan{}, lookupErr
				}
				if candidateMatches {
					targetID, create = candidateID, false
					plan.report.Counts.CommentsSkipped++
					portableAddRemap(&plan.report, "comment", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
					plan.comments = append(plan.comments, portableCommentPlan{source: mapped, id: targetID, taskID: taskID, actorID: actorID, create: false})
					plan.commentMap[source.ID] = targetID
					continue
				}
				targetID = portableCandidateID("comment", source.ID, usedCommentIDs)
			}
			if _, taken := usedCommentIDs[targetID]; taken {
				targetID = portableCandidateID("comment", source.ID, usedCommentIDs)
			}
			usedCommentIDs[targetID] = struct{}{}
			if targetID != source.ID {
				portableAddRemap(&plan.report, "comment", source.ID, targetID, "id", "source id conflicted with a destination record")
			}
			plan.report.Counts.CommentsCreated++
		}
		plan.comments = append(plan.comments, portableCommentPlan{source: mapped, id: targetID, taskID: taskID, actorID: actorID, create: create})
		plan.commentMap[source.ID] = targetID
	}
	for _, source := range archive.AgentNotes {
		taskID := plan.taskMap[source.TaskID]
		actorID, err := portableMapActor(q, source.ActorID, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		targetID, create := source.ID, true
		mapped := source
		mapped.TaskID, mapped.ActorID = taskID, actorID
		existing, exists, err := portableExistingAgentNote(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if exists && portableAgentNoteFieldsEqual(existing, mapped) {
			create = false
			plan.report.Counts.AgentNotesSkipped++
		} else if exists {
			if options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "agent note id conflict")
			}
			candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("agent-note", source.ID, func(id string) (bool, bool, error) {
				candidate, candidateExists, candidateErr := portableExistingAgentNote(q, id)
				return candidateExists, candidateExists && portableAgentNoteFieldsEqual(candidate, mapped), candidateErr
			})
			if lookupErr != nil {
				return portableImportPlan{}, lookupErr
			}
			if candidateMatches {
				targetID, create = candidateID, false
				plan.report.Counts.AgentNotesSkipped++
				portableAddRemap(&plan.report, "agent_note", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
			} else {
				targetID = portableCandidateID("agent-note", source.ID, usedAgentNoteIDs)
				usedAgentNoteIDs[targetID] = struct{}{}
				portableAddRemap(&plan.report, "agent_note", source.ID, targetID, "id", "source id conflicted with a destination record")
				plan.report.Counts.AgentNotesCreated++
			}
		} else {
			usedAgentNoteIDs[targetID] = struct{}{}
			plan.report.Counts.AgentNotesCreated++
		}
		plan.agentNotes = append(plan.agentNotes, portableAgentNotePlan{source: mapped, id: targetID, taskID: taskID, actorID: actorID, create: create})
	}

	return plan, nil
}
