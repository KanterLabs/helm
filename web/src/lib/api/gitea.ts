import type { GiteaProbeInput, GiteaProbeResult, GiteaRepositoryList, GiteaStartInput, GiteaStatus } from '../types';
import { request } from './client';

const base = '/integrations/gitea';

/**
 * Gitea / Forgejo connection (contract C5). Human administrators only. These
 * routes take no Idempotency-Key: probing and starting save nothing, and the
 * other writes are naturally repeatable.
 */
export const giteaApi = {
  getGiteaConnection: () => request<GiteaStatus>(base),

  probeGitea: (input: GiteaProbeInput, signal?: AbortSignal) =>
    request<GiteaProbeResult>(`${base}/probe`, { method: 'POST', body: input, signal }),

  startGiteaSignIn: (input: GiteaStartInput) =>
    request<{ authorize_url: string }>(`${base}/oauth/start`, { method: 'POST', body: input }),

  checkGiteaConnection: () => request<GiteaStatus>(`${base}/check`, { method: 'POST' }),

  listGiteaRepositories: () => request<GiteaRepositoryList>(`${base}/repositories`),

  disconnectGitea: () => request<GiteaStatus>(`${base}/disconnect`, { method: 'POST' })
};
