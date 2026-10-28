package githubprovider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

var discoveryTerm = regexp.MustCompile(`^[A-Za-z0-9_./{}-]{1,200}$`)

// SourceDiscovery is optional, keeping existing Provider implementations valid.
// Search results are default-branch hints, never evidence at a requested commit.
// Callers must intersect paths with its pinned tree and ReadFile at that commit.
type SourceDiscovery interface {
	FindPaths(context.Context, string, string) ([]string, bool, error)
	PullRequestPaths(context.Context, string, int, string) ([]string, bool, error)
}

func (c *Client) FindPaths(ctx context.Context, repository, term string) ([]string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return nil, false, err
	}
	// Only a literal route/symbol/path is accepted; callers cannot supply search
	// operators, quotes, whitespace or a second repository scope.
	if !discoveryTerm.MatchString(term) {
		return nil, false, providerError("invalid_input")
	}
	generation := c.Generation()
	var result struct {
		Total      int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			Path       string `json:"path"`
			Repository struct {
				Name string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	more, err := c.getWithPolicy(ctx, repository, "/search/code?q="+url.QueryEscape(`repo:`+repository+` "`+term+`"`)+"&per_page=30", &result, true)
	if err != nil {
		return nil, false, err
	}
	partial := more || result.Incomplete || result.Total > len(result.Items)
	paths := []string{}
	seen := map[string]bool{}
	for i, item := range result.Items {
		if i >= 30 {
			partial = true
			break
		}
		repo, err := NormalizeRepository(item.Repository.Name)
		if err != nil || repo != repository || !validPath(item.Path) {
			partial = true
			continue
		}
		if !seen[item.Path] {
			paths = append(paths, item.Path)
			seen[item.Path] = true
		}
	}
	if !c.sameGeneration(generation) {
		return nil, false, providerError("unavailable")
	}
	return paths, partial, nil
}

// PullRequestPaths returns a bounded list of change hints only while the PR head
// still matches the captured snapshot. No patch text or remote URL is followed.
func (c *Client) PullRequestPaths(ctx context.Context, repository string, number int, head string) ([]string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return nil, false, err
	}
	if number <= 0 || !shaPattern.MatchString(head) {
		return nil, false, providerError("invalid_input")
	}
	generation := c.Generation()
	base := fmt.Sprintf("/repos/%s/pulls/%d", repository, number)
	verify := func() error {
		var pr struct {
			Head struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		if _, err := c.get(ctx, repository, base, &pr); err != nil {
			return err
		}
		if pr.Head.SHA != head {
			return providerError("head_changed")
		}
		return nil
	}
	if err = verify(); err != nil {
		return nil, false, err
	}
	var entries []struct {
		Path string `json:"filename"`
	}
	more, err := c.get(ctx, repository, base+"/files?per_page=100", &entries)
	if err != nil {
		return nil, false, err
	}
	paths := []string{}
	partial := more
	for i, entry := range entries {
		if i >= 100 {
			partial = true
			break
		}
		if !validPath(entry.Path) {
			partial = true
			continue
		}
		paths = append(paths, entry.Path)
	}
	if err = verify(); err != nil {
		return nil, false, err
	}
	if !c.sameGeneration(generation) {
		return nil, false, providerError("unavailable")
	}
	return paths, partial, nil
}
