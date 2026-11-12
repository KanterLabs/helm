package githubprovider

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	baseBranch         = "main"
	stagingBranch      = "beta"
	maxNewComparisons  = 160
	maxComparisons     = 4096
	comparisonWorkers  = 6
	maxComparisonCount = 10_000_000
)

// BranchTip is the base trunk at the commit every branch was measured against.
type BranchTip struct {
	Name        string     `json:"name"`
	SHA         string     `json:"sha"`
	CommittedAt *time.Time `json:"committed_at,omitempty"`
}

// Branch is one branch measured against the base trunk. Ahead and Behind mean
// nothing unless Compared is true; an uncompared branch is never merged.
type Branch struct {
	Name        string     `json:"name"`
	SHA         string     `json:"sha"`
	CommittedAt *time.Time `json:"committed_at,omitempty"`
	Ahead       int        `json:"ahead"`
	Behind      int        `json:"behind"`
	Compared    bool       `json:"compared"`
	// InStaging stays nil until a comparison with the staging tip succeeded.
	InStaging *bool `json:"in_staging,omitempty"`
}

type BranchReport struct {
	Repository string    `json:"repository"`
	Base       BranchTip `json:"base"`
	Staging    *Branch   `json:"staging"`
	Branches   []Branch  `json:"branches"`
	Partial    bool      `json:"partial"`
	FetchedAt  time.Time `json:"fetched_at"`
}

// comparison measures a source commit against a target commit.
type comparison struct {
	missing     int // commits on the target that the source lacks
	unique      int // commits only the source has
	committedAt time.Time
}

type comparisonScan struct {
	client     *Client
	ctx        context.Context
	repository string
	generation uint64
	budget     int
	mu         sync.Mutex
	incomplete bool
}

// Branches lists every branch against the base trunk (`main`, otherwise the
// default branch) and reports how the `beta` staging trunk compares, when it
// exists. It is bounded in requests, concurrency and time; anything it could
// not measure is returned uncompared with Partial set.
func (c *Client) Branches(ctx context.Context, repository string) (BranchReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return BranchReport{}, err
	}
	generation := c.Generation()
	tips, names, partial, err := c.branchTips(ctx, repository)
	if err != nil {
		return BranchReport{}, err
	}
	base := baseBranch
	if _, ok := tips[base]; !ok {
		var metadata struct {
			DefaultBranch string `json:"default_branch"`
		}
		if _, err = c.get(ctx, repository, "/repos/"+repository, &metadata); err != nil {
			return BranchReport{}, err
		}
		base = metadata.DefaultBranch
		if _, ok = tips[base]; !ok {
			return BranchReport{}, providerError("unavailable")
		}
	}
	staging := ""
	if _, ok := tips[stagingBranch]; ok && base != stagingBranch {
		staging = stagingBranch
	}
	scan := &comparisonScan{client: c, ctx: ctx, repository: repository, generation: generation, budget: maxNewComparisons}
	// The base compared with itself yields only its commit date. Trunks go
	// first so a constrained budget still answers the headline question.
	pairs := [][2]string{{tips[base], tips[base]}}
	if staging != "" {
		pairs = append(pairs, [2]string{tips[staging], tips[base]})
	}
	for _, name := range names {
		pairs = append(pairs, [2]string{tips[name], tips[base]})
	}
	against := scan.run(pairs)
	pairs = pairs[:0]
	if staging != "" {
		for _, name := range names {
			if result, ok := against[[2]string{tips[name], tips[base]}]; ok && result.unique > 0 && name != base && name != staging {
				pairs = append(pairs, [2]string{tips[name], tips[staging]})
			}
		}
	}
	contained := scan.run(pairs)
	if !c.sameGeneration(generation) {
		if !c.AccessAllowed(repository) {
			return BranchReport{}, providerError("permission_required")
		}
		return BranchReport{}, providerError("unavailable")
	}
	report := BranchReport{Repository: repository, Base: BranchTip{Name: base, SHA: tips[base]}, Branches: []Branch{}, Partial: partial || scan.incomplete, FetchedAt: time.Now().UTC()}
	if own, ok := against[[2]string{tips[base], tips[base]}]; ok {
		report.Base.CommittedAt = commitTime(own.committedAt)
	}
	for _, name := range names {
		if name == base {
			continue
		}
		branch := Branch{Name: name, SHA: tips[name]}
		if result, ok := against[[2]string{tips[name], tips[base]}]; ok {
			branch.Ahead, branch.Behind, branch.Compared = result.unique, result.missing, true
			branch.CommittedAt = commitTime(result.committedAt)
		}
		if name == staging {
			report.Staging = &branch
			continue
		}
		if result, ok := contained[[2]string{tips[name], tips[staging]}]; ok && staging != "" {
			inStaging := result.unique == 0
			branch.InStaging = &inStaging
		}
		report.Branches = append(report.Branches, branch)
	}
	return report, nil
}

