import type { AuditDetail, AuditFinding, AuditFindingPatch, AuditRun, AuditTerminalStatus, Collection } from '../types';
import { collectPages, collectionFrom, key, pathWithQuery, request } from './client';

export const auditsApi = {
  /** List audit runs for one project. Runs are read-only until a finding is explicitly reviewed. */
  listAudits: (project: string, params: { cursor?: string; limit?: number } = {}) =>
    request<Collection<AuditRun> | AuditRun[]>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/audits`, {
        cursor: params.cursor,
        limit: params.limit ?? 100
      })
    ).then(collectionFrom),

  listAllAudits: (project: string) => collectPages((cursor) =>
    request<Collection<AuditRun> | AuditRun[]>(
      pathWithQuery(`/projects/${encodeURIComponent(project)}/audits`, { cursor, limit: 200 })
    ).then(collectionFrom)
  ),

  /** Start an audit run. Starting a run never applies a proposed move. */
  createAudit: (project: string, input: { scope?: string; status?: 'queued' | 'running' } = {}) =>
    request<AuditRun>(`/projects/${encodeURIComponent(project)}/audits`, {
      method: 'POST',
      body: input,
      idempotencyKey: key()
    }),

  getAudit: async (audit: string): Promise<AuditDetail> => {
    const run = await request<AuditRun>(`/audits/${encodeURIComponent(audit)}`);
    const findings = await collectPages((cursor) =>
      request<Collection<AuditFinding> | AuditFinding[]>(
        pathWithQuery(`/audits/${encodeURIComponent(audit)}/findings`, { cursor, limit: 200 })
      ).then(collectionFrom)
    );
    return { ...run, findings: findings.data };
  },

  finalizeAudit: (audit: string, status: AuditTerminalStatus = 'complete') =>
    request<AuditRun>(`/audits/${encodeURIComponent(audit)}/finalize`, {
      method: 'POST',
      body: { status },
      idempotencyKey: key()
    }),

  patchAuditFinding: (finding: string, input: AuditFindingPatch, version: number) =>
    request<AuditFinding>(`/audit-findings/${encodeURIComponent(finding)}`, {
      method: 'PATCH',
      body: input,
      ifMatch: version,
      idempotencyKey: key()
    })
};
