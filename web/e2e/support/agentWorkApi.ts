// API helpers and read-boundary payload rewriters for the agent-work journey.
import { expect, type APIRequestContext, type APIResponse } from '@playwright/test';

export type Project = {
  id: string;
  key: string;
  name: string;
  slug: string;
};

export type Column = {
  id: string;
  name: string;
  semantic_state: string;
  position: number;
};

export type AgentWork = {
  operation_id: string;
  actor_id: string;
  state: 'working' | 'waiting' | 'verifying' | 'handoff';
  phase: string;
  summary: string;
  next_action: string;
  checkpoint_refs: string[];
  checkpoint_completed?: number;
  checkpoint_total?: number;
  started_at: string;
  updated_at: string;
  stale?: boolean;
  action_needed?: boolean;
};

export type Task = {
  id: string;
  key: string;
  title: string;
  project_id: string;
  column_id: string;
  priority: string;
  version: number;
  agent_work?: AgentWork | null;
};

export type Collection<T> = {
  data: T[];
  next_cursor?: string | null;
};

const e2eOrigin = new URL(process.env.HELM_E2E_BASE_URL || process.env.ROADMAP_E2E_BASE_URL || 'http://127.0.0.1:18080').origin;

export function etagForVersion(version: number): string {
  return `"v${version}"`;
}

export async function assertJSON<T>(response: APIResponse, description: string): Promise<T> {
  expect(response.ok(), `${description} returned HTTP ${response.status()}`).toBeTruthy();
  return await response.json() as T;
}

export async function getJSON<T>(request: APIRequestContext, path: string): Promise<T> {
  return assertJSON<T>(await request.get(path), `GET ${path}`);
}

export async function postJSON<T>(
  request: APIRequestContext,
  path: string,
  body: unknown,
  headers: Record<string, string> = {}
): Promise<T> {
  return assertJSON<T>(await request.post(path, {
    data: body,
    headers: {
      'Content-Type': 'application/json',
      Origin: e2eOrigin,
      ...headers
    }
  }), `POST ${path}`);
}

export function collectionData<T>(payload: Collection<T> | T[]): T[] {
  return Array.isArray(payload) ? payload : payload.data;
}

export function columnFor(columns: Column[], semanticState: string): Column {
  const column = columns.find((item) => item.semantic_state === semanticState);
  expect(column, `the project should have a ${semanticState} column`).toBeTruthy();
  return column as Column;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * The public API intentionally has no clock-setting endpoint. The browser
 * workflow publishes the pulses through the real API, then ages read
 * responses at the API boundary so stale rendering is deterministic without
 * a fifteen-minute sleep or a database-specific test hook.
 */
export function ageTaskInPayload(value: unknown, taskID: string, updatedAt: string): boolean {
  if (Array.isArray(value)) return value.some((item) => ageTaskInPayload(item, taskID, updatedAt));
  if (!isRecord(value)) return false;

  if (value.id === taskID && isRecord(value.agent_work)) {
    value.agent_work = {
      ...value.agent_work,
      updated_at: updatedAt,
      stale: true,
      action_needed: true
    };
    return true;
  }

  return Array.isArray(value.data) && value.data.some((item) => ageTaskInPayload(item, taskID, updatedAt));
}

export function expireTaskClaimInPayload(value: unknown, taskID: string, expiresAt: string): boolean {
  if (Array.isArray(value)) return value.some((item) => expireTaskClaimInPayload(item, taskID, expiresAt));
  if (!isRecord(value)) return false;

  if (value.id === taskID) {
    value.claim_expires_at = expiresAt;
    return true;
  }

  return Array.isArray(value.data) && value.data.some((item) => expireTaskClaimInPayload(item, taskID, expiresAt));
}
