package httpapi

import "github.com/KanterLabs/helm/internal/store"

// Internal callers can lower a run's budgets for a deployment or focused retry.
// Values never exceed the server's hard ceilings, even if a future caller
// accidentally passes task/model-controlled values.
func boundedRetrievalBudget(b store.GithubAuditRetrievalBudget) store.GithubAuditRetrievalBudget {
	maximum := defaultRetrievalBudget()
	clamp := func(value *int, ceiling int) {
		if *value < 0 {
			*value = 0
		}
		if *value > ceiling {
			*value = ceiling
		}
	}
	clamp(&b.IndexPlanningRounds, maximum.IndexPlanningRounds)
	clamp(&b.PerCardFiles, maximum.PerCardFiles)
	clamp(&b.PerCardBytes, maximum.PerCardBytes)
	clamp(&b.RunFiles, maximum.RunFiles)
	clamp(&b.RunReadBytes, maximum.RunReadBytes)
	clamp(&b.RunExcerptBytes, maximum.RunExcerptBytes)
	clamp(&b.RunDiscoveryRequests, maximum.RunDiscoveryRequests)
	clamp(&b.PerCardDiscoveryRequests, maximum.PerCardDiscoveryRequests)
	clamp(&b.RefinementRounds, maximum.RefinementRounds)
	clamp(&b.ExcerptBytes, maximum.ExcerptBytes)
	clamp(&b.DurationSeconds, maximum.DurationSeconds)
	if b.DurationSeconds == 0 {
		b.DurationSeconds = maximum.DurationSeconds
	}
	return b
}

func (r *retrievalRun) linkedGap(task store.GithubAuditTask, repository, code string) {
	source := &retrievalSource{repository: repository, role: "linked_pr"}
	for _, condition := range task.Conditions {
		r.gap(source, task.ID, condition.ID, code, "", "Linked evidence is unavailable; judge the selected baseline separately.")
	}
}
