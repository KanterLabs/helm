package githubprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Isolation tests for the failures recorded in failures.md, "Branch comparison".

type fakeBranch struct {
	name, sha             string
	ahead, behind         int // against the base trunk
	aheadOfStaging        int
	compareStatus         int    // non-zero replaces the comparison reply
	compareBody, listName string // raw reply override; listed name override
}

type branchUpstream struct {
	mu            sync.Mutex
	branches      []fakeBranch
	defaultBranch string
	alwaysMore    bool
	limitAfter    int // comparisons served before 429; zero disables
	lists         int
	compares      []string
}

var pinnedCompare = regexp.MustCompile(`^/repos/example/repo/compare/([0-9a-f]{40})\.\.\.([0-9a-f]{40})$`)

func tip(n int) string { return fmt.Sprintf("%040x", n) }

func (u *branchUpstream) find(name string) (fakeBranch, bool) {
	for _, branch := range u.branches {
		if branch.name == name {
			return branch, true
		}
	}
	return fakeBranch{}, false
}

func (u *branchUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+sentinel && !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer rotated") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/repos/example/repo":
		fmt.Fprintf(w, `{"id":11,"default_branch":%q}`, u.defaultBranch)
	case r.URL.Path == "/repos/example/repo/branches":
		u.lists++
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start, end := (page-1)*100, page*100
		listed := []map[string]any{}
		for i := start; i < end && i < len(u.branches); i++ {
			name := u.branches[i].name
			if u.branches[i].listName != "" {
				name = u.branches[i].listName
			}
			listed = append(listed, map[string]any{"name": name, "commit": map[string]string{"sha": u.branches[i].sha}})
		}
		if end < len(u.branches) || u.alwaysMore {
			// A hostile next link must never be followed.
			w.Header().Set("Link", `<https://attacker.invalid/steal>; rel="next"`)
		}
		_ = json.NewEncoder(w).Encode(listed)
	case pinnedCompare.MatchString(r.URL.Path):
		u.compares = append(u.compares, r.URL.Path+"?"+r.URL.RawQuery)
		if u.limitAfter > 0 && len(u.compares) > u.limitAfter {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		match := pinnedCompare.FindStringSubmatch(r.URL.Path)
		var source fakeBranch
		for _, branch := range u.branches {
			if branch.sha == match[1] {
				source = branch
			}
		}
		if source.compareStatus != 0 {
			w.WriteHeader(source.compareStatus)
			return
		}
		if source.compareBody != "" {
			fmt.Fprint(w, source.compareBody)
			return
		}
		ahead, behind := source.behind, source.ahead
		if staging, ok := u.find("beta"); ok && match[2] == staging.sha && match[1] != staging.sha {
			ahead, behind = 0, source.aheadOfStaging
		}
		fmt.Fprintf(w, `{"ahead_by":%d,"behind_by":%d,"base_commit":{"sha":%q,"commit":{"committer":{"date":"2026-11-01T18:06:44Z"}}},"files":"never requested"}`, ahead, behind, match[1])
	default:
		u.compares = append(u.compares, "unexpected "+r.URL.Path)
		http.NotFound(w, r)
	}
}

func branchFixture(t *testing.T, upstream *branchUpstream) *Client {
	t.Helper()
	if upstream.defaultBranch == "" {
		upstream.defaultBranch = "main"
	}
	return fixture(t, upstream.ServeHTTP)
}

func standardBranches() []fakeBranch {
	return []fakeBranch{
		{name: "main", sha: tip(1)},
		{name: "beta", sha: tip(2), ahead: 2, behind: 1},
		{name: "feat/open", sha: tip(3), ahead: 3, behind: 18, aheadOfStaging: 3},
		{name: "feat/in-beta", sha: tip(4), ahead: 1, behind: 1, aheadOfStaging: 0},
		{name: "fix/merged", sha: tip(5), ahead: 0, behind: 5},
	}
}

func named(report BranchReport, name string) Branch {
	for _, branch := range report.Branches {
		if branch.Name == name {
			return branch
		}
	}
	return Branch{}
}

