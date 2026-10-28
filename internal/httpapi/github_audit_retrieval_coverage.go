package httpapi

import (
	"fmt"
	"strings"

	"github.com/KanterLabs/helm/internal/store"
)

func (r *retrievalRun) sourceCoverage(sources []*retrievalSource) {
	counted := map[string]bool{}
	for _, source := range sources {
		key := source.repository + "@" + source.sha
		inspected := 0
		for file, read := range r.reads {
			if strings.HasPrefix(file, key+":") && read.problem == "" {
				inspected++
			}
		}
		unread := len(source.tree) - inspected
		r.evidence.SourceCoverage = append(r.evidence.SourceCoverage, store.GithubAuditSourceCoverage{Repository: source.repository, Ref: source.ref, SHA: source.sha, SourceRole: source.role, IndexedFiles: len(source.tree), InspectedFiles: inspected, UnreadFiles: unread, IndexPartial: source.indexPartial})
		// Shared baseline/PR pins count physical reads once while retaining both
		// source roles in the manifest and in every criterion coverage record.
		if !counted[key] && unread > 0 {
			r.evidence.OmittedFiles += unread
			r.evidence.Omissions = append(r.evidence.Omissions, fmt.Sprintf("%d indexed files not inspected at %s; see criterion gaps for relevance", unread, key))
		}
		counted[key] = true
	}
}
