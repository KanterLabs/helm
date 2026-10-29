import { ApiError, type Collection, type CorrectionRequest, type CorrectionResult, type Task } from '../types';
import { collectionFrom, pathWithQuery, request } from './client';

/** The server's hard batch limit for one audit capture. */
export const AUDIT_SCOPE_LIMIT = 1000;

export interface ScopeCard {
  id: string;
  column_id: string;
}

export interface ScopeCount {
  cards: ScopeCard[];
  /** True when the project holds more cards than one audit can capture. */
  overLimit: boolean;
}

export const columnAuditApi = {
  /**
   * Count every card through server pagination, never the loaded board page.
   * Only identity and column are retained.
   */
  countAuditScope: async (project: string): Promise<ScopeCount> => {
    const cards: ScopeCard[] = [];
    let cursor: string | undefined;
    const visited = new Set<string>();
    // Counting must finish even when All cards exceeds the audit batch limit:
    // a narrower column or selected card can appear on a later project page.
    // Bound malformed or unexpectedly large traversals without claiming a count.
    for (let page = 0; page < 100; page += 1) {
      const result = collectionFrom(await request<Collection<Task> | Task[]>(
        pathWithQuery(`/projects/${encodeURIComponent(project)}/tasks`, { cursor, limit: 200 })
      ));
      cards.push(...result.data.map((task) => ({ id: task.id, column_id: task.column_id })));
      if (!result.next_cursor) return { cards, overLimit: cards.length > AUDIT_SCOPE_LIMIT };
      if (visited.has(result.next_cursor)) throw new ApiError('The server repeated a pagination cursor.', 500, 'invalid_pagination_cursor');
      visited.add(result.next_cursor);
      cursor = result.next_cursor;
    }
    throw new ApiError('The full card scope could not be counted safely. No audit was started.', 500, 'audit_scope_count_limit');
  },

  /**
   * v2 correction preview (read-only) or final apply. Final apply needs one
   * logical-batch key; a transport loss leaves the outcome unknown, so the
   * caller retries the identical body and key. The app is not forced offline
   * by this one lost response: the review owns that recovery.
   */
  correctAuditFindings: (audit: string, body: CorrectionRequest, idempotencyKey?: string) =>
    request<{ data: CorrectionResult[] }>(`/audits/${encodeURIComponent(audit)}/apply`, {
      method: 'POST',
      body,
      idempotencyKey,
      suppressNetworkUnavailable: true
    })
};
