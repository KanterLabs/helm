import { expect, type APIRequestContext, type APIResponse } from '@playwright/test';

// Synthetic API setup shared by the board-control user journeys.
type Project = { id: string; key: string; slug: string };
type Column = { id: string; name: string; semantic_state: string };
export type Task = { id: string; key: string; title: string; column_id: string; position: number; version: number; priority: string };
type Collection<T> = { data: T[] };
const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
export const headers = (version?: number) => ({
  Origin: origin,
  'Content-Type': 'application/json',
  'Idempotency-Key': `board-controls-${crypto.randomUUID()}`,
  ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
});

export async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), `Fixture/API request returned ${response.status()}`).toBeTruthy();
  return response.json() as Promise<T>;
}

export async function fixture(request: APIRequestContext) {
  const status = await json<{ mode: string }>(await request.get('/api/v1/auth/status'));
  expect(status.mode, 'Use the documented auth-disabled E2E server').toBe('disabled');
  const suffix = Date.now().toString(36).toUpperCase() + Math.random().toString(36).slice(2, 5).toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    headers: headers(), data: { key: `CTRL${suffix}`.slice(0, 16), name: `Board controls ${suffix}`, description: 'Synthetic mobile and keyboard browser evidence.' }
  }));
  const response = await json<Collection<Column> | Column[]>(await request.get(`/api/v1/projects/${project.id}/columns?limit=20`));
  const columns = Array.isArray(response) ? response : response.data;
  const ready = columns.find((column) => column.semantic_state === 'ready')!;
  const backlog = columns.find((column) => column.semantic_state === 'backlog')!;
  expect(ready).toBeTruthy();
  expect(backlog).toBeTruthy();
  const tasks: Task[] = [];
  for (let index = 0; index < 3; index += 1) {
    tasks.push(await json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
      headers: headers(),
      data: {
        column_id: ready.id,
        title: index === 1 ? `A long synthetic task title that must leave Actions reachable on every phone ${suffix}` : `Synthetic neighbor ${index} ${suffix}`,
        description: index === 1 ? 'Long synthetic metadata and a detailed description exercise compact uniform card heights without exposing user content.' : '',
        priority: 'normal'
      }
    })));
  }
  // Left/right commands follow physical adjacency; Bugs now sits before Ready.
  const leftOfReady = columns[columns.findIndex(column => column.id === ready.id) - 1];
  return { project, ready, backlog, leftOfReady, tasks, moving: tasks[1] };
}

export async function tasksFor(request: APIRequestContext, project: Project) {
  const response = await json<Collection<Task>>(await request.get(`/api/v1/projects/${project.id}/tasks?limit=100`));
  return response.data;
}
