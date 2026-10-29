package httpapi

import (
	"path"
	"sort"
	"strings"
)

type semanticCandidate struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	Preferred  bool   `json:"linked_pr_hint"`
	SearchHint bool   `json:"criterion_search_hint"`
	Kind       string `json:"kind"`
}

// A bounded file index must not hide every later-sorting ownership directory.
// Supplementary tests/docs must not force a small production tree into a
// single-directory guess. Explicit criterion paths and linked-PR hints remain
// inspectable. Genuinely large production trees still use bounded routing.
func semanticIndex(w *retrievalWork) ([]semanticCandidate, bool) {
	files := []semanticCandidate{}
	for _, entry := range w.source.tree {
		explicit := false
		for _, p := range append(append([]string{}, w.anchors.paths...), w.source.preferred...) {
			explicit = explicit || p == entry.Path
		}
		if sourceClass(entry.Path) != "implementation" && !explicit || len(entry.Path) > 512 {
			continue
		}
		searchHint := false
		for _, p := range w.searchPaths {
			searchHint = searchHint || p == entry.Path
		}
		if !searchHint && w.semanticDirectory == "." && path.Dir(entry.Path) != "." {
			continue
		}
		if !searchHint && w.semanticDirectory != "" && w.semanticDirectory != "." && !strings.HasPrefix(entry.Path, w.semanticDirectory+"/") {
			continue
		}
		preferred := false
		for _, p := range w.source.preferred {
			preferred = preferred || p == entry.Path
		}
		files = append(files, semanticCandidate{Path: entry.Path, Size: entry.Size, Preferred: preferred, SearchHint: searchHint, Kind: "file"})
	}
	totalBytes := 0
	for _, f := range files {
		totalBytes += len(f.Path)
	}
	directories := w.semanticDirectory == "" && len(w.searchPaths) == 0 && (len(files) > 512 || totalBytes > 48*1024)
	if directories {
		owners := map[string]semanticCandidate{}
		roots := []semanticCandidate{}
		for _, f := range files {
			dir := path.Dir(f.Path)
			if dir == "." {
				roots = append(roots, f)
				continue
			}
			owner := owners[dir]
			owner.Path = dir
			owner.Kind = "directory"
			owner.Preferred = owner.Preferred || f.Preferred
			owner.SearchHint = owner.SearchHint || f.SearchHint
			owners[dir] = owner
		}
		files = roots
		for _, dir := range owners {
			files = append(files, dir)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].SearchHint != files[j].SearchHint {
			return files[i].SearchHint
		}
		if files[i].Preferred != files[j].Preferred {
			return files[i].Preferred
		}
		return files[i].Path < files[j].Path
	})
	out := []semanticCandidate{}
	bytes := 0
	for _, f := range files {
		if len(out) >= 512 || bytes+len(f.Path) > 48*1024 {
			break
		}
		out = append(out, f)
		bytes += len(f.Path)
	}
	return out, directories
}
