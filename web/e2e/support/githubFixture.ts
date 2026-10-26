import type { Page, Route } from '@playwright/test';
import { createAuditMock } from './githubAuditFixture';
import { API, columns, error, json, project, SHA_API, SHA_WEB, stamp, WEB, type Json } from './githubShared';

// Synthetic GitHub beta API used by pr-links-ui and github-audit-ui specs.
// Every repository, SHA, title and message here is invented test data.
export { API, columns, json, project, SHA_API, SHA_WEB, WEB } from './githubShared';
export { evidenceManifest, serverFindings } from './githubAuditFixture';

const task = (n: number, column: string, title: string, version: number): Json => ({
  id: `task-${n}`, number: n, key: `GHB-${n}`, project_id: project.id, column_id: column, title,
  description: 'Acceptance: synthetic criteria.', priority: 'normal', position: n, version,
  created_at: stamp, updated_at: stamp
});

function snapshot(title: string, state: string, checks: string, extra: Json = {}): Json {
  const list = checks === 'failing'
    ? [{ name: 'web-e2e', state: 'completed', conclusion: 'failure', url: 'https://github.com/demo-org/helm-web/runs/9001', sha: SHA_WEB },
      { name: 'lint', state: 'completed', conclusion: 'success', url: 'https://github.com/demo-org/helm-web/runs/9002', sha: SHA_WEB }]
    : [{ name: 'build', state: 'completed', conclusion: 'success', url: 'https://github.com/demo-org/helm-api/runs/8001', sha: SHA_API }];
  return {
    repository_id: 101, title, state, draft: false, head_sha: checks === 'failing' ? SHA_WEB : SHA_API, base_sha: 'aaaa111',
    checks_state: checks, checks: list, review_state: 'approved', fetched_at: new Date(Date.now() - 4 * 60_000).toISOString(),
    stale: false, partial: false, ...extra
  };
}

function artifact(id: string, taskId: string, repo: string, number: number, extra: Json = {}): Json {
  return {
    id, task_id: taskId, kind: 'pull_request', provider: 'github', url: `https://github.com/${repo}/pull/${number}`,
    repository: repo, number, created_at: stamp, verification: 'verified', ...extra
  };
}

export type GithubMockOptions = {
  integrationState?: string;
  readiness?: { github: string; ai: string };
  tasksOffBoard?: boolean;
  evidenceError?: boolean;
  slowRefresh?: boolean;
};

export type GithubMockState = {
  requests: string[];
  writes: Array<{ method: string; path: string; body: any; ifMatch: string | null; key: string | null }>;
  tasks: Json[];
  artifacts: Map<string, Json[]>;
  jobs: Map<string, Json>;
  releaseRefresh: () => void;
};

function summaryFor(taskId: string, list: Json[]): Json {
  const rank: string[] = ['failing', 'pending', 'unknown', 'unverified', 'unavailable', 'passing', 'merged', 'closed'];
  const states: string[] = list.map((item) => item.verification === 'unverified' ? 'unverified'
    : item.verification === 'unavailable' ? 'unavailable'
      : item.snapshot?.checks_state === 'failing' ? 'failing'
        : item.snapshot?.checks_state === 'pending' ? 'pending'
          : item.snapshot?.state === 'merged' ? 'merged' : item.snapshot?.checks_state === 'passing' ? 'passing' : 'unknown');
  const state = rank.find((candidate) => states.includes(candidate)) || 'unknown';
  return { task_id: taskId, count: list.length, label: list.length === 1 ? `#${list[0].number}` : `${list.length} PRs`, state };
}

