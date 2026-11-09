package store

// sort=not_after (test/neglect/API.md). The key is
// (not_after IS NULL, not_after, number, id) in one direction, so tasks
// without a bound (unknown rows, or no row yet) sort after every time
// ascending and before every time descending. Stored not_after text is
// fixed-width UTC, so text order is time order.

const taskNotAfterExpression = `(SELECT sig.not_after FROM task_activity_signal sig WHERE sig.task_id=t.id)`

func taskNotAfterOrderSQL(direction string) string {
	return "(" + taskNotAfterExpression + " IS NULL) " + direction + ", " + taskNotAfterExpression + " " + direction +
		", t.number " + direction + ", t.id " + direction
}

// taskNotAfterSortValue is the cursor value of the page's last task: the
// stored not_after the order compared, or null.
func taskNotAfterSortValue(task Task) (string, bool) {
	if task.Activity == nil || task.Activity.sortNotAfter == "" {
		return "", true
	}
	return task.Activity.sortNotAfter, false
}

// taskNotAfterCursorPredicate selects the rows strictly after the cursor in
// the NULLS-last (ascending) or NULLS-first (descending) order.
func taskNotAfterCursorPredicate(args []any, cursor TaskCursor, descending bool) (string, []any) {
	operator := ">"
	if descending {
		operator = "<"
	}
	value := taskNotAfterExpression
	tie := "(t.number " + operator + " ? OR (t.number = ? AND t.id " + operator + " ?))"
	tieArgs := []any{cursor.Number, cursor.Number, cursor.ID}
	switch {
	case cursor.Null && !descending:
		return "(" + value + " IS NULL AND " + tie + ")", append(args, tieArgs...)
	case cursor.Null:
		return "(" + value + " IS NOT NULL OR " + tie + ")", append(args, tieArgs...)
	case !descending:
		return "(" + value + " IS NULL OR " + value + " > ? OR (" + value + " = ? AND " + tie + "))",
			append(append(args, cursor.Value, cursor.Value), tieArgs...)
	default:
		return "(" + value + " IS NOT NULL AND (" + value + " < ? OR (" + value + " = ? AND " + tie + ")))",
			append(append(args, cursor.Value, cursor.Value), tieArgs...)
	}
}
