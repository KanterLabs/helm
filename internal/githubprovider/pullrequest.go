package githubprovider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type upstreamPR struct {
	Title          string `json:"title"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA  string `json:"sha"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func (c *Client) FetchPullRequest(ctx context.Context, repository string, number int) (PullRequestSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	repository, err := c.scoped(repository)
	if err != nil {
		return PullRequestSnapshot{}, err
	}
	if number < 1 || number > 2147483647 {
		return PullRequestSnapshot{}, providerError("invalid_input")
	}
	generation := c.Generation()
	key := fmt.Sprintf("%s/%d", repository, number)
	relative := fmt.Sprintf("/repos/%s/pulls/%d", repository, number)
	var pr upstreamPR
	if _, err = c.get(ctx, repository, relative, &pr); err != nil {
		return c.staleSnapshot(key, generation, err)
	}
	if !shaPattern.MatchString(pr.Head.SHA) || !shaPattern.MatchString(pr.Base.SHA) || pr.Base.Repo.ID <= 0 || len(pr.Title) > 4096 || (pr.State != "open" && pr.State != "closed") {
		return PullRequestSnapshot{}, providerError("unavailable")
	}
	snapshot := PullRequestSnapshot{Repository: repository, Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repository, number), RepositoryID: pr.Base.Repo.ID, Title: pr.Title, State: pr.State, Draft: pr.Draft, HeadSHA: pr.Head.SHA, BaseSHA: pr.Base.SHA, Checks: []Check{}, ReviewState: "unknown", ChecksState: "unknown", FetchedAt: time.Now().UTC()}
	if pr.Merged {
		snapshot.State = "merged"
	}
	if shaPattern.MatchString(pr.MergeCommitSHA) {
		snapshot.MergeSHA = pr.MergeCommitSHA
	}
	checks, partial, checkErr := c.checks(ctx, repository, pr.Head.SHA)
	if ErrorCode(checkErr) == "permission_required" || ErrorCode(checkErr) == "not_configured" {
		return PullRequestSnapshot{}, checkErr
	}
	if len(checks) > 200 {
		// Preserve visible failures and pending observations within the store's
		// normalized snapshot bound. Omitted observations prevent all-clear.
		rank := func(state string) int {
			switch state {
			case "failing":
				return 0
			case "pending":
				return 1
			case "unknown":
				return 2
			default:
				return 3
			}
		}
		sort.SliceStable(checks, func(i, j int) bool { return rank(checks[i].State) < rank(checks[j].State) })
		checks = checks[:200]
		partial = true
	}
	snapshot.Checks = checks
	snapshot.Partial = partial
	snapshot.ChecksState = summarizeChecks(checks, partial, checkErr != nil)
	snapshot.ReviewState, partial, err = c.reviews(ctx, repository, number, pr.Head.SHA)
	if ErrorCode(err) == "permission_required" || ErrorCode(err) == "not_configured" {
		return PullRequestSnapshot{}, err
	}
	snapshot.Partial = snapshot.Partial || partial || err != nil
	var final upstreamPR
	if _, err = c.get(ctx, repository, relative, &final); err != nil {
		return c.staleSnapshot(key, generation, err)
	}
	if final.Head.SHA != pr.Head.SHA || final.Base.SHA != pr.Base.SHA || final.State != pr.State || final.Merged != pr.Merged || final.Base.Repo.ID != pr.Base.Repo.ID {
		return PullRequestSnapshot{}, providerError("head_changed")
	}
	if !c.sameGeneration(generation) {
		return PullRequestSnapshot{}, providerError("unavailable")
	}
	c.mu.Lock()
	if c.generation == generation {
		delete(c.denied, repository)
		if len(c.snapshots) >= maxCacheEntries {
			c.snapshots = map[string]PullRequestSnapshot{}
		}
		c.snapshots[key] = cloneSnapshot(snapshot)
	}
	c.mu.Unlock()
	return snapshot, nil
}

func cloneSnapshot(snapshot PullRequestSnapshot) PullRequestSnapshot {
	snapshot.Checks = append([]Check{}, snapshot.Checks...)
	return snapshot
}

func (c *Client) staleSnapshot(key string, generation uint64, err error) (PullRequestSnapshot, error) {
	code := ErrorCode(err)
	if (code != "unavailable" && code != "rate_limited") || !c.sameGeneration(generation) {
		return PullRequestSnapshot{}, err
	}
	c.mu.Lock()
	snapshot, ok := c.snapshots[key]
	c.mu.Unlock()
	if !ok {
		return PullRequestSnapshot{}, err
	}
	snapshot = cloneSnapshot(snapshot)
	snapshot.Stale = true
	return snapshot, err
}