export async function installGithubMocks(page: Page, options: GithubMockOptions = {}): Promise<GithubMockState> {
  const tasks = [
    task(1, 'col-active', 'Link PRs to tasks', 3),
    task(2, 'col-backlog', 'Show verified PR status', 2),
    task(3, 'col-active', 'Compact board toolbar', 1),
    task(4, 'col-ready', 'Unverified link example', 1)
  ];
  const offPage = task(77, 'col-backlog', 'Off-page task from page 51', 1);
  const artifacts = new Map<string, Json[]>([
    ['task-1', [
      artifact('art-418', 'task-1', WEB, 418, { snapshot: snapshot('Task drawer: Pull requests section', 'open', 'failing') }),
      artifact('art-212', 'task-1', API, 212, { snapshot: snapshot('Store task to PR links', 'merged', 'passing', { merge_sha: SHA_API }) })
    ]],
    ['task-3', [artifact('art-401', 'task-3', WEB, 401, { snapshot: snapshot('Toolbar: one row', 'merged', 'failing', { stale: true, partial: true }) })]],
    ['task-4', [artifact('art-300', 'task-4', WEB, 300, { verification: 'unavailable', error_code: 'github_permission_required', error_message: 'GitHub access to this repository was revoked.' })]]
  ]);
  let selection = { version: 1, repositories: [{ repository: WEB, ref: 'main' }] };
  let held: Array<() => void> = [];
  let nextArtifact = 1;
  const audits = createAuditMock({ findTask: (id) => findTask(id), options });
  const jobs = audits.jobs;
  const state: GithubMockState = {
    requests: [], writes: [], tasks, artifacts, jobs,
    releaseRefresh: () => { held.forEach((release) => release()); held = []; }
  };
  const findTask = (id: string) => tasks.find((item) => item.id === id) || (id === offPage.id ? offPage : undefined);
  const versionOk = (route: Route, current: Json): boolean => {
    const header = route.request().headers()['if-match'];
    if (header === `"v${current.version}"`) return true;
    void error(route, 412, 'stale_task', 'This task changed. Refresh and try again.', { current });
    return false;
  };

  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace('/api/v1', '');
    const method = request.method();
    state.requests.push(`${method} ${path}${url.search}`);
    const body = request.postData() ? request.postDataJSON() : undefined;
    if (method !== 'GET') state.writes.push({ method, path, body, ifMatch: request.headers()['if-match'] || null, key: request.headers()['idempotency-key'] || null });
    const parts = path.split('/').filter(Boolean).map(decodeURIComponent);

    if (path === '/auth/status') {
      const actor = { id: 'human-gh', kind: 'human', name: 'Synthetic reviewer', admin: true };
      return json(route, { mode: 'disabled', configured: true, setup_required: false, authenticated: true, actor, user: actor });
    }
    if (path === '/projects') return json(route, { data: [project], next_cursor: null });
    if (path === `/projects/${project.id}/columns`) return json(route, { data: columns, next_cursor: null });
    if (path === `/projects/${project.id}/tasks`) {
      const column = url.searchParams.get('column');
      const visible = options.tasksOffBoard ? tasks.filter((item) => item.id !== 'task-2') : tasks;
      return json(route, { data: visible.filter((item) => !column || item.column_id === column), next_cursor: null });
    }
    if (['labels', 'releases', 'boards'].includes(parts[2] || '') && parts[0] === 'projects') return json(route, { data: [], next_cursor: null });
    if (path === '/events' || path === '/notifications') return json(route, { data: [], next_cursor: null });
    // Unrelated drawer panels: empty synthetic responses keep screenshots clean.
    if (parts[0] === 'tasks' && parts[2] === 'dependencies') return json(route, { prerequisites: [], dependents: [] });
    if (parts[0] === 'tasks' && ['watches', 'watch', 'events', 'activity', 'comments', 'children', 'checklist'].includes(parts[2] || '')) return json(route, { data: [], next_cursor: null });
    if (path === '/watches') return json(route, { data: [], next_cursor: null });

    if (path === '/artifact-summaries') {
      const ids = (url.searchParams.get('task_ids') || '').split(',').filter(Boolean);
      return json(route, { data: ids.filter((id) => (artifacts.get(id) || []).length).map((id) => summaryFor(id, artifacts.get(id) || [])) });
    }
    if (parts[0] === 'tasks' && parts.length === 2 && method === 'GET') {
      const found = findTask(parts[1]);
      return found ? json(route, found) : error(route, 404, 'not_found', 'Task not found');
    }
    if (parts[0] === 'tasks' && parts[2] === 'artifacts') {
      const current = findTask(parts[1]);
      if (!current) return error(route, 404, 'not_found', 'Task not found');
      const list = artifacts.get(current.id) || [];
      if (parts.length === 3 && method === 'GET') return json(route, { data: list, next_cursor: '', task_version: current.version });
      if (parts.length === 3 && method === 'POST') {
        if (!versionOk(route, current)) return;
        const match = String(body?.url || '').match(/github\.com\/([^/]+\/[^/]+)\/pull\/(\d+)/);
        if (!match || ![WEB, API].includes(match[1])) return error(route, 422, 'repository_not_selected', 'That repository is not selected for this project.');
        const created = artifact(`art-new-${nextArtifact++}`, current.id, match[1], Number(match[2]), { verification: 'unverified' });
        artifacts.set(current.id, [...list, created]);
        current.version += 1;
        return json(route, { artifact: created, task_version: current.version }, 201);
      }
      const target = list.find((item) => item.id === parts[3]);
      if (!target) return error(route, 404, 'not_found', 'Artifact not found');
      if (parts[4] === 'refresh' && method === 'POST') {
        if (options.slowRefresh) await new Promise<void>((resolve) => held.push(resolve));
        if (target.number === 999) Object.assign(target, { verification: 'unverified', error_code: 'github_unavailable', error_message: 'GitHub did not respond. Try again later.' });
        else Object.assign(target, { verification: 'verified', error_code: undefined, error_message: undefined, snapshot: { ...snapshot(target.number === 215 ? 'Expose verified PR status' : 'Synthetic change', 'open', 'pending'), fetched_at: new Date().toISOString() } });
        return json(route, target);
      }
      if (parts.length === 4 && method === 'DELETE') {
        if (!versionOk(route, current)) return;
        artifacts.set(current.id, list.filter((item) => item.id !== target.id));
        current.version += 1;
        return json(route, { task_version: current.version });
      }
    }

    if (path === '/integrations/github' || path === '/integrations/github/check') {
      const integrationState = options.integrationState || 'connected';
      return json(route, {
        enabled: integrationState !== 'not_configured', configured: integrationState !== 'not_configured', state: integrationState,
        message: integrationState === 'connected' ? 'Token can read the selected repositories.' : 'Synthetic integration message.',
        allowed_repositories: [WEB, API], checked_at: method === 'POST' ? new Date().toISOString() : stamp
      });
    }
    if (path === `/projects/${project.id}/github` && method === 'GET') return json(route, selection);
    if (path === `/projects/${project.id}/github` && method === 'PUT') {
      if (request.headers()['if-match'] !== `"v${selection.version}"`) return error(route, 412, 'version_conflict', 'The selection changed.', { current: selection });
      selection = { version: selection.version + 1, repositories: body.repositories };
      return json(route, selection);
    }
    if (path === `/projects/${project.id}/github/readiness`) {
      const readiness = options.readiness || { github: 'connected', ai: 'connected' };
      return json(route, {
        ready: readiness.github === 'connected' && readiness.ai === 'connected',
        github: { state: readiness.github, message: readiness.github === 'connected' ? 'GitHub access is ready.' : 'The GitHub token lacks access.' },
        ai: { state: readiness.ai, message: readiness.ai === 'connected' ? 'Your AI connection is ready.' : 'Connect your AI account in Settings.' },
        repositories: selection.repositories
      });
    }
    const handled = audits.handle(route, method, path, parts, body);
    if (handled) return handled;
    return error(route, 404, 'mock_route_missing', `Unhandled mock route ${method} ${path}`);
  });

  return state;
}
