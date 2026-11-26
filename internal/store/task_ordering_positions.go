package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

const (
	maxTaskOrderingPosition = 1e12
	// A generous gap keeps ordinary drag/drop operations from needing a
	// rebalance while remaining well below the existing position cap.
	taskOrderingRebalanceSpacing = 1024.0
)

type orderedTask struct {
	ID       string
	Number   int
	Position float64
}

func listOrderedTasksTx(ctx context.Context, tx *sql.Tx, projectID, columnID, excludingID string) ([]orderedTask, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, number, position FROM tasks WHERE project_id=? AND column_id=? AND id<>? AND deleted_at IS NULL AND completed_at IS NULL ORDER BY position, number, id`, projectID, columnID, excludingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]orderedTask, 0)
	for rows.Next() {
		var item orderedTask
		if err := rows.Scan(&item.ID, &item.Number, &item.Position); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func orderingBounds(items []orderedTask, input TaskMoveInput) (*orderedTask, *orderedTask, error) {
	if len(items) == 0 {
		if input.BeforeTaskID != "" || input.AfterTaskID != "" {
			return nil, nil, errors.New("ordering anchor is not in the destination column")
		}
		return nil, nil, nil
	}
	byID := make(map[string]int, len(items))
	for index, item := range items {
		byID[item.ID] = index
	}
	beforeIndex, beforeFound := byID[input.BeforeTaskID]
	afterIndex, afterFound := byID[input.AfterTaskID]
	if input.BeforeTaskID != "" && !beforeFound || input.AfterTaskID != "" && !afterFound {
		return nil, nil, errors.New("ordering anchor is not in the destination column")
	}
	placement := input.Placement
	if placement == "" {
		switch {
		case beforeFound && afterFound:
			placement = "between"
		case beforeFound:
			placement = "before"
		case afterFound:
			placement = "after"
		default:
			placement = "last"
		}
	}
	switch placement {
	case "first":
		return nil, &items[0], nil
	case "last":
		return &items[len(items)-1], nil, nil
	case "before":
		var lower *orderedTask
		if beforeIndex > 0 {
			lower = &items[beforeIndex-1]
		}
		return lower, &items[beforeIndex], nil
	case "after":
		var upper *orderedTask
		if afterIndex+1 < len(items) {
			upper = &items[afterIndex+1]
		}
		return &items[afterIndex], upper, nil
	case "between":
		if afterIndex >= beforeIndex {
			return nil, nil, errors.New("ordering anchors are reversed")
		}
		return &items[afterIndex], &items[beforeIndex], nil
	default:
		return nil, nil, errors.New("invalid ordering placement")
	}
}

func orderingPlacement(input TaskMoveInput) string {
	if input.Placement != "" {
		return input.Placement
	}
	if input.BeforeTaskID != "" && input.AfterTaskID != "" {
		return "between"
	}
	if input.BeforeTaskID != "" {
		return "before"
	}
	if input.AfterTaskID != "" {
		return "after"
	}
	return "last"
}

func chooseOrderingPosition(ctx context.Context, tx *sql.Tx, items []orderedTask, before, after *orderedTask) (float64, bool, error) {
	position, ok := availableOrderingPosition(items, before, after)
	if ok {
		return position, false, nil
	}
	if err := rebalanceTaskOrdering(ctx, tx, items); err != nil {
		return 0, false, err
	}
	// Re-read the bounds from the rebalanced slice. The pointers still refer
	// to the same backing array, whose positions were updated in place.
	position, ok = availableOrderingPosition(items, before, after)
	if !ok {
		return 0, true, invalid("task ordering could not allocate a stable position", nil)
	}
	return position, true, nil
}

// availableOrderingPosition prefers the midpoint but also looks for an
// unoccupied representable gap. A hidden card may already own the midpoint
// between two visible anchors; choosing the first open sub-gap preserves that
// card's numeric position and relative order without an unnecessary rebalance.
func availableOrderingPosition(items []orderedTask, before, after *orderedTask) (float64, bool) {
	if candidate, ok := midpointOrderingPosition(before, after); ok && !orderingPositionUsed(items, candidate) {
		return candidate, true
	}
	lower := 0.0
	if before != nil {
		lower = before.Position
	}
	upper := maxTaskOrderingPosition
	if after != nil {
		upper = after.Position
	}
	if upper <= lower {
		return 0, false
	}
	for _, item := range items {
		if item.Position <= lower || item.Position >= upper {
			continue
		}
		if candidate, ok := openOrderingGap(lower, item.Position); ok && !orderingPositionUsed(items, candidate) {
			return candidate, true
		}
		lower = item.Position
	}
	return openOrderingGap(lower, upper)
}

func openOrderingGap(lower, upper float64) (float64, bool) {
	candidate := lower + (upper-lower)/2
	if math.IsNaN(candidate) || math.IsInf(candidate, 0) || candidate <= lower || candidate >= upper || candidate < 0 || candidate > maxTaskOrderingPosition {
		return 0, false
	}
	return candidate, true
}

func orderingPositionUsed(items []orderedTask, candidate float64) bool {
	for _, item := range items {
		if item.Position == candidate {
			return true
		}
	}
	return false
}

func midpointOrderingPosition(before, after *orderedTask) (float64, bool) {
	var candidate float64
	switch {
	case before == nil && after == nil:
		candidate = 0
	case before == nil:
		candidate = after.Position - 1
	case after == nil:
		candidate = before.Position + 1
	default:
		candidate = before.Position + (after.Position-before.Position)/2
	}
	if math.IsNaN(candidate) || math.IsInf(candidate, 0) || candidate < 0 || candidate > maxTaskOrderingPosition {
		return 0, false
	}
	if before != nil && !(candidate > before.Position) {
		return 0, false
	}
	if after != nil && !(candidate < after.Position) {
		return 0, false
	}
	return candidate, true
}

func rebalanceTaskOrdering(ctx context.Context, tx *sql.Tx, items []orderedTask) error {
	if len(items) == 0 {
		return nil
	}
	spacing := taskOrderingRebalanceSpacing
	if float64(len(items))*spacing > maxTaskOrderingPosition {
		spacing = maxTaskOrderingPosition / float64(len(items)+1)
	}
	if spacing <= 0 || math.IsInf(spacing, 0) || math.IsNaN(spacing) {
		return invalid("task ordering has too many cards to rebalance", nil)
	}
	for index := range items {
		position := float64(index+1) * spacing
		if position > maxTaskOrderingPosition {
			return invalid("task ordering has too many cards to rebalance", nil)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET position=? WHERE id=? AND deleted_at IS NULL`, position, items[index].ID); err != nil {
			return err
		}
		items[index].Position = position
	}
	return nil
}
