import type {
  Actor,
  Agent,
  AgentNote,
  ApiToken,
  AuthStatus,
  BetaBuildsResponse,
  BetaSwitchJob,
  BetaSwitchResponse,
  Collection
} from '../types';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

export const adminApi = {
  authStatus: (signal?: AbortSignal) => request<AuthStatus>('/auth/status', { signal }),

  authSetup: (input: { name: string; email: string; password: string }) =>
    request<Actor | { user: Actor }>('/auth/setup', { method: 'POST', body: input }),

  authLogin: (input: { email: string; password: string }) =>
    request<Actor | { user: Actor }>('/auth/login', { method: 'POST', body: input }),

  authLogout: () => request<{ ok: boolean }>('/auth/logout', { method: 'POST' }),

  authMe: (signal?: AbortSignal) => request<Actor>('/auth/me', { signal }),

  /** Optional owner-only beta controls. A missing/disabled endpoint is hidden by the UI. */
  getBetaBuilds: () => request<BetaBuildsResponse>('/admin/beta/builds', {
    suppressNetworkUnavailable: true,
    preserveOfflineSnapshotsOnError: true
  }),

  switchBetaBuild: (targetSha: string, idempotencyKey = key()) => request<BetaSwitchResponse>('/admin/beta/switch', {
    method: 'POST',
    body: { sha: targetSha },
    idempotencyKey,
    // The beta process intentionally restarts after accepting this request.
    suppressNetworkUnavailable: true
  }).then((payload) => payload.job),

  getBetaSwitchJob: (jobId: string) => request<BetaSwitchJob | { job: BetaSwitchJob }>(`/admin/beta/switches/${encodeURIComponent(jobId)}`, {
    suppressNetworkUnavailable: true
  }).then((payload) => 'job' in payload ? payload.job : payload),

  createAgentNote: (task: string, input: Pick<AgentNote, 'category' | 'body' | 'evidence'>) =>
    request<AgentNote>(`/tasks/${encodeURIComponent(task)}/agent-notes`, {
      method: 'POST', body: input, idempotencyKey: key()
    }),

  listAgents: (params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<Agent> | Agent[]>(pathWithQuery('/agents', { cursor: params.cursor, limit: params.limit })).then(collectionFrom),

  listAllAgents: () => collectPages((cursor) =>
    request<Collection<Agent>>(pathWithQuery('/agents', { cursor, limit: 200 })).then(collectionFrom)
  ),

  createAgent: (input: { name: string; description?: string; project_ids?: string[] }) =>
    request<Agent>('/agents', { method: 'POST', body: input, idempotencyKey: key() }),

  createToken: (
    agent: string,
    input: { name: string; scopes: string[]; project_ids?: string[]; expires_at?: string | null }
  ) =>
    request<ApiToken>(`/agents/${encodeURIComponent(agent)}/tokens`, {
      method: 'POST',
      // Token plaintext is deliberately returned once and never persisted in
      // the idempotency cache, so issuance cannot safely be replayed.
      body: input
    }),

  deleteToken: (token: string) =>
    request<void>(`/tokens/${encodeURIComponent(token)}`, { method: 'DELETE', idempotencyKey: key() })
};
