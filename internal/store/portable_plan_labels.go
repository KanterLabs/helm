package store

import (
	"context"
	"fmt"
)

func planPortableLabels(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedLabelIDs map[string]struct{}) (portableImportPlan, error) {
	// Labels are reused by stable IDs or (for a separately created label with
	// the same name) by project/name.  Both mappings are explicitly reported.
	for _, source := range archive.Labels {
		projectID := plan.projectMap[source.ProjectID]
		targetID, create := source.ID, true
		existing, exists, err := portableExistingLabel(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		mappedSource := source
		mappedSource.ProjectID = projectID
		if exists && existing.ProjectID == projectID && portableLabelFieldsEqual(existing, mappedSource) {
			create = false
			plan.report.Counts.LabelsSkipped++
		} else if named, namedExists, lookupErr := portableExistingNamedLabel(q, projectID, source.Name); lookupErr != nil {
			return portableImportPlan{}, lookupErr
		} else if namedExists {
			if options.Conflict == portableConflictFail && !portableLabelFieldsEqual(named, mappedSource) {
				return portableImportPlan{}, portableConflictError(plan.report, "label name conflicts with a destination record")
			}
			targetID, create = named.ID, false
			plan.report.Counts.LabelsSkipped++
			if targetID != source.ID {
				portableAddRemap(&plan.report, "label", source.ID, targetID, "id", "same project label name already exists")
			}
			if !portableLabelFieldsEqual(named, mappedSource) {
				plan.report.Warnings = append(plan.report.Warnings, fmt.Sprintf("label %s was retained because the destination already has the same name", source.ID))
			}
		} else {
			if exists && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "label id conflict")
			}
			if exists {
				targetID = portableCandidateID("label", source.ID, usedLabelIDs)
			}
			// Reuse the first deterministic remap on retries when its fields
			// still match the source label.
			if exists {
				candidateID := portableStableID("label", source.ID)
				if candidateID != source.ID {
					candidate, candidateExists, lookupErr := portableExistingLabel(q, candidateID)
					if lookupErr != nil {
						return portableImportPlan{}, lookupErr
					}
					if candidateExists && candidate.ProjectID == projectID && portableLabelFieldsEqual(candidate, mappedSource) {
						targetID, create = candidateID, false
						plan.report.Counts.LabelsSkipped++
						portableAddRemap(&plan.report, "label", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
						plan.labels = append(plan.labels, portableLabelPlan{source: source, id: targetID, projectID: projectID, create: false})
						plan.labelMap[source.ID] = targetID
						continue
					}
				}
			}
			if _, taken := usedLabelIDs[targetID]; taken {
				targetID = portableCandidateID("label", source.ID, usedLabelIDs)
			}
			usedLabelIDs[targetID] = struct{}{}
			if targetID != source.ID {
				portableAddRemap(&plan.report, "label", source.ID, targetID, "id", "source id conflicted with a destination record")
			}
			plan.report.Counts.LabelsCreated++
		}
		plan.labels = append(plan.labels, portableLabelPlan{source: source, id: targetID, projectID: projectID, create: create})
		plan.labelMap[source.ID] = targetID
	}

	return plan, nil
}
