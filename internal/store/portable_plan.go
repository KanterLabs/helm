package store

import (
	"context"
	"strings"
)

// buildPortableImportPlan performs every structural and destination conflict
// check through q.  It is called inside the eventual write transaction, so a
// concurrent writer cannot invalidate a successful validation between the
// final read and the first insert.
func buildPortableImportPlan(ctx context.Context, q portableSQL, archive PortableArchive, options PortableImportOptions) (portableImportPlan, error) {
	archive.normalize()
	report := PortableImportReport{Format: archive.Format, Version: archive.Version, DryRun: options.DryRun, Conflict: options.Conflict, Remaps: []PortableImportRemap{}, Warnings: []string{}, Errors: []PortableImportIssue{}}
	if options.Conflict == "" {
		options.Conflict = portableConflictRemap
		report.Conflict = options.Conflict
	}
	if options.Conflict != portableConflictRemap && options.Conflict != portableConflictFail {
		addPortableIssue(&report, "import", "", "conflict", "conflict must be remap or fail")
	}
	validatePortableArchive(&archive, &report)
	if len(archive.Projects) > 1 && strings.TrimSpace(options.TargetProjectID) != "" {
		addPortableIssue(&report, "import", options.TargetProjectID, "target_project", "target_project can only be used with a one-project archive")
	}
	if strings.TrimSpace(options.ActorID) == "" {
		addPortableIssue(&report, "import", "", "actor", "an importing actor is required")
	} else {
		var exists int
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM actors WHERE id=?)`, options.ActorID).Scan(&exists); err != nil {
			return portableImportPlan{}, err
		}
		if exists == 0 {
			addPortableIssue(&report, "import", options.ActorID, "actor", "importing actor was not found")
		}
	}
	if len(report.Errors) > 0 {
		return portableImportPlan{archive: archive, report: report, options: options}, portableImportError(report, "portable archive validation failed")
	}

	plan := portableImportPlan{
		archive: archive, report: report, options: options,
		projects: []portableProjectPlan{}, columns: []portableColumnPlan{}, releases: []portableReleasePlan{}, labels: []portableLabelPlan{}, tasks: []portableTaskPlan{},
		taskLabels: []PortableTaskLabel{}, dependencies: []portableDependencyPlan{}, links: []portableLinkPlan{}, comments: []portableCommentPlan{}, agentNotes: []portableAgentNotePlan{}, events: []portableEventPlan{}, work: []portableWorkPlan{}, history: []portableHistoryPlan{},
		projectMap: map[string]string{}, columnMap: map[string]string{}, releaseMap: map[string]string{}, taskMap: map[string]string{}, labelMap: map[string]string{}, commentMap: map[string]string{}, eventCursorMap: map[int64]int64{},
	}
	usedProjectIDs, err := portableIDSet(q, "projects")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedColumnIDs, err := portableIDSet(q, "columns")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedReleaseIDs, err := portableIDSet(q, "releases")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedReleaseNames := map[string]struct{}{}
	releaseNameRows, err := q.QueryContext(ctx, `SELECT project_id,lower(name) FROM releases`)
	if err != nil {
		return portableImportPlan{}, err
	}
	for releaseNameRows.Next() {
		var projectID, name string
		if err := releaseNameRows.Scan(&projectID, &name); err != nil {
			releaseNameRows.Close()
			return portableImportPlan{}, err
		}
		usedReleaseNames[portableProjectReleaseNameKey(projectID, name)] = struct{}{}
	}
	if err := releaseNameRows.Err(); err != nil {
		releaseNameRows.Close()
		return portableImportPlan{}, err
	}
	releaseNameRows.Close()
	usedLabelIDs, err := portableIDSet(q, "labels")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedTaskIDs, err := portableIDSet(q, "tasks")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedCommentIDs, err := portableIDSet(q, "comments")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedAgentNoteIDs, err := portableIDSet(q, "agent_notes")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedEventIDs, err := portableIDSet(q, "events")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedHistoryIDs, err := portableIDSet(q, "task_agent_work_history")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedProjectKeys, err := portableIDSet(q, "projects")
	if err != nil {
		return portableImportPlan{}, err
	}
	usedProjectSlugs := map[string]struct{}{}
	rows, err := q.QueryContext(ctx, `SELECT lower(slug) FROM projects`)
	if err != nil {
		return portableImportPlan{}, err
	}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			rows.Close()
			return portableImportPlan{}, err
		}
		usedProjectSlugs[slug] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return portableImportPlan{}, err
	}
	rows.Close()
	// The project key set is case-normalized because the API treats keys as
	// uppercase identifiers while SQLite's UNIQUE constraint is byte-sensitive.
	rows, err = q.QueryContext(ctx, `SELECT upper(key) FROM projects`)
	if err != nil {
		return portableImportPlan{}, err
	}
	usedProjectKeys = map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return portableImportPlan{}, err
		}
		usedProjectKeys[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return portableImportPlan{}, err
	}
	rows.Close()

	actorMap := map[string]string{}
	dependencyCycleConflict := false
	plan, err = planPortableProjects(ctx, q, archive, options, plan, usedProjectIDs, usedProjectKeys, usedProjectSlugs)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableColumns(ctx, q, archive, options, plan, usedColumnIDs)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableReleases(ctx, q, archive, options, plan, usedReleaseIDs, usedReleaseNames, actorMap)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableLabels(ctx, q, archive, options, plan, usedLabelIDs)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableTasks(ctx, q, archive, options, plan, usedTaskIDs, actorMap)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableBugReferences(ctx, q, archive, options, plan)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableRelationships(ctx, q, archive, options, plan, actorMap, &dependencyCycleConflict)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableComments(ctx, q, archive, options, plan, usedCommentIDs, usedAgentNoteIDs, actorMap)
	if err != nil {
		return plan, err
	}
	if err := planPortableGithub(ctx, q, &plan, actorMap); err != nil {
		return plan, err
	}
	plan, err = planPortableEvents(ctx, q, archive, options, plan, usedEventIDs, actorMap)
	if err != nil {
		return plan, err
	}
	plan, err = planPortableWork(ctx, q, archive, options, plan, usedHistoryIDs, actorMap)
	if err != nil {
		return plan, err
	}
	if err := portableValidateActivityPlan(ctx, q, &plan); err != nil {
		return portableImportPlan{}, err
	}
	if dependencyCycleConflict {
		return plan, portableConflictError(plan.report, "portable dependency validation found a destination cycle")
	}
	if len(plan.report.Errors) > 0 {
		return plan, portableImportError(plan.report, "portable destination validation failed")
	}
	return plan, nil
}
