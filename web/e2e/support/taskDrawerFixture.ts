import { expect, type APIRequestContext, type APIResponse } from '@playwright/test';

export type Project = { id: string; key: string; name: string; slug: string };
export type Column = { id: string; name: string; semantic_state: string; position: number };
export type Task = {
  id: string;
  key: string;
  title: string;
  project_id: string;
  column_id: string;
  version: number;
  kind?: string;
  bug?: { severity?: string | null; resolution?: string | null } | null;
};
export type Collection<T> = { data: T[]; next_cursor?: string | null };

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

export function mutationHeaders(version?: number, key = `task-drawer-e2e-${crypto.randomUUID()}`): Record<string, string> {
  return {
    Origin: e2eOrigin,
    'Content-Type': 'application/json',
    'Idempotency-Key': key,
    ...(version === undefined ? {} : { 'If-Match': `"v${version}"` })
  };
}

export async function json<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

export function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

export function columnFor(columns: Column[], semanticState: string): Column {
  const column = columns.find((item) => item.semantic_state === semanticState);
  expect(column, `the project should have a ${semanticState} column`).toBeTruthy();
  return column as Column;
}

export async function createProject(request: APIRequestContext, suffix: string): Promise<Project> {
  return json<Project>(await request.post('/api/v1/projects', {
    data: {
      key: `TD${suffix}`.slice(0, 16),
      name: `Task drawer E2E ${suffix}`,
      description: 'Task drawer acceptance fixture.'
    },
    headers: mutationHeaders()
  }), 'create task drawer project');
}

export async function createTask(
  request: APIRequestContext,
  project: Project,
  column: Column,
  title: string,
  extra: Record<string, unknown> = {}
): Promise<Task> {
  return json<Task>(await request.post(`/api/v1/projects/${project.id}/tasks`, {
    data: { title, column_id: column.id, priority: 'normal', ...extra },
    headers: mutationHeaders()
  }), `create ${title}`);
}

export function mutateTaskPayload(value: unknown, taskID: string, update: (task: Record<string, unknown>) => void): boolean {
  if (Array.isArray(value)) return value.some((item) => mutateTaskPayload(item, taskID, update));
  if (!value || typeof value !== 'object') return false;
  const record = value as Record<string, unknown>;
  if (record.id === taskID) {
    update(record);
    return true;
  }
  return Array.isArray(record.data) && record.data.some((item) => mutateTaskPayload(item, taskID, update));
}

export function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

