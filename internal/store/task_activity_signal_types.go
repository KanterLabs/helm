package store

import "time"

// Neglect signal (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D2). "Task activity" in
// older files means agent progress history; this read model is named
// task_activity_signal to keep the two apart. The API field stays activity.

// TaskActivityClassificationVersion is stored on every derived row. Raising
// it makes older rows eligible for the projector's seed pass (plan D4).
const TaskActivityClassificationVersion = 2

// Stored statuses. "pending" is a transport-only status (plan D5) and is
// never stored or produced by the classifier.
const (
	TaskActivityKnown   = "known"
	TaskActivityBounded = "bounded"
	TaskActivityUnknown = "unknown"
)

// Reasons carried by bounded and unknown results; known results have none.
const (
	TaskActivityReasonLegacyUpdate        = "legacy_update"
	TaskActivityReasonUnknownEventType    = "unknown_event_type"
	TaskActivityReasonActorUnavailable    = "actor_unavailable"
	TaskActivityReasonUnparseableTime     = "unparseable_time"
	TaskActivityReasonClassificationError = "classification_error"
)

// taskActivitySignalTimeLayout is fixed-width UTC so stored text compares as
// instants (`not_after <= ?`) whatever offset or precision the source used.
const taskActivitySignalTimeLayout = "2006-01-02T15:04:05.000000000Z"

// TaskActivitySignal is one task's classification (plan D2).
//
//   - known: LastMeaningfulAt and NotAfter are the last qualifying activity,
//     Source names it, Reason is empty.
//   - bounded: only NotAfter is set, the newest ambiguous row's time, which is
//     at or after the last qualifying activity; Reason comes from that row.
//   - unknown: no times; Reason says why.
//
// A card is inactive for N days exactly when NotAfter <= now - N days.
type TaskActivitySignal struct {
	TaskID                string
	Status                string
	LastMeaningfulAt      *time.Time
	Source                string
	NotAfter              *time.Time
	Reason                string
	ClassificationVersion int
	// ComputedAt is set by a rebuild, in taskActivitySignalTimeLayout.
	ComputedAt string
}

func formatTaskActivitySignalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(taskActivitySignalTimeLayout)
}
