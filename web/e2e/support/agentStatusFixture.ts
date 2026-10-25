// API fixtures shared by the real-server agent-status acceptance flow.
import { expect, type APIRequestContext, type APIResponse } from '@playwright/test';

export type Project = { id: string; key: string; name: string; slug: string };
export type Column = { id: string; semantic_state: string };
export type Work = {
  operation_id: string;
  actor_id: string;
  state: 'working' | 'waiting' | 'verifying' | 'handoff';
  updated_at: string;
};
export type Task = {
  id: string; key: string; title: string; version: number;
  claimed_by?: unknown; claim_expires_at?: string; completed_at?: string | null;
  agent_work?: Work;
};
export type Timeline = {
  data: Array<{ id: string; task_id: string; kind: string; created_at: string; progress?: { operation_id: string } }>;
  next_cursor?: string | null;
};
export const origin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

export async function json<T>(response: APIResponse): Promise<T> {
  expect(response.ok(), `HTTP ${response.status()} for ${new URL(response.url()).pathname}`).toBeTruthy();
  return response.json() as Promise<T>;
}

export async function post<T>(request: APIRequestContext, path: string, data: unknown, task?: Task): Promise<T> {
  return json<T>(await request.post(path, {
    data,
    headers: {
      Origin: origin,
      'Idempotency-Key': `agent-status-${crypto.randomUUID()}`,
      ...(task ? { 'If-Match': `"v${task.version}"` } : {})
    }
  }));
}

