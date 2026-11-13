package forgeconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Repository is one repository the connected account can read.
type Repository struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
	Archived bool   `json:"archived"`
}

// Check refreshes if needed, re-reads the identity, reruns the limit check,
// counts repositories, records the outcome and returns the status.
func (m *Manager) Check(ctx context.Context) Status {
	if !m.enabled {
		return m.Status()
	}
	m.mu.Lock()
	connected, gen := m.stateLocked() == StateConnected, m.gen
	m.mu.Unlock()
	if !connected {
		return m.Status()
	}
	result, account, access, count, hasCount := m.runCheck(ctx)
	m.record(gen, result, account, access, count, hasCount)
	return m.Status()
}

// record stores a check outcome unless the connection changed meanwhile.
func (m *Manager) record(gen uint64, result CheckResult, account Account, access string, count int, hasCount bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gen != gen || m.rec == nil {
		return
	}
	next := *m.rec
	next.CheckedAt = m.now()
	if result.State == CheckOK {
		next.Account, next.Access = account, access
		if hasCount {
			m.repoCount = count
		}
	}
	if err := m.store.save(&next); err != nil && result.State == CheckOK {
		result.Code = Code(err)
	}
	m.rec, m.check = &next, result
}

func checkFailure(err error) CheckResult {
	code := Code(err)
	if code == "" {
		code = CodeServerUnreachable
	}
	if code == CodeReconnectRequired {
		return CheckResult{State: CheckUnauthorized, Code: code}
	}
	return CheckResult{State: CheckUnreachable, Code: code}
}

func (m *Manager) runCheck(ctx context.Context) (result CheckResult, account Account, access string, count int, hasCount bool) {
	r, err := m.apiGet(ctx, gitea.UserPath, maxBody)
	if err != nil {
		return checkFailure(err), account, "", 0, false
	}
	account, ok := parseAccount(r)
	if !ok {
		return checkFailure(fail(CodeServerUnreachable)), account, "", 0, false
	}
	if r, err = m.apiGet(ctx, gitea.LimitCheckPath, maxBody); err != nil {
		return checkFailure(err), account, "", 0, false
	}
	access = limitStatus(r.status)
	if r, err = m.apiGet(ctx, gitea.ReposPath+"?limit=1", maxBody); err != nil {
		return checkFailure(err), account, "", 0, false
	}
	if r.status == http.StatusOK {
		count, hasCount = totalCount(r.header)
	}
	return CheckResult{State: CheckOK}, account, access, count, hasCount
}

// Repositories lists what the account can read, 50 per page, at most 10
// pages; truncated means there were more.
func (m *Manager) Repositories(ctx context.Context) (list []Repository, truncated bool, err error) {
	size := gitea.RepoPageSize
	list = []Repository{}
	for page := 1; page <= gitea.MaxRepoPages; page++ {
		r, err := m.apiGet(ctx, fmt.Sprintf("%s?limit=%d&page=%d", gitea.ReposPath, size, page), maxRepoPage)
		if err != nil {
			return nil, false, err
		}
		var items []Repository
		if r.status != http.StatusOK || json.Unmarshal(r.body, &items) != nil {
			return nil, false, fail(CodeServerUnreachable)
		}
		for _, item := range items {
			if item.FullName != "" && len(item.FullName) <= 512 {
				list = append(list, item)
			}
		}
		total, hasTotal := totalCount(r.header)
		if len(items) < size || hasTotal && page*size >= total {
			return list, false, nil
		}
		if page == gitea.MaxRepoPages {
			return list, !hasTotal || total > page*size, nil
		}
	}
	return list, false, nil
}

// Disconnect deletes the state file. The grant stays on the forge until the
// user revokes it at RevokeURL; Gitea has no revocation endpoint.
func (m *Manager) Disconnect(actorID string) error {
	_ = actorID // recorded by the caller's audit trail, not here
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store != nil {
		if err := m.store.remove(); err != nil {
			return err
		}
	}
	m.rec, m.broken, m.rejected, m.lastRefresh = nil, false, false, time.Time{}
	m.gen++
	m.repoCount, m.check = 0, CheckResult{}
	m.logf(`{"level":"info","msg":"gitea disconnected"}`)
	return nil
}
