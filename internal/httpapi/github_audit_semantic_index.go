package httpapi

import (
	"path"
	"sort"
	"strings"
)

type semanticCandidate struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Preferred bool   `json:"linked_pr_hint"`
	Kind      string `json:"kind"`
}

// A bounded file index must not hide every later-sorting ownership directory.
// For large trees, first offer exact server-known owning directories, then the
// selected directory's pinned files. This is semantic routing, not word ranking.
func semanticIndex(w *retrievalWork) ([]semanticCandidate, bool) {
	files := []semanticCandidate{}
	for _, entry := range w.source.tree {
		if sourceClass(entry.Path) == "other" || len(entry.Path) > 512 {
			continue
		}
		if w.semanticDirectory == "." && path.Dir(entry.Path) != "." {
			continue
		}
		if w.semanticDirectory != "" && w.semanticDirectory != "." && !strings.HasPrefix(entry.Path, w.semanticDirectory+"/") {
			continue
		}
		preferred := false
		for _, p := range w.source.preferred {
			preferred = preferred || p == entry.Path
		}
		files = append(files, semanticCandidate{Path: entry.Path, Size: entry.Size, Preferred: preferred, Kind: "file"})
	}
	totalBytes := 0
	for _, f := range files {
		totalBytes += len(f.Path)
	}
	directories := w.semanticDirectory == "" && (len(files) > 512 || totalBytes > 48*1024)
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
			owners[dir] = owner
		}
		files = roots
		for _, dir := range owners {
			files = append(files, dir)
		}
	}
	sort.Slice(files, func(i, j int) bool {
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
