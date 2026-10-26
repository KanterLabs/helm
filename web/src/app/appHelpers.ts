// Pure board, bulk, release, issue-filter and beta helpers used by the app
// shell. Kept free of component state so App.test.ts can exercise them.

export type BoardRefreshScope = 'full' | 'targeted';
export type BoardMetadataErrorState = {
  full: string;
  targeted: Readonly<Record<string, string>>;
};
/** Full metadata success clears all uncertainty; targeted refreshes own their columns. */
export function boardMetadataErrorAfterRefresh(
  current: BoardMetadataErrorState,
  scope: BoardRefreshScope,
  columnIds: readonly string[],
  nextError: string
): BoardMetadataErrorState {
  if (scope === 'full') return { full: nextError, targeted: {} };
  const targeted = { ...current.targeted };
  columnIds.forEach((columnId) => {
    if (nextError) targeted[columnId] = nextError;
    else delete targeted[columnId];
  });
  return { full: current.full, targeted };
}

export function boardMetadataErrorMessage(state: BoardMetadataErrorState): string {
  return state.full || Object.values(state.targeted)[0] || '';
}

/** Refresh ownership is per target column, not a global latest-request flag. */
export function boardRefreshTargetsAreCurrent(
  refreshes: Readonly<Record<string, number>>,
  columnIds: readonly string[],
  refreshToken: number
): boolean {
  return columnIds.every((columnId) => refreshes[columnId] === refreshToken);
}

/** Merge only the columns owned by a targeted response; other responses cannot clobber them. */
export function mergeOwnedBoardMetadata<T extends { id: string }>(
  current: readonly T[],
  refreshed: readonly T[],
  ownedColumnIds: readonly string[]
): T[] {
  const owned = new Set(ownedColumnIds);
  const refreshedById = new Map(refreshed.map((column) => [column.id, column]));
  return current.map((column) => owned.has(column.id) ? refreshedById.get(column.id) || column : column);
}

/** Success/reconciliation announcements require both live mutation context and reload success. */
export function boardMutationReloadCanAnnounce(
  mutationIsCurrent: boolean,
  reloadSucceeded: boolean
): boolean {
  return mutationIsCurrent && reloadSucceeded;
}

/** A truncated global event page may hide a newer event for this board. */
export function boardEventPageRequiresRefresh(
  boardChanged: boolean,
  nextCursor: string | null | undefined
): boolean {
  return boardChanged || Boolean(nextCursor);
}

export type BulkMutationRequestContext = {
  requestId: number;
  sessionGeneration: number;
  projectId: string;
  projectSlug: string;
};

/** Delayed bulk responses may update state only while their session/project owns the request. */
export function bulkMutationRequestIsCurrent(
  requested: BulkMutationRequestContext,
  current: BulkMutationRequestContext & { authenticated: boolean }
): boolean {
  return current.authenticated
    && requested.requestId === current.requestId
    && requested.sessionGeneration === current.sessionGeneration
    && requested.projectId === current.projectId
    && requested.projectSlug === current.projectSlug;
}

export type ReleaseAssignmentReference = {
  id: string;
  status?: string;
};

/** Only planned releases may receive new task assignments. */
export function plannedReleaseIdForAssignment(
  requestedId: string | null | undefined,
  releases: readonly ReleaseAssignmentReference[]
): string {
  if (!requestedId) return '';
  return releases.some((release) => release.id === requestedId && release.status === 'planned')
    ? requestedId
    : '';
}

/** Retained board URLs must never send an inaccessible release to task APIs. */
export function validBoardReleaseFilter(
  requestedFilter: string | null | undefined,
  releases: readonly ReleaseAssignmentReference[]
): string {
  if (!requestedFilter || requestedFilter === 'all' || requestedFilter === 'unassigned') return requestedFilter || 'all';
  return releases.some((release) => release.id === requestedFilter) ? requestedFilter : 'all';
}

export function clearedIssueFilterState() {
  return {
    filters: {
      query: '',
      priority: 'all',
      label: 'all',
      assignee: 'all',
      state: 'all',
      kind: 'bug',
      severity: 'all',
      reporter: 'all',
      resolution: 'all'
    },
    project: 'all',
    release: 'all'
  } as const;
}

export function betaShortSha(sha: string): string {
  return sha.slice(0, 7);
}

export function betaBranchLabel(ref: string): string {
  return ref.replace(/^refs\/(heads|tags)\//, '') || 'beta';
}

export function isValidBetaBuildShape(value: unknown): value is { sha: string; ref: string; subject?: string; current?: boolean } {
  if (!value || typeof value !== 'object') return false;
  const build = value as { sha?: unknown; ref?: unknown; subject?: unknown; current?: unknown };
  return typeof build.sha === 'string'
    && build.sha.trim().length > 0
    && /^[0-9a-f]{40}$/.test(build.sha.trim())
    && build.sha.length <= 128
    && typeof build.ref === 'string'
    && build.ref.trim().length > 0
    && build.ref.length <= 256
    && !/[\u0000-\u001f\u007f]/.test(build.ref)
    && (build.subject === undefined || (
      typeof build.subject === 'string'
      && build.subject.trim().length > 0
      && new TextEncoder().encode(build.subject).length <= 160
      && !/[\u0000-\u001f\u007f]/.test(build.subject)
    ))
    && (build.current === undefined || typeof build.current === 'boolean');
}
