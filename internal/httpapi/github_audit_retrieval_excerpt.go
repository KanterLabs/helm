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
		id, limited := r.recordExcerpt(w, file, start, end, text, class)
		if limited {
			return ids, true
		}
		ids = appendUnique(ids, id)
		lastEnd = end
		if len(ids) >= 3 {
			break
		}
	}
	return ids, false
}

// plannedExcerpts captures the entire deliberate behavior window, including
// middle calls. At most three whole-line excerpts share the unchanged byte
// limits; an overlong line/window remains an explicit criterion gap.
func (r *retrievalRun) plannedExcerpts(w *retrievalWork, file githubprovider.File, plan semanticPath, class string) ([]string, bool) {
	lines := strings.Split(file.Text, "\n")
	end := plan.EndLine
	if end > len(lines) {
		end = len(lines)
	}
	ids := []string{}
	for start := plan.StartLine - 1; start < end; {
		last := start
		text := lines[start]
		if len(text) > r.budget.ExcerptBytes {
			return ids, true
		}
		for last+1 < end && len(text)+1+len(lines[last+1]) <= r.budget.ExcerptBytes {
			last++
			text += "\n" + lines[last]
		}
		id, limited := r.recordExcerpt(w, file, start, last, text, class)
		if limited {
			return ids, true
		}
		ids = appendUnique(ids, id)
		start = last + 1
		if len(ids) >= 3 && start < end {
			return ids, true
		}
	}
	return ids, false
}

func (r *retrievalRun) recordExcerpt(w *retrievalWork, file githubprovider.File, start, end int, text, class string) (string, bool) {
	card := r.card(w.task.ID)
	key := fmt.Sprintf("%s:%s:%d:%d", sourceKey(w.source), file.Path, start, end)
	if index, ok := r.items[key]; ok {
		item := &r.evidence.Items[index]
		item.TaskIDs = appendUnique(item.TaskIDs, w.task.ID)
		item.CriterionIDs = appendUnique(item.CriterionIDs, w.condition.ID)
		return item.ID, false
	}
	if card.excerpts+len(text) > r.budget.PerCardBytes || r.excerptBytes+len(text) > r.budget.RunExcerptBytes {
		return "", true
	}
	id := fmt.Sprintf("github:file:%d", len(r.items)+1)
	r.items[key] = len(r.evidence.Items)
	r.evidence.Items = append(r.evidence.Items, store.GithubAuditItem{ID: id, Kind: "file", EvidenceClass: class, Title: file.Path, URL: file.URL, Repository: w.source.repository, Ref: w.source.ref, SHA: file.CommitSHA, BlobSHA: file.SHA, Path: file.Path, SourceRole: w.source.role, StartLine: start + 1, EndLine: end + 1, Text: text, TaskIDs: []string{w.task.ID}, CriterionIDs: []string{w.condition.ID}})
	r.excerptBytes += len(text)
	card.excerpts += len(text)
	return id, false
}