func TestBranchReportComparesPinnedSHAsAndReusesFinishedWork(t *testing.T) {
	upstream := &branchUpstream{branches: standardBranches()}
	c := branchFixture(t, upstream)
	report, err := c.Branches(context.Background(), "Example/Repo")
	if err != nil {
		t.Fatal(err)
	}
	if report.Base.Name != "main" || report.Base.SHA != tip(1) || report.Base.CommittedAt == nil || report.Partial {
		t.Fatalf("base: %+v partial=%v", report.Base, report.Partial)
	}
	if report.Staging == nil || report.Staging.Name != "beta" || report.Staging.Ahead != 2 || report.Staging.Behind != 1 || !report.Staging.Compared {
		t.Fatalf("staging: %+v", report.Staging)
	}
	if len(report.Branches) != 3 {
		t.Fatalf("trunks must not be repeated as branches: %+v", report.Branches)
	}
	open, inBeta, merged := named(report, "feat/open"), named(report, "feat/in-beta"), named(report, "fix/merged")
	if open.Ahead != 3 || open.Behind != 18 || !open.Compared || open.InStaging == nil || *open.InStaging || open.CommittedAt == nil {
		t.Fatalf("open: %+v", open)
	}
	if inBeta.InStaging == nil || !*inBeta.InStaging {
		t.Fatalf("in staging: %+v", inBeta)
	}
	if merged.Ahead != 0 || merged.Behind != 5 || !merged.Compared || merged.InStaging != nil {
		t.Fatalf("merged: %+v", merged)
	}
	// main with itself, three branches and beta against main, two unmerged against beta.
	if len(upstream.compares) != 7 {
		t.Fatalf("comparisons: %v", upstream.compares)
	}
	for _, request := range upstream.compares {
		if !strings.HasSuffix(request, "?per_page=1&page=2") || strings.Contains(request, "feat") || strings.Contains(request, "unexpected") {
			t.Fatalf("comparison must be pinned and patch-free: %s", request)
		}
	}
	for path := range c.cache {
		if strings.Contains(path, "/compare/") {
			t.Fatalf("comparison evicts the conditional cache: %s", path)
		}
	}
	if _, err = c.Branches(context.Background(), "example/repo"); err != nil || len(upstream.compares) != 7 {
		t.Fatalf("finished comparisons repeated: %v %v", err, upstream.compares[7:])
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), sentinel) {
		t.Fatal("credential in report")
	}
}

func TestBranchReportMissingCommitLeavesRepositoryAccessible(t *testing.T) {
	branches := standardBranches()
	branches[2].compareStatus = http.StatusNotFound
	upstream := &branchUpstream{branches: branches}
	c := branchFixture(t, upstream)
	generation := c.Generation()
	report, err := c.Branches(context.Background(), "example/repo")
	if err != nil {
		t.Fatal(err)
	}
	gone := named(report, "feat/open")
	if gone.Compared || gone.InStaging != nil || !report.Partial {
		t.Fatalf("rewritten tip must be uncompared, never merged: %+v partial=%v", gone, report.Partial)
	}
	if !named(report, "fix/merged").Compared || !c.AccessAllowed("example/repo") || c.Generation() != generation {
		t.Fatal("one missing commit denied the repository or purged caches")
	}
	branches[2].compareStatus = http.StatusForbidden
	c = branchFixture(t, &branchUpstream{branches: branches})
	if _, err = c.Branches(context.Background(), "example/repo"); ErrorCode(err) != "permission_required" {
		t.Fatalf("forbidden comparison: %v", err)
	}
	if c.AccessAllowed("example/repo") {
		t.Fatal("permission loss during comparison must deny the repository")
	}
}

func TestBranchReportTrunkFallbacks(t *testing.T) {
	upstream := &branchUpstream{defaultBranch: "trunk", branches: []fakeBranch{{name: "trunk", sha: tip(1)}, {name: "feat/open", sha: tip(3), ahead: 2, behind: 4}}}
	report, err := branchFixture(t, upstream).Branches(context.Background(), "example/repo")
	if err != nil || report.Base.Name != "trunk" || report.Staging != nil || len(report.Branches) != 1 {
		t.Fatalf("default-branch base: %+v %v", report, err)
	}
	if open := named(report, "feat/open"); open.InStaging != nil || open.Ahead != 2 {
		t.Fatalf("staging invented: %+v", open)
	}
	if len(upstream.compares) != 2 {
		t.Fatalf("requests without a staging trunk: %v", upstream.compares)
	}
	upstream = &branchUpstream{defaultBranch: "gone", branches: []fakeBranch{{name: "feat/open", sha: tip(3)}}}
	if _, err = branchFixture(t, upstream).Branches(context.Background(), "example/repo"); ErrorCode(err) != "unavailable" || len(upstream.compares) != 0 {
		t.Fatalf("invented a base: %v %v", err, upstream.compares)
	}
}

func TestBranchReportBoundsListingAndRejectsUnsafeEntries(t *testing.T) {
	branches := standardBranches()
	branches = append(branches,
		fakeBranch{name: "bad-control", listName: "evil\x1b[31m", sha: tip(6)},
		fakeBranch{name: "bad-long", listName: strings.Repeat("a", 257), sha: tip(7)},
		fakeBranch{name: "bad-sha", sha: "not-a-sha"},
		fakeBranch{name: "dup", listName: "feat/open", sha: tip(8)})
	upstream := &branchUpstream{branches: branches}
	report, err := branchFixture(t, upstream).Branches(context.Background(), "example/repo")
	if err != nil || len(report.Branches) != 3 || !report.Partial {
		t.Fatalf("unsafe entries: %+v %v", report.Branches, err)
	}
	if open := named(report, "feat/open"); open.SHA != tip(3) {
		t.Fatalf("duplicate replaced the first listing: %+v", open)
	}
	upstream = &branchUpstream{branches: standardBranches(), alwaysMore: true}
	report, err = branchFixture(t, upstream).Branches(context.Background(), "example/repo")
	if err != nil || !report.Partial || upstream.lists != maxPages {
		t.Fatalf("pagination: partial=%v lists=%d %v", report.Partial, upstream.lists, err)
	}
}

