import { expect, type Page, type Route } from '@playwright/test';

type BoardFixture = {
  project: {
    id: string;
    key: string;
    slug: string;
    name: string;
    description: string;
    color: string;
    favorite: boolean;
  };
  otherProject?: BoardFixture['project'];
  otherColumns?: BoardFixture['columns'];
  columns: Array<{
    id: string;
    project_id: string;
    name: string;
    semantic_state: 'backlog' | 'ready' | 'active' | 'blocked' | 'completed';
    position: number;
  }>;
  task: Record<string, any>;
  labels: Array<{ id: string; project_id: string; name: string; color: string }>;
  deleted: boolean;
  conflictNextPatch: boolean;
  conflictNextRestore: boolean;
  restoreCalls: number;
  revokedToken: boolean;
};

export function columns(count: number, projectId: string) {
  const states: BoardFixture['columns'][number]['semantic_state'][] = ['backlog', 'ready', 'active', 'blocked', 'completed'];
  return Array.from({ length: count }, (_, index) => ({
    id: `column-${index + 1}`,
    project_id: projectId,
    name: `Column ${index + 1}`,
    semantic_state: states[index] || 'ready',
    position: index
  }));
}

export function fixture(columnCount = 8): BoardFixture {
  const projectId = 'project-a11y';
  const boardColumns = columns(columnCount, projectId);
  const label = { id: 'label-a11y', project_id: projectId, name: 'Accessibility', color: '#6d5efc' };
  return {
    project: {
      id: projectId,
      key: 'A11Y',
      slug: 'a11y-board',
      name: 'Accessibility board',
      description: 'A mocked board for accessibility regression coverage.',
      color: '#6d5efc',
      favorite: true
    },
    columns: boardColumns,
    task: {
      id: 'task-a11y',
      number: 1,
      key: 'A11Y-1',
      project_id: projectId,
      kind: 'bug',
      column_id: boardColumns[0].id,
      title: 'Keyboard and screen-reader regression',
      description: 'Keep board interactions understandable for every operator.',
      priority: 'normal',
      position: 0,
      version: 1,
      labels: [label],
      claimed_by: { id: 'agent-build', kind: 'agent', name: 'Build bot' },
      claim_expires_at: '2030-01-02T03:04:05Z',
      agent_work: {
        operation_id: 'board-a11y-fixture',
        actor_id: 'agent-build',
        state: 'working',
        phase: 'Regression coverage',
        summary: 'Testing board accessibility.',
        next_action: 'Review the result.',
        checkpoint_refs: ['layout', 'interaction'],
        checkpoint_completed: 1,
        checkpoint_total: 2,
        started_at: '2026-09-03T22:00:00Z',
        updated_at: '2026-09-03T23:00:00Z',
        stale: false,
        action_needed: false
      }
    },
    labels: [label],
    deleted: false,
    conflictNextPatch: false,
    conflictNextRestore: false,
    restoreCalls: 0,
    revokedToken: false
  };
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

export async function installFixture(page: Page, state: BoardFixture) {
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const method = request.method();
    const pathname = decodeURIComponent(new URL(request.url()).pathname);
    const task = state.task;
    if (pathname === '/api/v1/auth/status') {
      await json(route, {
        mode: 'disabled',
        authenticated: true,
        user: { id: 'human-a11y', kind: 'human', name: 'Accessibility tester', admin: true }
      });
      return;
    }
    if (pathname === '/api/v1/projects' && method === 'GET') {
      await json(route, { data: [state.project, ...(state.otherProject ? [state.otherProject] : [])], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${state.project.id}/columns` && method === 'GET') {
      await json(route, { data: state.columns, next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${state.project.id}/labels` && method === 'GET') {
      await json(route, { data: state.labels, next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/projects/${state.project.id}/tasks` && method === 'GET') {
      await json(route, { data: state.deleted ? [] : [task], next_cursor: null });
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}/dependencies` && method === 'GET') {
      await json(route, task.dependency_relations || { prerequisites: [], dependents: [] });
      return;
    }
    if (state.otherProject && pathname === `/api/v1/projects/${state.otherProject.id}/columns` && method === 'GET') {
      await json(route, { data: state.otherColumns || columns(3, state.otherProject.id), next_cursor: null });
      return;
    }
    if (state.otherProject && pathname === `/api/v1/projects/${state.otherProject.id}/labels` && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (state.otherProject && pathname === `/api/v1/projects/${state.otherProject.id}/tasks` && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname === '/api/v1/issues' && method === 'GET') {
      await json(route, { data: state.deleted ? [] : [task], next_cursor: null });
      return;
    }
    if (pathname === '/api/v1/events' && method === 'GET') {
      await json(route, { data: [], next_cursor: null });
      return;
    }
    if (pathname === '/api/v1/agents' && method === 'GET') {
      await json(route, {
        data: state.revokedToken
          ? [{ id: 'agent-build', kind: 'agent', name: 'Build bot', tokens: [] }]
          : [{ id: 'agent-build', kind: 'agent', name: 'Build bot', tokens: [{ id: 'token-a11y', actor_id: 'agent-build', name: 'Browser token', scopes: ['tasks:write'], project_ids: [] }] }],
        next_cursor: null
      });
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}` && method === 'GET') {
      if (state.deleted) {
        await json(route, { error: { code: 'not_found', message: 'task not found', details: {} } }, 404);
      } else {
        await json(route, task, 200);
      }
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}` && method === 'DELETE') {
      const expected = Number(request.headers()['if-match']?.replace(/[^0-9]/g, ''));
      if (state.deleted || expected !== task.version) {
        await json(route, { error: { code: 'stale_task', message: 'Task changed elsewhere.', details: { current: task } } }, 409);
      } else {
        state.deleted = true;
        task.version += 1;
        await route.fulfill({ status: 204, body: '' });
      }
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}/restore` && method === 'POST') {
      state.restoreCalls += 1;
      const expected = Number(request.headers()['if-match']?.replace(/[^0-9]/g, ''));
      if (state.conflictNextRestore || !state.deleted || expected !== task.version) {
        state.conflictNextRestore = false;
        await json(route, { error: { code: 'stale_task', message: 'Task changed elsewhere.', details: { current: task } } }, 409);
      } else {
        state.deleted = false;
        task.version += 1;
        await json(route, task, 200);
      }
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}/move` && method === 'POST') {
      const expected = Number(request.headers()['if-match']?.replace(/[^0-9]/g, ''));
      const input = JSON.parse(request.postData() || '{}') as {
        destination_column_id?: string;
        expected_source_column_id?: string;
      };
      if (
        state.deleted
        || expected !== task.version
        || !input.destination_column_id
        || input.expected_source_column_id !== task.column_id
      ) {
        await json(route, { error: { code: 'stale_task', message: 'Task changed elsewhere.', details: { current: task } } }, 409);
      } else {
        task.column_id = input.destination_column_id;
        task.position = 0;
        task.version += 1;
        await json(route, task, 200);
      }
      return;
    }
    if (pathname === `/api/v1/tasks/${task.id}` && method === 'PATCH') {
      const expected = Number(request.headers()['if-match']?.replace(/[^0-9]/g, ''));
      if (state.conflictNextPatch || state.deleted || expected !== task.version) {
        state.conflictNextPatch = false;
        await json(route, { error: { code: 'stale_task', message: 'Task changed elsewhere.', details: { current: task } } }, 409);
      } else {
        const input = JSON.parse(request.postData() || '{}') as { column_id?: string; position?: number };
        if (input.column_id) task.column_id = input.column_id;
        if (input.position !== undefined) task.position = input.position;
        task.version += 1;
        await json(route, task, 200);
      }
      return;
    }
    if (pathname === `/api/v1/labels/${state.labels[0]?.id || 'label-a11y'}` && method === 'DELETE') {
      state.labels = [];
      task.labels = [];
      await route.fulfill({ status: 204, body: '' });
      return;
    }
    if (pathname === '/api/v1/tokens/token-a11y' && method === 'DELETE') {
      state.revokedToken = true;
      await route.fulfill({ status: 204, body: '' });
      return;
    }
    await json(route, { error: { code: 'not_found', message: `Unhandled fixture route ${method} ${pathname}`, details: {} } }, 404);
  });
}

export async function openBoard(page: Page, state: BoardFixture) {
  await page.goto('/');
  await expect(page.locator('section.board')).toBeVisible();
  await expect(page.locator('section.board .board-column')).toHaveCount(state.columns.length);
}

