// Loading board metadata and column pages.
import { api } from '../../lib/api';
import { boardColumnRequestIsCurrent, isRecoverableBoardCursorConflict } from '../../lib/boardOrdering';
import { mergeAuthoritativeTaskList } from '../../lib/liveness';
import { ApiError } from '../../lib/types';
import { boardMetadataErrorAfterRefresh, boardMetadataErrorMessage, boardRefreshTargetsAreCurrent, mergeOwnedBoardMetadata, validBoardReleaseFilter } from '../appHelpers';
import { announce, friendlyError, observeWorkTransitions, toast } from '../feedback';
import { saveBoardSnapshot } from '../offline';
import { loadProjectReleases } from '../releases/load';
import { board } from '../state/board.svelte';
import { live } from '../state/live.svelte';
import { releasePlan } from '../state/releasePlan.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import { mutationsForRequest } from '../taskSync';
import type { BoardColumnLoadOptions, BoardColumnPage, BoardLoadOptions } from '../types';
import { refreshBoardInBackground } from './backgroundRefresh';
import { beginBoardColumnRefresh, boardTaskParams, claimBoardColumnRequestGeneration, emptyBoardColumnPage, endBoardColumnRefresh, flattenBoardPages, invalidateBoardColumnRequests, mergeBoardPageTasks, sortBoardTasks, syncBoardReleaseURL } from './pages';

