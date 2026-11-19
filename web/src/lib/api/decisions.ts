import { request } from './client';
import type { Priority } from '../types';

export type TriageSuggestion = {
  priority: Priority | 'insufficient_context';
  readiness: 'ready' | 'needs_details' | 'needs_decision';
  task_version: number;
};

const connection = '/openai/connection';

export const decisionsApi = {
  openAIConnection: (signal?: AbortSignal) => request<{ connected: boolean }>(connection, { signal }),
  saveOpenAIKey: (apiKey: string, signal: AbortSignal) =>
    request<{ connected: boolean }>(connection, { method: 'PUT', body: { api_key: apiKey }, signal }),
  removeOpenAIKey: (signal: AbortSignal) =>
    request<{ connected: boolean }>(connection, { method: 'DELETE', signal }),
  suggestTaskTriage: (task: string, version: number, signal: AbortSignal) =>
    request<TriageSuggestion>(`/tasks/${encodeURIComponent(task)}/triage-suggestion`, {
      method: 'POST', ifMatch: version, signal
    })
};
