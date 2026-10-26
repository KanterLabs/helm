import type {
  CodexAccountStatus,
  CodexDeviceLogin,
  Collection,
  LunaRun,
  LunaRunDetail,
  ProjectIntelligenceResponse,
  TaskDraftSuggestion
} from '../types';
import { collectionFrom, pathWithQuery, request } from './client';

export const intelligenceApi = {
  codexAccount: (refresh = false) => request<CodexAccountStatus>(pathWithQuery('/codex/account', { refresh })),

  startCodexLogin: () => request<CodexDeviceLogin>('/codex/login', { method: 'POST' }),

  cancelCodexLogin: (loginId: string) => request<{ status: string }>('/codex/login/cancel', { method: 'POST', body: { login_id: loginId } }),

  logoutCodex: () => request<{ ok: boolean }>('/codex/logout', { method: 'POST' }),

  listLunaRuns: (limit = 25) => request<Collection<LunaRun>>(pathWithQuery('/codex/runs', { limit })).then(collectionFrom),

  getLunaRun: (id: string) => request<LunaRunDetail>(`/codex/runs/${encodeURIComponent(id)}`),

  projectIntelligence: () => request<ProjectIntelligenceResponse>('/project-intelligence'),

  analyzeProjectIntelligence: (signal?: AbortSignal) => request<ProjectIntelligenceResponse>('/project-intelligence/analyze', {
    method: 'POST',
    signal
  }),

  draftTask: (project: string, query: string, signal?: AbortSignal) => request<TaskDraftSuggestion>(`/projects/${encodeURIComponent(project)}/task-draft`, { method: 'POST', body: { query }, signal })
};
