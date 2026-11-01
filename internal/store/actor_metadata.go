package store

import "strings"

// IsIntakeSource identifies the immutable namespace reserved by intakeActorID.
// Public agent creation always allocates a random ID; names and descriptions
// cannot opt an actor into or out of this namespace. Deriving this metadata
// preserves legacy attribution and credentials without rewriting actor rows.
func (a Actor) IsIntakeSource() bool {
	return a.Kind == "agent" && strings.HasPrefix(a.ID, "actor-") &&
		strings.HasSuffix(a.ID, "-intake") && len(a.ID) > len("actor--intake")
}
