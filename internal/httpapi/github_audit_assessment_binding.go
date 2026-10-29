package httpapi

import "github.com/KanterLabs/helm/internal/store"

// The model judges semantics; actual destinations are server-owned snapshot
// identities. Fill absent metadata only, then let the complete store validator
// reject every conflicting identity, ambiguous mapping, proof and lifecycle.
func bindGithubAssessmentDestinations(rows []store.GithubAssessmentInput, capture store.GithubAuditCapture) {
	for i := range rows {
		a := &rows[i]
		switch a.Placement {
		case "column_correct":
			for _, task := range capture.Tasks {
				if task.ID != a.TaskID {
					continue
				}
				if a.TargetColumnID == nil {
					id := task.ColumnID
					a.TargetColumnID = &id
				}
				if a.TargetSemanticState == nil {
					state := task.SemanticState
					a.TargetSemanticState = &state
				}
				break
			}
		case "correction_recommended":
			if a.TargetColumnID != nil || a.TargetSemanticState == nil {
				continue
			}
			var target string
			count := 0
			for _, col := range capture.Columns {
				if col.ArchivedAt == nil && col.SemanticState == *a.TargetSemanticState {
					target = col.ID
					count++
				}
			}
			if count == 1 {
				a.TargetColumnID = &target
			}
		}
	}
}
