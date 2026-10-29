package httpapi

import (
	"sort"
	"strings"

	"github.com/KanterLabs/helm/internal/store"
)

// Provider search is lexical; receiver identity is enforced against pinned
// declarations afterward. A Go method declaration does not contain Type.Method.
func (r *retrievalRun) semanticQueryTerm(w *retrievalWork, term string) string {
	if strings.Contains(term, ".") {
		for _, name := range w.inspected {
			read := r.reads[w.source.repository+"@"+w.source.sha+":"+name]
			for _, body := range semanticDeclarationBodies(name, read.file.Text) {
				if body.selector == term {
					return body.name
				}
			}
		}
	}
	// Dotted paths and unknown selectors retain their literal query. Only
	// already inspected declarations establish a receiver-qualified identity.
	return term
}

func semanticMatchingBodies(name, text, term string) []semanticDeclarationBody {
	var matches []semanticDeclarationBody
	for _, body := range semanticDeclarationBodies(name, text) {
		if body.selector == term || body.name == term {
			matches = append(matches, body)
		}
	}
	return matches
}

// A selected caller must not shadow a requested helper in that same file.
// Accumulated bodies still pass semanticSelectedBody's shared three-window cap.
func semanticRequestedDeclarations(w *retrievalWork, plan semanticPath, text string) semanticPath {
	plan.declarations = append([]string{}, plan.declarations...)
	if len(plan.declarations) == 0 {
		plan.declarations = []string{plan.Declaration}
	}
	for _, term := range w.pending {
		if !w.searchDefinitions[term] || !assessmentPathContains(w.searchOwners[term], plan.Path) {
			continue
		}
		if matches := semanticMatchingBodies(plan.Path, text, term); len(matches) == 1 {
			plan.declarations = appendUnique(plan.declarations, matches[0].selector)
		}
	}
	return plan
}

func assessmentPathContains(paths []string, name string) bool {
	for _, p := range paths {
		if p == name {
			return true
		}
	}
	return false
}

// A requested definition is resolved only when its full exact body is present
// in this criterion's evidence, not from a call or a declaration-only snippet.
func semanticDefinitionCovered(w *retrievalWork, e store.GithubAuditEvidence, name, text, term string) bool {
	matches := semanticMatchingBodies(name, text, term)
	if len(matches) != 1 {
		return false
	}
	var intervals [][2]int
	for _, item := range e.Items {
		if item.Path == name && item.Repository == w.source.repository && item.SHA == w.source.sha && item.EvidenceClass == "implementation" && assessmentPathContains(w.evidenceIDs, item.ID) {
			intervals = append(intervals, [2]int{item.StartLine, item.EndLine})
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	next := matches[0].start
	for _, span := range intervals {
		if span[0] > next {
			break
		}
		if span[1] >= next {
			next = span[1] + 1
		}
	}
	return next > matches[0].end
}
