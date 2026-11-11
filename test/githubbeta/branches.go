package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// syntheticBranch is one branch of the synthetic repository. Counts are fixed;
// commit dates are relative to the harness clock so stale work stays stale.
type syntheticBranch struct {
	name          string
	ahead, behind int // against main
	aheadOfBeta   int
	ageDays       int
	gone          bool // the tip was rewritten: its comparison answers 404
}

var syntheticBranches = []syntheticBranch{
	{name: "main", ageDays: 4},
	{name: "beta", ahead: 2, behind: 1, ageDays: 2},
	{name: "feat/neglect-signal", ahead: 1, behind: 1, aheadOfBeta: 0, ageDays: 2},
	{name: "feat/branch-graph", ahead: 3, behind: 18, aheadOfBeta: 3, ageDays: 10},
	{name: "fix/audit-timeout", ahead: 14, behind: 36, aheadOfBeta: 14, ageDays: 13},
	{name: "docs/old-plan", ahead: 2, behind: 120, aheadOfBeta: 2, ageDays: 75},
	{name: "backlog/search", ahead: 5, behind: 255, aheadOfBeta: 5, ageDays: 70},
	{name: "release/v0.2.2", behind: 59, ageDays: 14},
	{name: "fix/settings-column", behind: 15, ageDays: 10},
	{name: "wip/force-pushed", gone: true},
}

// The main tip is the commit every other synthetic route already resolves.
func branchSHA(index int) string {
	if index == 0 {
		return sourceSHA
	}
	return fmt.Sprintf("%040x", 0xb0000+index)
}

func (s *syntheticState) serveBranches(w http.ResponseWriter, r *http.Request, path string) bool {
	switch {
	case path == "/branches":
		s.mu.Lock()
		delay := s.branchDelayMS
		s.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(time.Duration(delay) * time.Millisecond):
			case <-r.Context().Done():
				return true
			}
		}
		listed := []any{}
		if page := r.URL.Query().Get("page"); page == "" || page == "1" {
			for index, branch := range syntheticBranches {
				listed = append(listed, map[string]any{"name": branch.name, "commit": map[string]string{"sha": branchSHA(index)}})
			}
		}
		writeJSON(w, listed)
	case strings.HasPrefix(path, "/compare/"):
		source, target, ok := strings.Cut(strings.TrimPrefix(path, "/compare/"), "...")
		for index, branch := range syntheticBranches {
			if !ok || branchSHA(index) != source {
				continue
			}
			if branch.gone {
				break
			}
			ahead, behind := branch.behind, branch.ahead
			if target == branchSHA(1) && index != 1 {
				ahead, behind = 0, branch.aheadOfBeta
			} else if target != sourceSHA {
				break
			}
			writeJSON(w, map[string]any{"ahead_by": ahead, "behind_by": behind, "base_commit": map[string]any{"sha": source,
				"commit": map[string]any{"committer": map[string]string{"date": time.Now().UTC().AddDate(0, 0, -branch.ageDays).Format(time.RFC3339)}}}})
			return true
		}
		http.NotFound(w, r)
	default:
		return false
	}
	return true
}
