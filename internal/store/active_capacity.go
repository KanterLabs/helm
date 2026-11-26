package store

import (
	"context"
	"errors"
)

const ProjectActiveTaskLimit = 5

const activeCardPredicate = "t.deleted_at IS NULL AND c.archived_at IS NULL AND c.semantic_state = 'active'"

var (
	ErrProjectCapacityReached = errors.New("project_capacity_reached")
	ErrAgentAlreadyActive     = errors.New("agent_already_active")
	ErrAgentAdmissionRequired = errors.New("agent_admission_required")
)

func projectActiveTaskCountTx(ctx context.Context, tx dependencySQL, projectID string) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM columns c
		JOIN tasks t ON t.project_id=c.project_id AND t.column_id=c.id
		WHERE c.project_id=? AND `+activeCardPredicate, projectID).Scan(&count)
	return count, err
}

// checkProjectActiveCapacityTx checks a positive delta before inserting or
// promoting a card. The caller must hold the SQLite writer lock.
func checkProjectActiveCapacityTx(ctx context.Context, tx dependencySQL, projectID string, added int) error {
	if added <= 0 {
		return nil
	}
	count, err := projectActiveTaskCountTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	return checkProjectActiveTaskDelta(projectID, count, added)
}

// checkAddedActiveCardsTx runs after a guarded write when that write obtained
// the writer lock. Rejection rolls back the card change and all side effects.
func checkAddedActiveCardsTx(ctx context.Context, tx dependencySQL, projectID string, added int) error {
	if added <= 0 {
		return nil
	}
	count, err := projectActiveTaskCountTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	return checkProjectActiveTaskDelta(projectID, count-added, added)
}

func checkProjectActiveTaskDelta(projectID string, count, added int) error {
	if count+added <= ProjectActiveTaskLimit {
		return nil
	}
	return &Error{
		Kind:    errors.Join(ErrProjectCapacityReached, ErrConflict),
		Message: "project has reached its active task limit",
		Details: map[string]any{"project_id": projectID, "active_count": count, "limit": ProjectActiveTaskLimit},
	}
}

// checkAgentActiveTaskTx checks the authenticated agent across projects. A
// human actor has no agent-specific lease limit. The target is excluded so a
// single card can be resumed or taken over without counting twice.
func checkAgentActiveTaskTx(ctx context.Context, tx dependencySQL, actorID, taskID string) error {
	var alreadyActive bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM actors a
		JOIN tasks t ON t.claimed_by=a.id
		JOIN columns c ON c.id=t.column_id
		JOIN projects p ON p.id=t.project_id
		WHERE a.id=? AND a.kind='agent' AND t.id<>?
		  AND p.archived_at IS NULL
		  AND julianday(t.claim_expires_at)>julianday('now')
		  AND `+activeCardPredicate+`
	)`, actorID, taskID).Scan(&alreadyActive)
	if err != nil {
		return err
	}
	return checkAgentAlreadyActive(alreadyActive)
}

func checkAgentAlreadyActive(alreadyActive bool) error {
	if !alreadyActive {
		return nil
	}
	// The other active task can be in a project the caller cannot read.
	return &Error{
		Kind:    errors.Join(ErrAgentAlreadyActive, ErrConflict),
		Message: "agent already has an active task",
	}
}

func agentAdmissionRequired() error {
	return &Error{
		Kind:    errors.Join(ErrAgentAdmissionRequired, ErrConflict),
		Message: "agents must use agent work admission to start or resume active work",
	}
}

func requireLegacyActivePlacementTx(ctx context.Context, tx dependencySQL, actorID string) error {
	var agent bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM actors WHERE id=? AND kind='agent')`, actorID).Scan(&agent); err != nil {
		return err
	}
	if agent {
		return agentAdmissionRequired()
	}
	return nil
}

// checkTaskEnteringActiveTx handles moves that retain a claim, including
// administrator moves and claims a legacy caller made before starting work.
func checkTaskEnteringActiveTx(ctx context.Context, tx dependencySQL, taskID, projectID, actorID, sourceState, destinationState string) error {
	if sourceState == "active" || destinationState != "active" {
		return nil
	}
	if err := checkAddedActiveCardsTx(ctx, tx, projectID, 1); err != nil {
		return err
	}
	if err := requireLegacyActivePlacementTx(ctx, tx, actorID); err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN p.archived_at IS NULL AND julianday(t.claim_expires_at)>julianday('now')
		THEN COALESCE(t.claimed_by, '') ELSE '' END FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.id=?`, taskID).Scan(&owner); err != nil {
		return err
	}
	if owner == "" {
		return nil
	}
	return checkAgentActiveTaskTx(ctx, tx, owner, taskID)
}

// checkColumnEnteringActiveTx checks the entire column as one delta after
// its administrative update, including duplicate owners within the column.
func checkColumnEnteringActiveTx(ctx context.Context, tx dependencySQL, projectID, columnID, actorID string) error {
	var added int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id=? AND column_id=? AND deleted_at IS NULL`, projectID, columnID).Scan(&added); err != nil {
		return err
	}
	if err := checkAddedActiveCardsTx(ctx, tx, projectID, added); err != nil || added == 0 {
		return err
	}
	if err := requireLegacyActivePlacementTx(ctx, tx, actorID); err != nil {
		return err
	}
	return checkActiveAgentClaimConflictsTx(ctx, tx, projectID, columnID)
}

// checkUnarchivedProjectAgentClaimsTx prevents reactivating a live lease
// while its agent owns a different Active card elsewhere.
func checkUnarchivedProjectAgentClaimsTx(ctx context.Context, tx dependencySQL, projectID string) error {
	return checkActiveAgentClaimConflictsTx(ctx, tx, projectID, "")
}

func checkActiveAgentClaimConflictsTx(ctx context.Context, tx dependencySQL, projectID, columnID string) error {
	var alreadyActive bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM tasks entrant JOIN actors a ON a.id=entrant.claimed_by
		JOIN columns entered ON entered.id=entrant.column_id
		JOIN projects entered_project ON entered_project.id=entrant.project_id
		WHERE entrant.project_id=? AND (?='' OR entrant.column_id=?)
		  AND entrant.deleted_at IS NULL AND entered.archived_at IS NULL
		  AND entered.semantic_state='active' AND entered_project.archived_at IS NULL
		  AND a.kind='agent' AND julianday(entrant.claim_expires_at)>julianday('now')
		  AND EXISTS (
			SELECT 1 FROM tasks t JOIN columns c ON c.id=t.column_id
			JOIN projects p ON p.id=t.project_id
			WHERE t.claimed_by=entrant.claimed_by AND t.id<>entrant.id
			  AND p.archived_at IS NULL
			  AND julianday(t.claim_expires_at)>julianday('now')
			  AND `+activeCardPredicate+`
		  )
	)`, projectID, columnID, columnID).Scan(&alreadyActive)
	if err != nil {
		return err
	}
	return checkAgentAlreadyActive(alreadyActive)
}
