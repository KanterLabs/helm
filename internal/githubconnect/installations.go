package githubconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/KanterLabs/helm/internal/githubprovider"
)

const (
	maxInstallations = 20
	reposPerPage     = 100
	maxRepoPages     = 3
)

var errUnauthorized = errors.New("github connection: unauthorized")

// Sync reads the repositories the App's installations reach, applies the
// operator ceiling and stores the result. A 401 is reported to the refresh
// logic and retried once with a renewed token.
func (m *Manager) Sync(ctx context.Context) error {
	m.mu.Lock()
	call := m.syncing
	if call != nil { // share the run already in flight
		m.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return fail(CodeGitHubUnreachable)
		}
	}
	call = &syncCall{done: make(chan struct{})}
	m.syncing = call
	m.mu.Unlock()
	call.err = m.sync(ctx)
	m.mu.Lock()
	m.syncing = nil
	m.mu.Unlock()
	close(call.done)
	return call.err
}

// syncCall is one sync that every concurrent caller shares.
type syncCall struct {
	done chan struct{}
	err  error
}

func (m *Manager) sync(ctx context.Context) error {
	for try := 0; try < 2; try++ {
		token, identity, err := m.Fresh(ctx)
		if err != nil {
			switch githubprovider.ErrorCode(err) {
			case "not_configured":
				return fail(CodeNotConnected)
			case "reconnect_required":
				return fail(CodeReconnectRequired)
			}
			return fail(CodeGitHubUnreachable)
		}
		list, truncated, err := m.listRepositories(ctx, token)
		if errors.Is(err, errUnauthorized) {
			m.Rejected(token)
			continue
		}
		if err != nil {
			return err
		}
		return m.storeRepositories(identity, list, truncated)
	}
	return fail(CodeReconnectRequired)
}

func (m *Manager) storeRepositories(identity string, list []string, truncated bool) error {
	m.mu.Lock()
	if m.rec == nil {
		m.mu.Unlock()
		return fail(CodeNotConnected)
	}
	if m.rec.ID != identity || m.endedLocked() {
		m.mu.Unlock()
		return fail(CodeReconnectRequired)
	}
	next := *m.rec
	next.Repositories, next.RepositoriesTruncated, next.RepositoriesCheckedAt = list, truncated, m.now()
	if err := m.saveLocked(&next); err != nil {
		m.mu.Unlock()
		return err
	}
	m.rec, m.unsaved = &next, false
	m.mu.Unlock()
	m.publish()
	return nil
}

// listRepositories walks up to 20 installations and 3 pages of 100
// repositories each. The result is normalized, deduplicated, limited to the
// ceiling, sorted and capped at 50; truncated says something was left out.
func (m *Manager) listRepositories(ctx context.Context, token string) ([]string, bool, error) {
	var installations struct {
		TotalCount    int `json:"total_count"`
		Installations []struct {
			ID int64 `json:"id"`
		} `json:"installations"`
	}
	path := fmt.Sprintf("/user/installations?per_page=%d", maxInstallations)
	if err := m.getList(ctx, path, token, &installations); err != nil {
		return nil, false, err
	}
	truncated := installations.TotalCount > maxInstallations || len(installations.Installations) > maxInstallations
	seen := map[string]bool{}
	for i, inst := range installations.Installations {
		if i == maxInstallations {
			break
		}
		if inst.ID < 1 {
			return nil, false, fail(CodeGitHubUnreachable)
		}
		fetched := 0
		for page := 1; page <= maxRepoPages; page++ {
			var repos struct {
				TotalCount   int `json:"total_count"`
				Repositories []struct {
					FullName string `json:"full_name"`
				} `json:"repositories"`
			}
			path := fmt.Sprintf("/user/installations/%d/repositories?per_page=%d&page=%d", inst.ID, reposPerPage, page)
			if err := m.getList(ctx, path, token, &repos); err != nil {
				return nil, false, err
			}
			for _, repo := range repos.Repositories {
				name, err := githubprovider.NormalizeRepository(repo.FullName)
				if err == nil && (m.ceiling == nil || m.ceiling[name]) {
					seen[name] = true
				}
			}
			fetched += len(repos.Repositories)
			if len(repos.Repositories) < reposPerPage || fetched >= repos.TotalCount {
				break
			}
			if page == maxRepoPages {
				truncated = true
			}
		}
	}
	list := make([]string, 0, len(seen))
	for name := range seen {
		list = append(list, name)
	}
	slices.Sort(list)
	if len(list) > maxRepositories {
		list, truncated = list[:maxRepositories], true
	}
	return list, truncated, nil
}

func (m *Manager) getList(ctx context.Context, path, token string, into any) error {
	r, err := m.getAPI(ctx, path, token, maxListBody)
	if err != nil {
		return err
	}
	switch {
	case r.status == http.StatusUnauthorized:
		return errUnauthorized
	case r.status >= 400 && r.status < 500:
		return fail(CodeGitHubRefused)
	case r.status != http.StatusOK:
		return fail(CodeGitHubUnreachable)
	}
	if json.Unmarshal(r.body, into) != nil {
		return fail(CodeGitHubUnreachable)
	}
	return nil
}