// branchTips reads at most maxPages of branches. Names are display data: they
// are validated for safe rendering and never interpolated into a request.
func (c *Client) branchTips(ctx context.Context, repository string) (map[string]string, []string, bool, error) {
	tips, names, partial := map[string]string{}, []string{}, false
	for page := 1; page <= maxPages; page++ {
		var listed []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		more, err := c.get(ctx, repository, "/repos/"+repository+"/branches?per_page=100&page="+strconv.Itoa(page), &listed)
		if err != nil {
			return nil, nil, false, err
		}
		for _, branch := range listed {
			if _, duplicate := tips[branch.Name]; duplicate || !validBranchName(branch.Name) || !shaPattern.MatchString(branch.Commit.SHA) {
				partial = true
				continue
			}
			tips[branch.Name] = strings.ToLower(branch.Commit.SHA)
			names = append(names, branch.Name)
		}
		if !more {
			break
		}
		partial = partial || page == maxPages
	}
	sort.Strings(names)
	return tips, names, partial, nil
}

func validBranchName(name string) bool {
	if len(name) < 1 || len(name) > 256 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func commitTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

// run resolves pairs from finished comparisons first, then spends the scan's
// remaining budget on new upstream requests. A rate limit, lost permission or
// missing credential stops the scan; one bad comparison does not.
func (s *comparisonScan) run(pairs [][2]string) map[[2]string]comparison {
	c, results, pending, seen := s.client, map[[2]string]comparison{}, [][2]string{}, map[[2]string]bool{}
	c.mu.Lock()
	for _, pair := range pairs {
		if seen[pair] {
			continue
		}
		seen[pair] = true
		if cached, ok := c.comparisons[s.repository+" "+pair[0]+" "+pair[1]]; ok && c.generation == s.generation {
			results[pair] = cached
		} else if s.budget > 0 {
			s.budget--
			pending = append(pending, pair)
		} else {
			s.incomplete = true
		}
	}
	c.mu.Unlock()
	var stopped atomic.Bool
	var wait sync.WaitGroup
	slots := make(chan struct{}, comparisonWorkers)
	for _, pair := range pending {
		slots <- struct{}{}
		wait.Add(1)
		go func(pair [2]string) {
			defer wait.Done()
			defer func() { <-slots }()
			var result comparison
			err := providerError("unavailable")
			if !stopped.Load() && s.ctx.Err() == nil {
				result, err = c.compare(s.ctx, s.repository, s.generation, pair[0], pair[1])
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if err != nil {
				s.incomplete = true
				if code := ErrorCode(err); code != "not_found" && code != "unavailable" {
					stopped.Store(true)
				}
				return
			}
			results[pair] = result
		}(pair)
	}
	wait.Wait()
	return results
}

// compare measures source against target by immutable SHA. The second result
// page carries the counts and the source commit but never file patches.
func (c *Client) compare(ctx context.Context, repository string, generation uint64, source, target string) (comparison, error) {
	var response struct {
		AheadBy    *int `json:"ahead_by"`
		BehindBy   *int `json:"behind_by"`
		BaseCommit struct {
			SHA    string `json:"sha"`
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		} `json:"base_commit"`
	}
	if _, err := c.fetch(ctx, repository, "/repos/"+repository+"/compare/"+source+"..."+target+"?per_page=1&page=2", &response, true); err != nil {
		return comparison{}, err
	}
	if response.AheadBy == nil || response.BehindBy == nil || *response.AheadBy < 0 || *response.BehindBy < 0 ||
		*response.AheadBy > maxComparisonCount || *response.BehindBy > maxComparisonCount || !strings.EqualFold(response.BaseCommit.SHA, source) {
		return comparison{}, providerError("unavailable")
	}
	result := comparison{missing: *response.AheadBy, unique: *response.BehindBy, committedAt: response.BaseCommit.Commit.Committer.Date}
	c.mu.Lock()
	if c.generation == generation {
		if len(c.comparisons) >= maxComparisons {
			c.comparisons = map[string]comparison{}
		}
		c.comparisons[repository+" "+source+" "+target] = result
	}
	c.mu.Unlock()
	return result, nil
}
