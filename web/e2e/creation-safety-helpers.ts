import { expect, type APIRequestContext, type APIResponse, type Page, type TestInfo } from '@playwright/test';

export type Project = { id: string; key: string; name: string; slug: string };
export type Column = { id: string; name: string; semantic_state: string };
export type Task = { id: string; title: string; project_id: string; column_id: string; bug?: { actual_behavior: string }; labels?: { name: string }[] };
export const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
export const headers = () => ({ Origin: origin, 'Idempotency-Key': crypto.randomUUID() });
export const suffix = () => `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`.toUpperCase();
export function gate() {
  let release!: () => void;
  const promise = new Promise<void>((resolve) => { release = resolve; });
  return { promise, release };
}
export async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), `HTTP ${response.status()}`).toBeTruthy();
  return response.json() as Promise<T>;
}
export async function project(request: APIRequestContext): Promise<Project> {
  const id = suffix();
  return json(await request.post('/api/v1/projects', { headers: headers(), data: { key: `CS${id}`.slice(0, 16), name: `Creation safety ${id}` } }));
}
export async function tasks(request: APIRequestContext, destination: Project): Promise<Task[]> {
  const result = await json<{ data: Task[] }>(await request.get(`/api/v1/projects/${destination.id}/tasks?limit=200`));
  return result.data;
}
export async function columns(request: APIRequestContext, destination: Project): Promise<Column[]> {
  return (await json<{ data: Column[] }>(await request.get(`/api/v1/projects/${destination.id}/columns?limit=100`))).data;
}
export async function evidence(page: Page, info: TestInfo, name: string, state: unknown) {
  await info.attach(`${name}.json`, { body: JSON.stringify(state, null, 2), contentType: 'application/json' });
  await info.attach(`${name}.png`, { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
}
export async function taskModal(page: Page) {
  await page.locator('[data-task-modal-trigger]').first().click();
  const modal = page.getByRole('dialog', { name: 'Create a task', exact: true });
  await expect(modal.getByRole('combobox', { name: 'Column', exact: true })).toBeEnabled();
  return modal;
}

