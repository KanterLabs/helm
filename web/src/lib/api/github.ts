import type {
  ArtifactSummary,
  GithubApplyResult,
  GithubAuditEvidence,
  GithubAuditJob,
  GithubAuditStartInput,
  GithubAuditStartResult,
  GithubIntegration,
  GithubReadiness,
  GithubRepositorySelection,
  ProjectGithubSelection,
  PullRequestArtifact,
  TaskArtifactCollection
} from '../types';
import { key, pathWithQuery, request } from './client';

const project = (id: string) => `/projects/${encodeURIComponent(id)}`;
const task = (id: string) => `/tasks/${encodeURIComponent(id)}`;
const audit = (id: string) => `/audits/${encodeURIComponent(id)}`;

/**
 * GitHub beta endpoints. Card/drawer artifact reads and summaries are local;
 * checks and refreshes contact GitHub, and readiness/evidence revalidate access.
 */
export const githubApi = {
  getGithubIntegration: () => request<GithubIntegration>('/integrations/github'),

  checkGithubIntegration: () =>
    request<GithubIntegration>('/integrations/github/check', { method: 'POST', idempotencyKey: key() }),

  getProjectGithub: (projectId: string) => request<ProjectGithubSelection>(`${project(projectId)}/github`),

  putProjectGithub: (projectId: string, repositories: GithubRepositorySelection[], version: number) =>
    request<ProjectGithubSelection>(`${project(projectId)}/github`, {
      method: 'PUT',
      body: { repositories },
      ifMatch: version,
      idempotencyKey: key()
    }),

  getGithubReadiness: (projectId: string) => request<GithubReadiness>(`${project(projectId)}/github/readiness`),

  listTaskArtifacts: (taskId: string) => request<TaskArtifactCollection>(`${task(taskId)}/artifacts`),

  linkTaskArtifact: (taskId: string, url: string, version: number, idempotencyKey = key()) =>
    request<{ artifact: PullRequestArtifact; task_version: number }>(`${task(taskId)}/artifacts`, {
      method: 'POST',
      body: { url },
      ifMatch: version,
      idempotencyKey
    }),

  unlinkTaskArtifact: (taskId: string, artifactId: string, version: number) =>
    request<{ task_version: number }>(`${task(taskId)}/artifacts/${encodeURIComponent(artifactId)}`, {
      method: 'DELETE',
      ifMatch: version,
      idempotencyKey: key()
    }),

  refreshTaskArtifact: (taskId: string, artifactId: string) =>
    request<PullRequestArtifact>(`${task(taskId)}/artifacts/${encodeURIComponent(artifactId)}/refresh`, {
      method: 'POST',
      idempotencyKey: key()
    }),

  /** One batched local read for up to 200 board cards. */
  listArtifactSummaries: (taskIds: string[]) =>
    request<{ data: ArtifactSummary[] }>(pathWithQuery('/artifact-summaries', { task_ids: taskIds.join(',') }), {
      preserveOfflineSnapshotsOnError: true
    }),

  startGithubAudit: (projectId: string, input: GithubAuditStartInput, idempotencyKey = key()) =>
    request<GithubAuditStartResult>(`${project(projectId)}/github-audits`, { method: 'POST', body: input, idempotencyKey }),

  getAuditJob: (auditId: string) => request<GithubAuditJob>(`${audit(auditId)}/job`),

  cancelAuditJob: (auditId: string) =>
    request<GithubAuditJob>(`${audit(auditId)}/cancel`, { method: 'POST', idempotencyKey: key() }),

  retryAuditJob: (auditId: string, idempotencyKey = key()) =>
    request<GithubAuditStartResult>(`${audit(auditId)}/retry`, { method: 'POST', idempotencyKey }),

  getAuditEvidence: (auditId: string) => request<GithubAuditEvidence>(`${audit(auditId)}/evidence`),

  applyAuditFindings: (auditId: string, findingIds: string[], preview: boolean) =>
    request<{ data: GithubApplyResult[] }>(`${audit(auditId)}/apply`, {
      method: 'POST',
      body: { finding_ids: findingIds, preview },
      idempotencyKey: key()
    })
};