func TestBranchReportRejectsInconsistentComparisons(t *testing.T) {
	for name, body := range map[string]string{
		"other base":     `{"ahead_by":0,"behind_by":0,"base_commit":{"sha":"` + tip(99) + `"}}`,
		"negative":       `{"ahead_by":-1,"behind_by":0,"base_commit":{"sha":"` + tip(3) + `"}}`,
		"missing counts": `{"base_commit":{"sha":"` + tip(3) + `"}}`,
		"absurd":         `{"ahead_by":0,"behind_by":99999999999,"base_commit":{"sha":"` + tip(3) + `"}}`,
		"credential":     `{"ahead_by":0,"behind_by":0,"note":"` + sentinel + `","base_commit":{"sha":"` + tip(3) + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			branches := standardBranches()
			branches[2].compareBody = body
			report, err := branchFixture(t, &branchUpstream{branches: branches}).Branches(context.Background(), "example/repo")
			if err != nil {
				t.Fatal(err)
			}
			if open := named(report, "feat/open"); open.Compared || open.Ahead != 0 || !report.Partial {
				t.Fatalf("inconsistent reply trusted: %+v partial=%v", open, report.Partial)
			}
			if encoded, _ := json.Marshal(report); strings.Contains(string(encoded), sentinel) {
				t.Fatal("credential in report")
			}
		})
	}
}

func TestBranchReportRateLimitAndBudgetResume(t *testing.T) {
	upstream := &branchUpstream{branches: standardBranches(), limitAfter: 2}
	c := branchFixture(t, upstream)
	report, err := c.Branches(context.Background(), "example/repo")
	if err != nil || !report.Partial {
		t.Fatalf("rate limit mid-scan: partial=%v %v", report.Partial, err)
	}
	served := len(upstream.compares)
	if served > 2+comparisonWorkers {
		t.Fatalf("scan continued after a rate limit: %d", served)
	}
	if _, err = c.Branches(context.Background(), "example/repo"); ErrorCode(err) != "rate_limited" || len(upstream.compares) != served {
		t.Fatalf("backoff ignored: %v", err)
	}
	upstream.mu.Lock()
	upstream.limitAfter = 0
	upstream.mu.Unlock()
	c.mu.Lock()
	c.backoffUntil = time.Time{}
	c.mu.Unlock()
	if report, err = c.Branches(context.Background(), "example/repo"); err != nil || report.Partial || len(upstream.compares) > served+7-2 {
		t.Fatalf("resume repeated finished work: partial=%v compares=%d %v", report.Partial, len(upstream.compares)-served, err)
	}

	many := []fakeBranch{{name: "main", sha: tip(1)}}
	for i := 0; i < maxNewComparisons+40; i++ {
		many = append(many, fakeBranch{name: fmt.Sprintf("merged/%03d", i), sha: tip(1000 + i), behind: i + 1})
	}
	upstream = &branchUpstream{branches: many}
	c = branchFixture(t, upstream)
	report, err = c.Branches(context.Background(), "example/repo")
	uncompared := 0
	for _, branch := range report.Branches {
		if !branch.Compared {
			uncompared++
		}
	}
	if err != nil || !report.Partial || len(upstream.compares) != maxNewComparisons || uncompared != 41 || len(report.Branches) != maxNewComparisons+40 {
		t.Fatalf("budget: partial=%v compares=%d uncompared=%d %v", report.Partial, len(upstream.compares), uncompared, err)
	}
	if report, err = c.Branches(context.Background(), "example/repo"); err != nil || report.Partial || len(upstream.compares) != maxNewComparisons+41 {
		t.Fatalf("second report: partial=%v compares=%d %v", report.Partial, len(upstream.compares), err)
	}
}

func TestBranchReportScopeAndCredentialRotation(t *testing.T) {
	upstream := &branchUpstream{branches: standardBranches()}
	c := branchFixture(t, upstream)
	if _, err := c.Branches(context.Background(), "other/repo"); ErrorCode(err) != "repository_not_allowed" || upstream.lists != 0 {
		t.Fatalf("out-of-scope repository: %v", err)
	}
	if _, err := c.Branches(context.Background(), "example/repo"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.tokenFile, []byte("rotated-synthetic-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Branches(context.Background(), "example/repo"); err != nil || len(upstream.compares) != 14 {
		t.Fatalf("rotation kept private comparisons: %v compares=%d", err, len(upstream.compares))
	}
	if err := os.Remove(c.tokenFile); err != nil {
		t.Fatal(err)
	}
	lists := upstream.lists
	if _, err := c.Branches(context.Background(), "example/repo"); ErrorCode(err) != "not_configured" || upstream.lists != lists || len(c.comparisons) != 0 {
		t.Fatalf("missing credential: %v", err)
	}
}
