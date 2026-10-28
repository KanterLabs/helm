package httpapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/KanterLabs/helm/internal/githubprovider"
	"github.com/KanterLabs/helm/internal/store"
)

func regexpDefinition(symbol string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)\b(?:func|function|fn|def)\s+(?:\([^\n)]*\)\s*)?` + regexp.QuoteMeta(symbol) + `\s*\(`)
}

// Excerpts are whole lines with surrounding context. Never truncate in the
// middle of UTF-8 or pretend a shortened line is the original complete line.
func (r *retrievalRun) excerpts(w *retrievalWork, file githubprovider.File, matches []int, class string) ([]string, bool) {
	lines := strings.Split(file.Text, "\n")
	ids := []string{}
	card := r.card(w.task.ID)
	lastEnd := -1
	for _, match := range matches {
		if match <= lastEnd {
			continue
		}
		start, end := match-8, match+24
		if start < 0 {
			start = 0
		}
		if end >= len(lines) {
			end = len(lines) - 1
		}
		text := strings.Join(lines[start:end+1], "\n")
		for len(text) > r.budget.ExcerptBytes && end > match {
			end--
			text = strings.Join(lines[start:end+1], "\n")
		}
		for len(text) > r.budget.ExcerptBytes && start < match {
			start++
			text = strings.Join(lines[start:end+1], "\n")
		}
		if len(text) > r.budget.ExcerptBytes {
			return ids, true
		}
		key := fmt.Sprintf("%s:%s:%d:%d", sourceKey(w.source), file.Path, start, end)
		if index, ok := r.items[key]; ok {
			item := &r.evidence.Items[index]
			item.TaskIDs = appendUnique(item.TaskIDs, w.task.ID)
			item.CriterionIDs = appendUnique(item.CriterionIDs, w.condition.ID)
			ids = appendUnique(ids, item.ID)
			lastEnd = end
			continue
		}
		if card.excerpts+len(text) > r.budget.PerCardBytes || r.excerptBytes+len(text) > r.budget.RunExcerptBytes {
			return ids, true
		}
		id := fmt.Sprintf("github:file:%d", len(r.items)+1)
		r.items[key] = len(r.evidence.Items)
		r.evidence.Items = append(r.evidence.Items, store.GithubAuditItem{ID: id, Kind: "file", EvidenceClass: class, Title: file.Path, URL: file.URL, Repository: w.source.repository, Ref: w.source.ref, SHA: file.CommitSHA, BlobSHA: file.SHA, Path: file.Path, SourceRole: w.source.role, StartLine: start + 1, EndLine: end + 1, Text: text, TaskIDs: []string{w.task.ID}, CriterionIDs: []string{w.condition.ID}})
		r.excerptBytes += len(text)
		card.excerpts += len(text)
		ids = append(ids, id)
		lastEnd = end
		if len(ids) >= 3 {
			break
		}
	}
	return ids, false
}
