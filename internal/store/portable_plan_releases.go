package store

import (
	"context"
)

func planPortableReleases(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedReleaseIDs, usedReleaseNames map[string]struct{}, actorMap map[string]string) (portableImportPlan, error) {
	var err error
	// Releases are resolved after projects and before tasks because every task
	// release reference must point at the destination release ID. Stable IDs
	// are reused when the complete row matches. A same-name release is reused
	// only when its complete row matches too; otherwise remap mode creates a
	// deterministic, uniquely named copy so neither release's status or
	// metadata is silently discarded and imported tasks remain compatible with
	// the source release lifecycle.
	for _, source := range archive.Releases {
		projectID := plan.projectMap[source.ProjectID]
		mappedSource := source
		mappedSource.ProjectID = projectID
		mappedSource.ReleasedBy, err = portableMapOptionalActor(q, source.ReleasedBy, options.ActorID, &plan.report, actorMap)
		if err != nil {
			return portableImportPlan{}, err
		}
		targetID, create := source.ID, true
		existing, exists, err := portableExistingRelease(q, targetID)
		if err != nil {
			return portableImportPlan{}, err
		}
		if exists && existing.ProjectID == projectID && portableReleaseFieldsEqual(existing, mappedSource) {
			create = false
			plan.report.Counts.ReleasesSkipped++
		} else {
			named, namedExists, lookupErr := portableExistingNamedRelease(q, projectID, source.Name)
			if lookupErr != nil {
				return portableImportPlan{}, lookupErr
			}
			if namedExists && portableReleaseFieldsEqual(named, mappedSource) {
				// The source ID may differ from a matching destination release ID;
				// retaining the destination row is safe because all release fields
				// (including status) agree.
				targetID, create = named.ID, false
				plan.report.Counts.ReleasesSkipped++
				if targetID != source.ID {
					portableAddRemap(&plan.report, "release", source.ID, targetID, "id", "same project release name already exists with matching fields")
				}
			} else {
				if namedExists && options.Conflict == portableConflictFail {
					return portableImportPlan{}, portableConflictError(plan.report, "release name conflicts with a destination record")
				}

				if namedExists {
					// A same-name, incompatible release cannot be reused: doing so
					// would either lose the source metadata or attach source tasks
					// to a released destination release. First look for the stable
					// remapped copy from an earlier import, then select a fresh
					// deterministic name for a new copy.
					candidateSource := mappedSource
					candidate, candidateMatches, candidateErr := portableFindMatchingReleaseNameCandidate(q, projectID, candidateSource)
					if candidateErr != nil {
						return portableImportPlan{}, candidateErr
					}
					if candidateMatches {
						mappedSource.Name = candidate.Name
						targetID, create = candidate.ID, false
						plan.report.Counts.ReleasesSkipped++
						portableAddRemap(&plan.report, "release", source.ID, targetID, "id", "reused deterministic remap from an earlier incompatible name collision")
						portableAddRemap(&plan.report, "release", source.Name, mappedSource.Name, "name", "reused deterministic remap from an earlier incompatible name collision")
					}
					if !candidateMatches {
						mappedSource.Name = portableUniqueReleaseName(source.Name, projectID, usedReleaseNames)
						portableAddRemap(&plan.report, "release", source.Name, mappedSource.Name, "name", "same project release name conflicted with incompatible destination metadata")
					}
				}

				if create {
					if exists && options.Conflict == portableConflictFail {
						return portableImportPlan{}, portableConflictError(plan.report, "release id conflict")
					}
					if exists {
						candidateID, candidateMatches, candidateErr := portableFindMatchingCandidate("release", source.ID, func(id string) (bool, bool, error) {
							candidate, candidateExists, candidateErr := portableExistingRelease(q, id)
							if candidateErr != nil {
								return false, false, candidateErr
							}
							return candidateExists, candidateExists && candidate.ProjectID == projectID && portableReleaseFieldsEqual(candidate, mappedSource), nil
						})
						if candidateErr != nil {
							return portableImportPlan{}, candidateErr
						}
						if candidateMatches {
							targetID, create = candidateID, false
							plan.report.Counts.ReleasesSkipped++
							portableAddRemap(&plan.report, "release", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
						}
					}
				}
				if create {
					if _, nameTaken := usedReleaseNames[portableProjectReleaseNameKey(projectID, mappedSource.Name)]; nameTaken {
						originalName := mappedSource.Name
						mappedSource.Name = portableUniqueReleaseName(mappedSource.Name, projectID, usedReleaseNames)
						portableAddRemap(&plan.report, "release", originalName, mappedSource.Name, "name", "release name conflicted with another imported release in the destination project")
					}
					if _, taken := usedReleaseIDs[targetID]; taken {
						targetID = portableCandidateID("release", source.ID, usedReleaseIDs)
					}
					usedReleaseIDs[targetID] = struct{}{}
					usedReleaseNames[portableProjectReleaseNameKey(projectID, mappedSource.Name)] = struct{}{}
					if targetID != source.ID {
						portableAddRemap(&plan.report, "release", source.ID, targetID, "id", "source id conflicted with a destination record")
					}
					plan.report.Counts.ReleasesCreated++
				}
			}
		}
		plan.releases = append(plan.releases, portableReleasePlan{source: mappedSource, id: targetID, projectID: projectID, releasedBy: mappedSource.ReleasedBy, create: create})
		plan.releaseMap[source.ID] = targetID
	}
	if len(plan.report.Errors) > 0 {
		return plan, portableImportError(plan.report, "portable destination validation failed")
	}

	return plan, nil
}
