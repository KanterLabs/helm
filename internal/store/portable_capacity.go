package store

import "context"

// checkPortableActiveCapacity distinguishes cards added to an existing board
// from the restored contents of a newly created project. Only additive
// imports are subject to the capacity cap. Neither format imports admission
// records or changes a task or claim that already exists on the destination.
func checkPortableActiveCapacity(ctx context.Context, q portableSQL, plan portableImportPlan) error {
	activeColumns := make(map[string]bool)
	for _, column := range plan.columns {
		if column.source.SemanticState != "active" {
			continue
		}
		live := true
		if !column.create {
			if err := q.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM columns WHERE id=?`, column.id).Scan(&live); err != nil {
				return err
			}
		}
		activeColumns[column.id] = live
	}
	added := make(map[string]int)
	for _, task := range plan.tasks {
		if task.create && activeColumns[task.columnID] {
			added[task.projectID]++
		}
	}
	if len(added) == 0 {
		return nil
	}

	existingProjects := make(map[string]bool)
	liveProjects := make(map[string]bool)
	for _, project := range plan.projects {
		if added[project.id] == 0 {
			continue
		}
		live := project.source.ArchivedAt == nil
		if !project.create {
			if err := checkProjectActiveCapacityTx(ctx, q, project.id, added[project.id]); err != nil {
				return err
			}
			if err := q.QueryRowContext(ctx, `SELECT archived_at IS NULL FROM projects WHERE id=?`, project.id).Scan(&live); err != nil {
				return err
			}
			existingProjects[project.id] = true
		}
		liveProjects[project.id] = live
	}
	if err := requireLegacyActivePlacementTx(ctx, q, plan.options.ActorID); err != nil {
		return err
	}

	claimedInExistingProject := make(map[string]bool)
	for _, task := range plan.tasks {
		if !task.create || !activeColumns[task.columnID] || !liveProjects[task.projectID] || task.claimedBy == nil || task.claimExpiresAt == nil {
			continue
		}
		var activeAgentClaim bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM actors WHERE id=? AND kind='agent'
			AND julianday(?)>julianday('now'))`, *task.claimedBy, *task.claimExpiresAt).Scan(&activeAgentClaim); err != nil {
			return err
		}
		if !activeAgentClaim {
			continue
		}
		existing := existingProjects[task.projectID]
		otherExisting, seen := claimedInExistingProject[*task.claimedBy]
		if seen && (existing || otherExisting) {
			return checkAgentAlreadyActive(true)
		}
		claimedInExistingProject[*task.claimedBy] = existing
		if existing {
			if err := checkAgentActiveTaskTx(ctx, q, *task.claimedBy, task.id); err != nil {
				return err
			}
		}
	}
	return nil
}
