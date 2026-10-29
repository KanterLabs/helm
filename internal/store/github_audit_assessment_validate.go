package store

// validateGithubAssessment checks v2 completely before producing the legacy
// public projection. No legacy validator can authorize a malformed assessment.
func validateGithubAssessment(a GithubAssessmentInput, c GithubAuditCapture, e GithubAuditEvidence) (GithubAssessment, AuditFindingInput, error) {
	fail := func(message string) (GithubAssessment, AuditFindingInput, error) {
		return GithubAssessment{}, AuditFindingInput{}, invalid(message, nil)
	}
	var task GithubAuditTask
	found := false
	for _, t := range c.Tasks {
		if t.ID == a.TaskID {
			task = t
			found = true
			break
		}
	}
	if !found || a.CapturedVersion != task.Version || a.SourceColumnID != task.ColumnID {
		return fail("assessment task snapshot does not match capture")
	}
	if !assessmentText(a.Reason, 2000) {
		return fail("assessment needs a bounded explanation")
	}
	supported, contradicted, err := validateAssessmentConditions(a, task, e)
	if err != nil {
		return GithubAssessment{}, AuditFindingInput{}, err
	}
	facts, err := validateAssessmentBasis(a, task, e)
	if err != nil {
		return GithubAssessment{}, AuditFindingInput{}, err
	}
	projection := AuditFindingInput{TaskID: task.ID, CapturedVersion: task.Version, SourceColumn: task.ColumnID, SourceColumnID: task.ColumnID, Reason: a.Reason, ReviewState: "pending", EvidenceRefs: []string{}}
	for _, condition := range a.Conditions {
		for _, ref := range condition.CitationIDs {
			if !assessmentContains(projection.EvidenceRefs, ref) {
				projection.EvidenceRefs = append(projection.EvidenceRefs, ref)
			}
		}
	}
	for _, basis := range a.Basis {
		for _, ref := range basis.CitationIDs {
			if !assessmentContains(projection.EvidenceRefs, ref) {
				projection.EvidenceRefs = append(projection.EvidenceRefs, ref)
			}
		}
	}
	if a.Placement == "unable_to_verify" {
		if a.TargetColumnID != nil || a.TargetSemanticState != nil || a.LifecycleAction != "none" {
			return fail("unable to verify cannot recommend a mutation")
		}
		projection.Verdict = "needs_attention"
	} else {
		if len(task.Conditions) == 0 {
			return fail("conclusive placement needs completion meaning and actual destination")
		}
		if a.TargetColumnID == nil {
			return fail("conclusive placement has missing target identity")
		}
		if a.TargetSemanticState == nil {
			return fail("conclusive placement has missing target semantic state")
		}
		target, unique := assessmentDestination(c.Columns, *a.TargetSemanticState)
		if a.Placement == "column_correct" && *a.TargetColumnID == task.ColumnID && *a.TargetSemanticState == task.SemanticState {
			for _, col := range c.Columns {
				if col.ID == task.ColumnID {
					target = col
					unique = true
				}
			}
		}
		if !unique || target.ID != *a.TargetColumnID {
			if !unique {
				return fail("destination semantic mapping is unavailable or ambiguous")
			}
			return fail("destination identity does not match semantic mapping")
		}
		sourceKnown := false
		for _, col := range c.Columns {
			if col.ID == task.ColumnID && col.SemanticState == task.SemanticState && validState(col.SemanticState) {
				sourceKnown = true
			}
		}
		if !sourceKnown {
			return fail("source column meaning is ambiguous; clarify mapping")
		}
		all := supported == len(task.Conditions)
		state := *a.TargetSemanticState
		switch state {
		case "completed":
			if !all {
				return fail("Done requires every completion condition supported")
			}
		case "active":
			if all || (!facts["implementation"] && !facts["work_remaining"]) || (contradicted == 0 && !facts["work_remaining"]) {
				return fail("In progress needs committed implementation and demonstrated remaining work")
			}
		case "ready", "backlog":
			readiness := "ready"
			if state == "backlog" {
				readiness = "not_ready"
			}
			if all || supported > 0 || contradicted > 0 || !facts["unstarted"] || !facts[readiness] {
				return fail("unstarted placement needs affirmative work/readiness context")
			}
			if state == "ready" {
				for _, d := range task.Context.Dependencies {
					if d.SemanticState != "completed" {
						return fail("Ready conflicts with an unresolved dependency")
					}
				}
			}
		case "blocked":
			if all || !facts["blocker"] {
				return fail("Blocked requires a concrete unresolved blocker")
			}
		default:
			return fail("invalid semantic destination")
		}
		action := assessmentLifecycle(task, state)
		if target.ID == task.ColumnID {
			if a.Placement != "column_correct" || a.LifecycleAction != "none" {
				return fail("correct placement has no lifecycle action")
			}
			projection.Verdict = "correct"
		} else {
			if a.Placement != "correction_recommended" || a.LifecycleAction != action {
				return fail("correction/action conflicts with captured placement")
			}
			projection.Verdict = "move_proposed"
			projection.ProposedSemanticDestination = a.TargetSemanticState
		}
	}
	if _, err = validateAuditFindingInput(projection); err != nil {
		return GithubAssessment{}, AuditFindingInput{}, err
	}
	result := GithubAssessment{GithubAssessmentInput: a, AssessmentVersion: GithubAssessmentVersion, TaskKey: task.Key, TaskKind: task.Kind, SourceColumnName: task.ColumnName, SourceSemanticState: task.SemanticState, Baselines: e.Repositories, CapturedAt: e.CapturedAt, ApplicationEligibility: GithubAssessmentEligibility{Status: "not_applicable", Reasons: []string{}}}
	if a.Placement == "correction_recommended" {
		result.ApplicationEligibility = GithubAssessmentEligibility{Status: "review_required", Reasons: []string{"human_confirmation_required", "lifecycle_validation_required"}}
	}
	return result, projection, nil
}

func assessmentDestination(columns []Column, state string) (Column, bool) {
	var target Column
	count := 0
	for _, c := range columns {
		if c.ArchivedAt == nil && c.SemanticState == state {
			target = c
			count++
		}
	}
	return target, count == 1
}
func assessmentLifecycle(task GithubAuditTask, target string) string {
	if task.SemanticState == "completed" {
		if task.Kind == "bug" {
			return "bug_reopen"
		}
		return "reopen"
	}
	if target == "completed" {
		if task.Kind == "bug" {
			return "bug_resolve"
		}
		return "complete"
	}
	if target == "active" {
		return "start"
	}
	if target == "blocked" {
		return "block"
	}
	return "move"
}
