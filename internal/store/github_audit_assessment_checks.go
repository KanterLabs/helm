package store

// GithubAuditObservedChecks identifies captured observations that can judge a
// check-only condition at the selected baseline. Such a condition needs check
// evidence, not a source-code owner; other conditions still need implementation.
// The evaluator separately decides supported/contradicted and validates proof.
func GithubAuditObservedChecks(e GithubAuditEvidence, task GithubAuditTask, condition GithubAuditCondition) []string {
	refs := []string{}
	if !assessmentCheckCondition.MatchString(condition.Text) {
		return refs
	}
	for _, item := range e.Items {
		if !assessmentContains(item.TaskIDs, task.ID) || !assessmentContains(item.CriterionIDs, condition.ID) {
			continue
		}
		if assessmentCheck(e, item, "supported") || assessmentCheck(e, item, "contradicted") {
			refs = append(refs, item.ID)
		}
	}
	return refs
}
