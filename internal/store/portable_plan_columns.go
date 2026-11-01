package store

import (
	"context"
	"fmt"
	"strconv"
)

func planPortableColumns(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedColumnIDs map[string]struct{}) (portableImportPlan, error) {
	// Resolve columns and their unique project positions.
	columnUsedPositions := map[string]map[int]struct{}{}
	for _, source := range archive.Columns {
		projectID := plan.projectMap[source.ProjectID]
		if columnUsedPositions[projectID] == nil {
			columnUsedPositions[projectID] = map[int]struct{}{}
			rows, err := q.QueryContext(ctx, `SELECT position FROM columns WHERE project_id=?`, projectID)
			if err != nil {
				return portableImportPlan{}, err
			}
			for rows.Next() {
				var position int
				if err := rows.Scan(&position); err != nil {
					rows.Close()
					return portableImportPlan{}, err
				}
				columnUsedPositions[projectID][position] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return portableImportPlan{}, err
			}
			rows.Close()
		}
		targetID, create := source.ID, true
		existing, exists, err := portableExistingColumn(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		mappedSource := source
		mappedSource.ProjectID = projectID
		if exists && existing.ProjectID == projectID && portableColumnFieldsEqual(existing, mappedSource) {
			create = false
			plan.report.Counts.ColumnsSkipped++
		} else if exists && existing.ProjectID == projectID && portableColumnFieldsEqualIgnoringPosition(existing, mappedSource) {
			// The source position may have been remapped around an existing
			// destination column on the first import. Reuse the stable source ID
			// and retain the already-imported position on every retry.
			create = false
			plan.report.Counts.ColumnsSkipped++
			if existing.Position != source.Position {
				portableAddRemap(&plan.report, "column", strconv.Itoa(source.Position), strconv.Itoa(existing.Position), "position", "reused position remap from an earlier import")
			}
			plan.columns = append(plan.columns, portableColumnPlan{source: source, id: targetID, projectID: projectID, position: existing.Position, create: false})
			plan.columnMap[source.ID] = targetID
			continue
		} else {
			if exists && options.Conflict == portableConflictFail {
				return portableImportPlan{}, portableConflictError(plan.report, "column id conflict")
			}
			if exists {
				// Search the complete deterministic sequence so a retry reuses
				// whichever suffix was selected when an earlier candidate was
				// already occupied by unrelated destination data.
				candidateID, candidateMatches, lookupErr := portableFindMatchingCandidate("column", source.ID, func(id string) (bool, bool, error) {
					candidate, candidateExists, candidateErr := portableExistingColumn(q, id)
					if candidateErr != nil {
						return false, false, candidateErr
					}
					return candidateExists, candidateExists && candidate.ProjectID == projectID && portableColumnFieldsEqualIgnoringPosition(candidate, mappedSource), nil
				})
				if lookupErr != nil {
					return portableImportPlan{}, lookupErr
				}
				if candidateMatches {
					candidate, candidateExists, candidateErr := portableExistingColumn(q, candidateID)
					if candidateErr != nil {
						return portableImportPlan{}, candidateErr
					}
					if !candidateExists {
						return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
					}
					targetID, create = candidateID, false
					plan.report.Counts.ColumnsSkipped++
					portableAddRemap(&plan.report, "column", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
					if candidate.Position != source.Position {
						portableAddRemap(&plan.report, "column", strconv.Itoa(source.Position), strconv.Itoa(candidate.Position), "position", "reused position remap from an earlier import")
					}
					plan.columns = append(plan.columns, portableColumnPlan{source: source, id: targetID, projectID: projectID, position: candidate.Position, create: false})
					plan.columnMap[source.ID] = targetID
					continue
				}
				targetID = portableCandidateID("column", source.ID, usedColumnIDs)
			}
			if _, taken := usedColumnIDs[targetID]; taken {
				targetID = portableCandidateID("column", source.ID, usedColumnIDs)
			}
			usedColumnIDs[targetID] = struct{}{}
			if targetID != source.ID {
				portableAddRemap(&plan.report, "column", source.ID, targetID, "id", "source id conflicted with a destination record")
			}
			position := source.Position
			if _, taken := columnUsedPositions[projectID][position]; taken {
				position = portableNextColumnPosition(columnUsedPositions[projectID])
				portableAddRemap(&plan.report, "column", strconv.Itoa(source.Position), strconv.Itoa(position), "position", "column position conflicted within the destination project")
			}
			columnUsedPositions[projectID][position] = struct{}{}
			plan.columns = append(plan.columns, portableColumnPlan{source: source, id: targetID, projectID: projectID, position: position, create: true})
			plan.report.Counts.ColumnsCreated++
			plan.columnMap[source.ID] = targetID
			continue
		}
		plan.columns = append(plan.columns, portableColumnPlan{source: source, id: targetID, projectID: projectID, position: source.Position, create: create})
		plan.columnMap[source.ID] = targetID
	}

	return plan, nil
}