export async function loadBoard(options: BoardLoadOptions = {}): Promise<boolean> {
  if (options.background) return refreshBoardInBackground();
  if (options.criteriaRevision === undefined && board.boardFilterTimer) {
    window.clearTimeout(board.boardFilterTimer);
    board.boardFilterTimer = undefined;
  }
  const requestId = ++board.boardRequest;
  live.boardLivenessRequest += 1;
  const requestedSession = session.sessionGeneration;
  const mutationSnapshot = board.taskMutationRevision;
  const requestedSlug = workspace.activeProjectSlug;
  const requestedCriteriaRevision = options.criteriaRevision ?? board.boardCriteriaRevision;
  invalidateBoardColumnRequests(Object.keys(board.boardPages));
  if (!requestedSlug) {
    board.boardLoading = false;
    if (requestedCriteriaRevision === board.boardCriteriaRevision) board.boardCriteriaTransition = false;
    return true;
  }
  const project = workspace.projects.find((item) => item.slug === requestedSlug);
  if (!project) {
    board.boardLoading = false;
    if (requestedCriteriaRevision === board.boardCriteriaRevision) board.boardCriteriaTransition = false;
    return true;
  }
  // Release metadata is intentionally independent from board columns so a
  // retained API can still render cached board work while release support
  // rolls out. The release-aware task request below remains authoritative.
  const requestedReleaseFilter = board.boardReleaseFilter;
  if (requestedReleaseFilter !== 'all' && requestedReleaseFilter !== 'unassigned') {
    const projectReleases = await loadProjectReleases(project.id);
    if (
      requestId !== board.boardRequest
      || workspace.activeProjectSlug !== requestedSlug
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return false;
    // A release deep link must be validated before the first task page is
    // requested. Otherwise an inaccessible release can leave every column
    // in a partial/error state. Preserve the filter when release metadata
    // itself is unavailable so a transient outage can still be retried.
    if (
      board.boardReleaseFilter === requestedReleaseFilter
      && releasePlan.releasesByProject[project.id] !== undefined
      && validBoardReleaseFilter(requestedReleaseFilter, projectReleases) === 'all'
    ) {
      board.boardReleaseFilter = 'all';
      syncBoardReleaseURL();
      announce('The focus filter was cleared because it is not part of this project.');
    }
  } else {
    void loadProjectReleases(project.id);
  }
  board.boardLoading = true;
  board.boardMetadataErrors = boardMetadataErrorAfterRefresh(board.boardMetadataErrors, 'full', [], '');
  board.boardError = '';
  board.boardPartial = false;
  board.boardReconciliationNotice = '';
  try {
    const [columnResult, labelResult] = await Promise.all([
      api.listAllColumns(project.id),
      api.listAllLabels(project.id)
    ]);
    if (
      requestId !== board.boardRequest
      || workspace.activeProjectSlug !== requestedSlug
      || session.sessionGeneration !== requestedSession
      || !session.user
    ) return false;
    board.boardMetadataErrors = boardMetadataErrorAfterRefresh(board.boardMetadataErrors, 'full', [], '');
    board.boardError = boardMetadataErrorMessage(board.boardMetadataErrors);
    board.columns = columnResult.data;
    board.labels = labelResult.data;
    invalidateBoardColumnRequests(Object.keys(board.boardPages));
    board.boardPages = Object.fromEntries(board.columns.map((column) => [column.id, emptyBoardColumnPage()]));
    board.boardCardOffsets = {};
    board.tasks = [];
    observeWorkTransitions(board.tasks);
    const pageResults = await Promise.all(
      [...board.columns].sort((a, b) => a.position - b.position).map((column) => loadBoardColumn(column.id, { reset: true, mutationSnapshot }))
    );
    return pageResults.every(Boolean);
  } catch (error) {
    if (
      requestId === board.boardRequest
      && workspace.activeProjectSlug === requestedSlug
      && session.sessionGeneration === requestedSession
      && session.user
    ) {
      board.boardMetadataErrors = boardMetadataErrorAfterRefresh(
        board.boardMetadataErrors,
        'full',
        [],
        friendlyError(error, 'This board could not be loaded.')
      );
      board.boardError = boardMetadataErrorMessage(board.boardMetadataErrors);
    }
    return false;
  } finally {
    if (requestId === board.boardRequest && session.sessionGeneration === requestedSession) {
      board.boardLoading = false;
      if (requestedCriteriaRevision === board.boardCriteriaRevision) board.boardCriteriaTransition = false;
      saveBoardSnapshot();
    }
  }
}

export async function loadBoardColumn(
  columnId: string,
  options: BoardColumnLoadOptions = {}
): Promise<boolean> {
  const {
    reset = false,
    mutationSnapshot = board.taskMutationRevision,
    recoveryNotice = false,
    announceChanges = true
  } = options;
  const page = board.boardPages[columnId] || emptyBoardColumnPage();
  if ((!reset && page.loading) || (!reset && !page.nextCursor)) return true;
  const requestedBoardRequest = board.boardRequest;
  const requestedSession = session.sessionGeneration;
  const requestedSlug = workspace.activeProjectSlug;
  const project = workspace.projects.find((item) => item.slug === requestedSlug);
  if (!project || !session.user) return false;
  const requestedProjectId = project.id;
  const columnGeneration = claimBoardColumnRequestGeneration(columnId);
  const requestIsCurrent = () => (
    requestedBoardRequest === board.boardRequest
    && requestedSession === session.sessionGeneration
    && requestedSlug === workspace.activeProjectSlug
    && workspace.activeProject?.id === requestedProjectId
    && Boolean(session.user)
    && boardColumnRequestIsCurrent(board.boardColumnGenerations, columnId, columnGeneration)
  );
  const cursor = reset ? '' : page.nextCursor;
  const existingColumnTasks = reset
    ? board.tasks.filter((task) => task.column_id === columnId)
    : page.tasks;
  board.boardPages = {
    ...board.boardPages,
    [columnId]: { ...page, loading: true, error: '' }
  };
  try {
    const result = await api.listTasks(project.id, boardTaskParams(columnId, cursor));
    if (!requestIsCurrent()) return false;
    const protectedMutations = mutationsForRequest('board', mutationSnapshot);
    const merged = reset
      ? mergeAuthoritativeTaskList(existingColumnTasks, result.data, protectedMutations)
      : mergeBoardPageTasks(existingColumnTasks, result.data, protectedMutations);
    const nextPage: BoardColumnPage = {
      tasks: sortBoardTasks(merged.filter((task) => task.column_id === columnId)),
      nextCursor: result.next_cursor || '',
      loading: false,
      loaded: true,
      error: ''
    };
    board.boardPages = { ...board.boardPages, [columnId]: nextPage };
    board.tasks = flattenBoardPages();
    board.boardPartial = Object.values(board.boardPages).some((item) => Boolean(item.error || item.nextCursor));
    board.boardOffline = false;
    saveBoardSnapshot();
    observeWorkTransitions(board.tasks, announceChanges);
    if (announceChanges && recoveryNotice && requestIsCurrent()) {
      const refreshedMessage = 'This column changed while loading more tasks; its first page was refreshed.';
      board.boardReconciliationNotice = refreshedMessage;
      announce(refreshedMessage);
    }
    return true;
  } catch (error) {
    if (!requestIsCurrent()) return false;
    if (error instanceof ApiError && isRecoverableBoardCursorConflict(error.status, error.code, reset, cursor)) {
      const currentPage = board.boardPages[columnId] || page;
      const recoveryMessage = 'This column changed while loading more tasks. Refreshing its first page.';
      board.boardReconciliationNotice = recoveryMessage;
      announce(recoveryMessage);
      toast('info', recoveryMessage);
      // Clear the in-flight marker before the reset so loadBoardColumn does
      // not short-circuit on the stale page request. The reset path is
      // deliberately one-shot; if it also conflicts, the normal page error
      // state gives the person an explicit retry action.
      board.boardPages = {
        ...board.boardPages,
        [columnId]: { ...currentPage, loading: false, error: '' }
      };
      board.boardPartial = true;
      return loadBoardColumn(columnId, { reset: true, mutationSnapshot, recoveryNotice: true });
    }
    const offlineNow = typeof navigator !== 'undefined' && !navigator.onLine;
    const message = board.boardOffline || offlineNow
      ? 'You are offline. Reconnect and retry this column.'
      : friendlyError(error, 'This column could not be loaded.');
    board.boardOffline = offlineNow;
    const currentPage = board.boardPages[columnId] || page;
    board.boardPages = {
      ...board.boardPages,
      [columnId]: { ...currentPage, loading: false, loaded: true, error: message }
    };
    board.boardPartial = true;
    return false;
  } finally {
    // A newer board or same-column request may have replaced this page while
    // this request was in flight. Its owner is responsible for clearing the
    // loading state and committing its response.
    const current = board.boardPages[columnId];
    if (requestIsCurrent() && current?.loading) {
      board.boardPages = { ...board.boardPages, [columnId]: { ...current, loading: false } };
    }
  }
}

/** Invalidate and reload only pages whose physical ordering changed. */
export async function reloadBoardColumns(columnIds: string[]): Promise<boolean> {
  const ids = [...new Set(columnIds)].filter((id) => Boolean(board.boardPages[id]));
  if (!ids.length || !workspace.activeProject) return true;
  const requestedProjectId = workspace.activeProject.id;
  const requestedSlug = workspace.activeProjectSlug;
  const requestedBoardRequest = board.boardRequest;
  const requestedSession = session.sessionGeneration;
  const refreshToken = beginBoardColumnRefresh(ids);
  // Invalidate pending page responses before waiting for refreshed column
  // metadata. The old response must not repopulate a terminal page while
  // this targeted refresh is in flight.
  invalidateBoardColumnRequests(ids);
  // A newer refresh may claim one or more of these columns. Only overlapping
  // targets make this refresh stale; disjoint column refreshes remain safe to
  // complete independently while the board/session checks below protect the
  // shared project context.
  const refreshIsCurrent = () => (
    boardRefreshTargetsAreCurrent(board.boardColumnRefreshes, ids, refreshToken)
    && requestedBoardRequest === board.boardRequest
    && requestedSession === session.sessionGeneration
    && workspace.activeProjectSlug === requestedSlug
    && workspace.activeProject?.id === requestedProjectId
    && Boolean(session.user)
  );
  try {
    let columnsRefreshed = true;
    try {
      // Task responses carry the moved row but not the affected column
      // revisions. Refresh those revisions before the next mutation so a
      // subsequent anchor request is not rejected with our own stale value.
      const result = await api.listAllColumns(requestedProjectId);
      if (!refreshIsCurrent()) return false;
      const previousMetadataError = boardMetadataErrorMessage(board.boardMetadataErrors);
      board.columns = mergeOwnedBoardMetadata(board.columns, result.data, ids);
      board.boardMetadataErrors = boardMetadataErrorAfterRefresh(board.boardMetadataErrors, 'targeted', ids, '');
      const nextMetadataError = boardMetadataErrorMessage(board.boardMetadataErrors);
      if (board.boardError === previousMetadataError) board.boardError = nextMetadataError;
    } catch (error) {
      if (!refreshIsCurrent()) return false;
      columnsRefreshed = false;
      const metadataError = friendlyError(error, 'Board metadata could not be refreshed. Retry before using global ordering.');
      const previousMetadataError = boardMetadataErrorMessage(board.boardMetadataErrors);
      board.boardMetadataErrors = boardMetadataErrorAfterRefresh(
        board.boardMetadataErrors,
        'targeted',
        ids,
        metadataError
      );
      const nextMetadataError = boardMetadataErrorMessage(board.boardMetadataErrors);
      if (!board.boardError || board.boardError === previousMetadataError) board.boardError = nextMetadataError;
    }
    if (!refreshIsCurrent()) return false;
    const mutationSnapshot = board.taskMutationRevision;
    const nextPages: Record<string, BoardColumnPage> = { ...board.boardPages };
    ids.forEach((columnId) => {
      nextPages[columnId] = emptyBoardColumnPage();
    });
    board.boardPages = nextPages;
    board.tasks = flattenBoardPages();
    board.boardPartial = Object.values(board.boardPages).some((item) => Boolean(item.error || item.nextCursor));
    observeWorkTransitions(board.tasks, false);
    if (!refreshIsCurrent()) return false;
    const results = await Promise.all(ids.map((columnId) => loadBoardColumn(columnId, {
      reset: true,
      mutationSnapshot,
      announceChanges: false
    })));
    return columnsRefreshed && results.every(Boolean);
  } finally {
    endBoardColumnRefresh(ids, refreshToken);
  }
}

export function loadMoreBoardColumn(columnId: string) {
  const page = board.boardPages[columnId];
  if (!page?.nextCursor || page.loading) return;
  void loadBoardColumn(columnId);
}
