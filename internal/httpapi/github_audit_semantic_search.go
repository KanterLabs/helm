package httpapi

import "strings"

type semanticSearch struct {
	Term     string `json:"term"`
	Question string `json:"question"`
}

func validSemanticSearch(s semanticSearch) bool {
	return len(s.Term) >= 3 && literalDiscoveryTerm(s.Term) && strings.TrimSpace(s.Question) != "" && len(s.Question) <= 1000
}

// Model-selected concepts use the existing provider's literal search contract.
// Search results are only hints: membership in the pinned tree is checked here,
// and every actual read still passes the SHA/blob/size checks in read.
func (r *retrievalRun) semanticSearch(w *retrievalWork, searches []semanticSearch) ([]string, error) {
	paths := []string{}
	if w.searchOwners == nil {
		w.searchOwners = map[string][]string{}
	}
	for _, search := range searches {
		if w.searchQuestions == nil {
			w.searchQuestions = map[string]string{}
		}
		w.searchQuestions[search.Term] = search.Question
		if w.searchFirst && w.semanticRounds > 1 {
			if w.searchDefinitions == nil {
				w.searchDefinitions = map[string]bool{}
			}
			w.searchDefinitions[search.Term] = true
		}
		hints, err := r.discover(w.source, w.task.ID, r.semanticQueryTerm(w, search.Term))
		if err != nil {
			return nil, err
		}
		if err := r.service.fresh(r.ctx, *r.evidence); err != nil {
			return nil, err
		}
		if err := r.fence(); err != nil {
			return nil, err
		}
		if hints.problem != "" {
			w.problem = hints.problem
		}
		w.pending = appendUnique(w.pending, search.Term)
		w.searchTerms = appendUnique(w.searchTerms, search.Term)
		for _, hint := range hints.paths {
			for _, entry := range w.source.tree {
				if entry.Path == hint && sourceClass(hint) == "implementation" {
					w.searchOwners[search.Term] = appendUnique(w.searchOwners[search.Term], hint)
					paths = appendUnique(paths, hint)
					w.searchPaths = appendUnique(w.searchPaths, hint)
					delete(w.seen, hint)
					break
				}
			}
		}
	}
	return paths, nil
}

// Selected excerpts matter more than a filename or package prefix: refinement
// must be able to reject an irrelevant body beyond the first 4096 bytes. Keep
// the existing 12-KiB aggregate observed-text ceiling and explicit truncation.
func (r *retrievalRun) semanticInspected(w *retrievalWork) []map[string]any {
	inspected := []map[string]any{}
	remaining := 12 * 1024
	for _, name := range w.inspected {
		read := r.reads[w.source.repository+"@"+w.source.sha+":"+name]
		excerpts := []map[string]any{}
		truncated := false
		for _, item := range r.evidence.Items {
			if item.Path != name || item.SHA != w.source.sha || item.Repository != w.source.repository {
				continue
			}
			linked := false
			for _, id := range w.evidenceIDs {
				linked = linked || item.ID == id
			}
			if !linked {
				continue
			}
			if len(item.Text) > remaining {
				truncated = true
				continue
			}
			remaining -= len(item.Text)
			excerpts = append(excerpts, map[string]any{"start_line": item.StartLine, "end_line": item.EndLine, "text": item.Text})
		}
		inspected = append(inspected, map[string]any{"path": name, "excerpts": excerpts, "excerpts_truncated": truncated, "symbols": semanticSymbols(read.file.Text)})
	}
	// All selected bodies get first use of the shared observed-text allowance;
	// an earlier file's generic prefix must not crowd out a later behavior body.
	for i, name := range w.inspected {
		read := r.reads[w.source.repository+"@"+w.source.sha+":"+name]
		prefix := read.file.Text
		if len(prefix) > 4096 {
			prefix = prefix[:4096]
		}
		if len(prefix) > remaining {
			prefix = prefix[:remaining]
		}
		remaining -= len(prefix)
		inspected[i]["prefix"] = prefix
		inspected[i]["prefix_truncated"] = len(prefix) < len(read.file.Text)
	}
	return inspected
}
