import { expect, type APIRequestContext, type APIResponse, type TestInfo } from '@playwright/test';

export type Task = { id: string; key: string; version: number; title: string };
export type Project = { id: string; slug: string; name: string };
export type PullRequest = {
  id: string; task_id: string; provider: 'github' | 'gitea'; host: string;
  repository: string; number: number; url: string; created_at: string;
};
export const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;
export const headers = (version?: number) => ({
  Origin: origin, 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID(),
  ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
});
export const endpoint = (task: Task) => `/api/v1/tasks/${task.id}/pull-requests`;
export async function json<T>(response: APIResponse, status = 200): Promise<T> {
  expect(response.status(), await response.text()).toBe(status);
  return response.json() as Promise<T>;
}
export async function fixture(request: APIRequestContext, label: string) {
  const suffix = crypto.randomUUID().slice(0, 8).toUpperCase();
  const project = await json<Project>(await request.post('/api/v1/projects', {
    headers: headers(), data: { key: `PR${suffix}`, name: `Synthetic PR links ${label} ${suffix}` }
  }), 201);
  const createTask = async (title: string) => json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    headers: headers(), data: { title }
  }), 201);
  return { project, task: await createTask(`Synthetic ${label} pull request task`), createTask };
}
export async function currentTask(request: APIRequestContext, task: Task): Promise<Task> {
  return json<Task>(await request.get(`/api/v1/tasks/${task.id}`));
}
export async function links(request: APIRequestContext, task: Task): Promise<PullRequest[]> {
  return (await json<{ items: PullRequest[] }>(await request.get(endpoint(task)))).items;
}
export async function link(request: APIRequestContext, task: Task, url: string) {
  return json<{ link: PullRequest; task: Task }>(await request.post(endpoint(task), {
    headers: headers(task.version), data: { url }
  }), 201);
}
export async function receipt(info: TestInfo, name: string, value: unknown) {
  await info.attach(`${name}.json`, { body: JSON.stringify(value, null, 2), contentType: 'application/json' });
}