func (c *Client) checks(ctx context.Context, repository, sha string) ([]Check, bool, error) {
	checks := []Check{}
	partial := false
	for page := 1; page <= maxPages; page++ {
		var response struct {
			TotalCount int `json:"total_count"`
			Runs       []struct {
				Name       string `json:"name"`
				HeadSHA    string `json:"head_sha"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				URL        string `json:"html_url"`
			} `json:"check_runs"`
		}
		more, err := c.get(ctx, repository, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100&page=%d&filter=latest", repository, sha, page), &response)
		if err != nil {
			return checks, true, err
		}
		if len(response.Runs) > 100 {
			response.Runs = response.Runs[:100]
			partial = true
		}
		for _, run := range response.Runs {
			if run.HeadSHA != sha || len(run.Name) > 512 {
				partial = true
				continue
			}
			state := "unknown"
			switch run.Status {
			case "queued", "in_progress", "waiting", "requested", "pending":
				state = "pending"
			case "completed":
				switch run.Conclusion {
				case "success":
					state = "passing"
				case "failure", "timed_out", "action_required", "startup_failure":
					state = "failing"
				default:
					state = "unknown"
				}
			}
			checks = append(checks, Check{Name: run.Name, SHA: sha, State: state, Conclusion: run.Conclusion, URL: safeGitHubURL(run.URL)})
		}
		if !more {
			if response.TotalCount > page*100 {
				partial = true
			}
			break
		}
		if page == maxPages {
			partial = true
		}
	}
	// GitHub's legacy status objects omit SHA. The fixed-origin request itself
	// is pinned to the validated immutable head; reject a conflicting SHA if
	// an adapter supplies one.
	// Keep the latest occurrence of each legacy context, in GitHub's newest-first order.
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		var response []struct {
			Name  string `json:"context"`
			SHA   string `json:"sha"`
			State string `json:"state"`
			URL   string `json:"target_url"`
		}
		more, err := c.get(ctx, repository, fmt.Sprintf("/repos/%s/commits/%s/statuses?per_page=100&page=%d", repository, sha, page), &response)
		if err != nil {
			return checks, true, err
		}
		if len(response) > 100 {
			response = response[:100]
			partial = true
		}
		for _, status := range response {
			if (status.SHA != "" && status.SHA != sha) || len(status.Name) > 512 {
				partial = true
				continue
			}
			if seen[status.Name] {
				continue
			}
			seen[status.Name] = true
			state := "unknown"
			switch status.State {
			case "success":
				state = "passing"
			case "failure", "error":
				state = "failing"
			case "pending":
				state = "pending"
			}
			checks = append(checks, Check{Name: status.Name, SHA: sha, State: state, Conclusion: status.State, URL: safeGitHubURL(status.URL)})
		}
		if !more {
			break
		}
		if page == maxPages {
			partial = true
		}
	}
	return checks, partial, nil
}

func summarizeChecks(checks []Check, partial, unavailable bool) string {
	pending, unknown := false, false
	for _, check := range checks {
		switch check.State {
		case "failing":
			return "failing"
		case "pending":
			pending = true
		case "passing":
		default:
			unknown = true
		}
	}
	if pending {
		return "pending"
	}
	if unavailable {
		return "unavailable"
	}
	if partial || unknown || len(checks) == 0 {
		return "unknown"
	}
	return "passing"
}

func (c *Client) reviews(ctx context.Context, repository string, number int, sha string) (string, bool, error) {
	states := map[int64]string{}
	partial := false
	for page := 1; page <= maxPages; page++ {
		var response []struct {
			ID       int64  `json:"id"`
			State    string `json:"state"`
			CommitID string `json:"commit_id"`
			User     struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}
		more, err := c.get(ctx, repository, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100&page=%d", repository, number, page), &response)
		if err != nil {
			return "unknown", true, err
		}
		if len(response) > 100 {
			response = response[:100]
			partial = true
		}
		// Reviews are returned oldest first. A dismissed latest observation
		// replaces approval; comments do not erase a substantive review.
		for _, review := range response {
			if review.User.ID == 0 {
				partial = true
				continue
			}
			state := strings.ToLower(review.State)
			if review.CommitID != "" && review.CommitID != sha && (state == "approved" || state == "changes_requested") {
				state = "unknown"
			}
			if state != "commented" || states[review.User.ID] == "" {
				states[review.User.ID] = state
			}
		}
		if !more {
			break
		}
		if page == maxPages {
			partial = true
		}
	}
	if partial {
		return "unknown", true, nil
	}
	result := "none"
	for _, state := range states {
		switch state {
		case "changes_requested":
			return state, false, nil
		case "approved":
			result = "approved"
		case "commented":
			if result == "none" {
				result = "commented"
			}
		case "dismissed", "pending":
		default:
			if result != "approved" {
				result = "unknown"
			}
		}
	}
	return result, false, nil
}
