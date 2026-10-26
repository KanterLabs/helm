package store

import (
	"context"
	"fmt"
	"strings"
)

func planPortableProjects(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions, plan portableImportPlan, usedProjectIDs, usedProjectKeys, usedProjectSlugs map[string]struct{}) (portableImportPlan, error) {
	var err error
	// Project IDs are resolved first because every downstream relation is
	// project-scoped.  Existing exact matches are reused only when their
	// immutable export fields agree; no project is ever overwritten.
	for _, source := range archive.Projects {
		originalKey, originalSlug := source.Key, source.Slug
		targetID := source.ID
		create := true
		if options.TargetProjectID != "" {
			targetID = options.TargetProjectID
			create = false
			if targetID != source.ID {
				portableAddRemap(&plan.report, "project", source.ID, targetID, "id", "import target project selected by caller")
			}
			plan.report.Counts.ProjectsSkipped++
			var existing PortableProject
			var exists bool
			existing, exists, err = portableExistingProject(q, targetID)
			if err != nil {
				return portableImportPlan{}, err
			}
			if !exists {
				addPortableIssue(&plan.report, "import", targetID, "target_project", "target project was not found")
			}
			if exists && !portableProjectMetadataEqual(existing, source) {
				plan.report.Warnings = append(plan.report.Warnings, "target project metadata was retained; import never overwrites an existing project")
			}
		} else {
			existing, exists, lookupErr := portableExistingProject(q, source.ID)
			if lookupErr != nil {
				return portableImportPlan{}, lookupErr
			}
			if exists && portableProjectFieldsEqual(existing, source) {
				create = false
				plan.report.Counts.ProjectsSkipped++
			} else if exists && portableProjectMetadataEqual(existing, source) {
				// A previous import can keep the stable project ID while remapping
				// only its globally unique key and/or slug. Treat that as the same
				// imported subtree on retry instead of creating another project.
				create = false
				plan.report.Counts.ProjectsSkipped++
				if existing.Key != source.Key {
					portableAddRemap(&plan.report, "project", source.Key, existing.Key, "key", "reused key remap from an earlier import")
				}
				if existing.Slug != source.Slug {
					portableAddRemap(&plan.report, "project", source.Slug, existing.Slug, "slug", "reused slug remap from an earlier import")
				}
			} else {
				if exists && options.Conflict == portableConflictFail {
					return portableImportPlan{}, portableConflictError(plan.report, "project id conflict")
				}
				candidateID, candidateMatches := "", false
				if exists {
					// A deterministic remapped ID lets a retry discover the same
					// project even when an earlier candidate was already occupied.
					candidateID, candidateMatches, err = portableFindMatchingCandidate("project", source.ID, func(id string) (bool, bool, error) {
						candidate, candidateExists, lookupErr := portableExistingProject(q, id)
						if lookupErr != nil {
							return false, false, lookupErr
						}
						return candidateExists, candidateExists && portableProjectMetadataEqual(candidate, source), nil
					})
					if err != nil {
						return portableImportPlan{}, err
					}
				}
				if candidateMatches {
					candidate, candidateExists, lookupErr := portableExistingProject(q, candidateID)
					if lookupErr != nil {
						return portableImportPlan{}, lookupErr
					}
					if !candidateExists {
						return portableImportPlan{}, fmt.Errorf("portable remap candidate disappeared")
					}
					targetID, create = candidateID, false
					plan.report.Counts.ProjectsSkipped++
					portableAddRemap(&plan.report, "project", source.ID, targetID, "id", "reused deterministic remap from an earlier import")
					if candidate.Key != source.Key {
						portableAddRemap(&plan.report, "project", originalKey, candidate.Key, "key", "reused key remap from an earlier import")
					}
					if candidate.Slug != source.Slug {
						portableAddRemap(&plan.report, "project", originalSlug, candidate.Slug, "slug", "reused slug remap from an earlier import")
					}
				} else {
					targetID = source.ID
					if exists {
						targetID = portableCandidateID("project", source.ID, usedProjectIDs)
					}
					if _, taken := usedProjectIDs[targetID]; taken {
						targetID = portableCandidateID("project", source.ID, usedProjectIDs)
					}
					usedProjectIDs[targetID] = struct{}{}
					if targetID != source.ID {
						portableAddRemap(&plan.report, "project", source.ID, targetID, "id", "source id conflicted with a destination record")
					}
					if _, taken := usedProjectKeys[strings.ToUpper(source.Key)]; taken {
						source.Key = portableUniqueKey(source.Key, source.ID, usedProjectKeys)
						portableAddRemap(&plan.report, "project", originalKey, source.Key, "key", "project key conflicted with a destination record")
					}
					if _, taken := usedProjectSlugs[strings.ToLower(source.Slug)]; taken {
						source.Slug = portableUniqueSlug(source.Slug, source.ID, usedProjectSlugs)
						portableAddRemap(&plan.report, "project", originalSlug, source.Slug, "slug", "project slug conflicted with a destination record")
					}
					usedProjectKeys[strings.ToUpper(source.Key)] = struct{}{}
					usedProjectSlugs[strings.ToLower(source.Slug)] = struct{}{}
					plan.report.Counts.ProjectsCreated++
				}
			}
		}
		plan.projectMap[source.ID] = targetID
		plan.projects = append(plan.projects, portableProjectPlan{source: source, id: targetID, key: source.Key, slug: source.Slug, create: create})
	}
	if len(plan.report.Errors) > 0 {
		return plan, portableImportError(plan.report, "portable destination validation failed")
	}

	return plan, nil
}
