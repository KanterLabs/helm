package httpapi

import (
	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
	"strings"
)

func (r *retrievalRun) step(w *retrievalWork) error {
	if err := r.fence(); err != nil {
		return err
	}
	if r.evidence.AssessmentVersion == store.GithubAssessmentVersion {
		if refs := store.GithubAuditObservedChecks(*r.evidence, w.task, w.condition); len(refs) > 0 {
			w.evidenceIDs = refs
			w.done = true
			return nil
		}
	}
	if !w.initialized {
		w.initialized = true
		if w.source.problem != "" {
			w.problem = w.source.problem
			w.done = true
			return nil
		}
		w.required = appendUnique(w.required, w.anchors.paths...)
		w.remaining = appendUnique(w.remaining, w.anchors.terms...)
		w.queue = appendUnique(w.queue, w.anchors.paths...)
		w.queue = appendUnique(w.queue, w.source.preferred...)
		for _, term := range w.anchors.terms {
			hints, err := r.discover(w.source, w.task.ID, term)
			if err != nil {
				return err
			}
			w.queue = appendUnique(w.queue, hints.paths...)
			if hints.problem != "" {
				w.problem = hints.problem
			}
		}
		if len(w.anchors.paths) == 0 && len(w.anchors.terms) == 0 {
			if _, err := r.plan(w); err != nil {
				return err
			}
		}
		w.queue = appendUnique(w.queue, likelyOwners(w.source.tree, len(w.anchors.terms) > 0)...)
		w.rounds = 1
	}
	var entry *githubprovider.TreeEntry
	for len(w.queue) > 0 {
		name := w.queue[0]
		w.queue = w.queue[1:]
		if w.seen[name] {
			continue
		}
		w.seen[name] = true
		for i := range w.source.tree {
			if w.source.tree[i].Path == name {
				entry = &w.source.tree[i]
				break
			}
		}
		if entry != nil {
			break
		} // default-branch hint absent from pinned tree is no proof
	}
	if entry == nil {
		if !w.implementation {
			planned, err := r.plan(w)
			if err != nil {
				return err
			}
			if planned {
				return nil
			}
		}
		w.done = true
		return nil
	}
	read, err := r.read(w.source, w.task.ID, *entry)
	if err != nil {
		return err
	}
	if read.problem != "" {
		w.problem = read.problem
		w.problemPath = entry.Path
		if strings.Contains(read.problem, "budget") {
			w.done = true
		}
		return nil
	}
	w.inspected = appendUnique(w.inspected, entry.Path)
	w.required = removeValue(w.required, entry.Path)
	terms := append(append([]string{}, w.anchors.terms...), w.searchTerms...)
	lines := relevantLines(read.file.Text, terms)
	if sourceClass(entry.Path) == "implementation" {
		lines = implementationLines(read.file.Text, terms)
	}
	explicit := false
	for _, p := range w.anchors.paths {
		if p == entry.Path {
			explicit = true
		}
	}
	if planned, ok := w.semantic[entry.Path]; ok {
		count := len(strings.Split(read.file.Text, "\n"))
		if planned.StartLine > count {
			w.problem = "semantic_range_unavailable"
			return nil
		}
		lines = nil
		for line := planned.StartLine - 1; line < planned.EndLine && line < count; line++ {
			lines = append(lines, line)
		}
		explicit = true
	}
	if len(lines) == 0 && !explicit {
		return nil
	}
	if len(lines) == 0 {
		lines = []int{0}
	}
	class := sourceClass(entry.Path)
	var ids []string
	var limited bool
	if planned, ok := w.semantic[entry.Path]; ok {
		ids, limited = r.plannedExcerpts(w, read.file, planned, class)
	} else {
		bodies, truncated := semanticSearchBodies(entry.Path, read.file.Text, w.searchTerms)
		if len(bodies) > 0 {
			for _, body := range bodies {
				more, cut := r.plannedExcerpts(w, read.file, body, class)
				ids = appendUnique(ids, more...)
				limited = limited || cut
				if cut {
					break
				}
			}
			limited = limited || truncated
		} else {
			ids, limited = r.excerpts(w, read.file, lines, class)
		}
	}
	w.evidenceIDs = appendUnique(w.evidenceIDs, ids...)
	if limited {
		w.problem = "excerpt_budget_exhausted"
		w.problemPath = entry.Path
		w.done = true
		return nil
	}
	if class != "implementation" {
		return nil
	}
	w.implementation = true
	// Search results are alternatives, not a required closure of every hint.
	// Once a matching pinned owner supplies an excerpt, the evaluator judges
	// whether that source answers the model's named criterion question.
	for term, owners := range w.searchOwners {
		for _, owner := range owners {
			if owner == entry.Path {
				w.pending = removeValue(w.pending, term)
				break
			}
		}
	}
	for _, term := range append([]string{}, w.remaining...) {
		if len(implementationLines(read.file.Text, []string{term})) > 0 {
			w.remaining = removeValue(w.remaining, term)
		}
	}
	for _, term := range w.pending {
		if regexpDefinition(term).MatchString(read.file.Text) {
			w.pending = removeValue(w.pending, term)
		}
	}
	// Ownership prefixes are context for the next semantic request. Chasing
	// constructor helpers here can consume the card before its behavior is read.
	if plan, ok := w.semantic[entry.Path]; ok && plan.StartLine == 1 && len(strings.Split(read.file.Text, "\n")) > 40 {
		ready, err := r.semanticOwnersReady(w)
		if err != nil {
			return err
		}
		if !ready {
			return nil
		}
		w.problem = "semantic_discovery_unresolved"
		w.done = true
		return nil
	}
	semanticFollowups := r.evidence.AssessmentVersion == store.GithubAssessmentVersion && len(w.anchors.paths) == 0 && len(w.anchors.terms) == 0 && (len(w.semantic) > 0 || len(w.searchTerms) > 0)
	if w.rounds < r.budget.RefinementRounds && !semanticFollowups {
		refs := sourceReferences(read.file.Text, lines, w.anchors.terms)
		if r.evidence.AssessmentVersion == store.GithubAssessmentVersion {
			refs = fileSourceReferences(entry.Path, read.file.Text, lines, w.anchors.terms)
		}
		for _, ref := range refs {
			// A definition already present in this excerpt needs no network hint.
			definition := regexpDefinition(ref)
			if at := definition.FindStringIndex(read.file.Text); at != nil {
				refLines := []int{strings.Count(read.file.Text[:at[0]], "\n")}
				more, limited := r.excerpts(w, read.file, refLines, class)
				w.evidenceIDs = appendUnique(w.evidenceIDs, more...)
				if limited {
					w.problem = "excerpt_budget_exhausted"
					w.problemPath = entry.Path
					w.done = true
					return nil
				}
				continue
			}
			w.pending = appendUnique(w.pending, ref)
			hints, err := r.discover(w.source, w.task.ID, ref)
			if err != nil {
				return err
			}
			// A search hint can have been read before this symbol was known.
			// Reconsider its cached pinned text instead of locking out that owner.
			for _, hint := range hints.paths {
				delete(w.seen, hint)
			}
			w.queue = appendUnique(hints.paths, w.queue...)
			w.anchors.terms = appendUnique(w.anchors.terms, ref)
		}
		w.rounds++
	}
	// Once an implementation owner and its references were inspected, unrelated
	// fallback files cannot improve this named retrieval gap.
	if len(w.pending) == 0 && len(w.required) == 0 && len(w.remaining) == 0 {
		ready, err := r.semanticOwnersReady(w)
		if err != nil {
			return err
		}
		w.done = ready
	}
	return nil
}
