// Refreshes loaded board pages in the background without losing local edits.
import { api } from '../../lib/api';
import { mergeAuthoritativeTaskList } from '../../lib/liveness';
import type { Task } from '../../lib/types';
import { boardMetadataErrorAfterRefresh, boardMetadataErrorMessage } from '../appHelpers';
import { friendlyError, observeWorkTransitions } from '../feedback';
import { saveBoardSnapshot } from '../offline';
import { board } from '../state/board.svelte';
import { live } from '../state/live.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import { mutationsForRequest } from '../taskSync';
import type { BoardColumnPage } from '../types';
import { boardTaskParams, emptyBoardColumnPage, flattenBoardPagesForColumns, sortBoardTasks } from './pages';

/**
 * Refresh the board without taking the currently rendered board offline.
 *
 * Liveness and event polling are deliberately staged separately from the
 * normal load path. A full reload is still allowed to show its skeleton;
 * this path keeps the current pages in place until metadata and each
 * currently loaded page depth have been read, then commits the reconciled
 * snapshot atomically.
 */
export async function refreshBoardInBackground(): Promise<boolean> {
  if (
    !session.user
    || (workspace.view !== 'board' && workspace.view !== 'timeline')
    || !workspace.activeProject
    || board.boardLoading
    || Object.values(board.boardPages).some((page) => page.loading)
  ) return false;
  if (live.boardBackgroundInFlight) return live.boardBackgroundInFlight;

  const requestedSession = session.sessionGeneration;
  const requestedSlug = workspace.activeProjectSlug;
  const requestedProjectId = workspace.activeProject.id;
  const requestedBoardRequest = board.boardRequest;
  const requestedCriteriaRevision = board.boardCriteriaRevision;
  const requestedLivenessRequest = ++live.boardLivenessRequest;
  const mutationSnapshot = board.taskMutationRevision;
  const requestedColumnGenerations = { ...board.boardColumnGenerations };
  // A board page stores all cards fetched so far, not a cursor history. Keep
  // that pagination state authoritative by refetching the same loaded depth
  // from the first page before committing the background snapshot.
  const requestedLoadedTaskCounts = Object.fromEntries(
    Object.entries(board.boardPages).map(([columnId, page]) => [columnId, page.tasks.length])
  );

  const request = (async () => {
    const isCurrent = () => Boolean(
      session.user
      && session.sessionGeneration === requestedSession
      && workspace.activeProjectSlug === requestedSlug
      && workspace.activeProject?.id === requestedProjectId
      && board.boardRequest === requestedBoardRequest
      && board.boardCriteriaRevision === requestedCriteriaRevision
      && live.boardLivenessRequest === requestedLivenessRequest
    );
    const columnGenerationsAreCurrent = () => {
      const columnIds = new Set([
        ...Object.keys(requestedColumnGenerations),
        ...Object.keys(board.boardColumnGenerations)
      ]);
      return [...columnIds].every((columnId) => (
        (requestedColumnGenerations[columnId] || 0) === (board.boardColumnGenerations[columnId] || 0)
      ));
    };

    let columnResult: Awaited<ReturnType<typeof api.listAllColumns>>;
    let labelResult: Awaited<ReturnType<typeof api.listAllLabels>>;
    try {
      [columnResult, labelResult] = await Promise.all([
        api.listAllColumns(requestedProjectId),
        api.listAllLabels(requestedProjectId)
      ]);
    } catch (error) {
      if (isCurrent()) {
        const message = friendlyError(error, 'This board could not be loaded.');
        board.boardMetadataErrors = boardMetadataErrorAfterRefresh(board.boardMetadataErrors, 'full', [], message);
        board.boardError = boardMetadataErrorMessage(board.boardMetadataErrors);
      }
      return false;
    }

    if (!isCurrent() || !columnGenerationsAreCurrent()) return false;

    const pageResults = await Promise.all(
      [...columnResult.data].sort((a, b) => a.position - b.position).map(async (column) => {
        try {
          const loadedTaskCount = requestedLoadedTaskCounts[column.id] || 0;
          const fetched: Task[] = [];
          const seenCursors = new Set<string>();
          let cursor = '';
          let nextCursor = '';
          do {
            if (!isCurrent()) {
              return {
                columnId: column.id,
                data: [] as Task[],
                nextCursor: '',
                error: '',
                offline: false
              };
            }
            const result = await api.listTasks(requestedProjectId, boardTaskParams(column.id, cursor));
            fetched.push(...result.data);
            nextCursor = result.next_cursor || '';
            if (!nextCursor || fetched.length >= loadedTaskCount || seenCursors.has(nextCursor)) break;
            seenCursors.add(nextCursor);
            cursor = nextCursor;
          } while (true);
          return {
            columnId: column.id,
            data: fetched,
            nextCursor,
            error: '',
            offline: false
          };
        } catch (error) {
          const offlineNow = typeof navigator !== 'undefined' && !navigator.onLine;
          return {
            columnId: column.id,
            data: [] as Task[],
            nextCursor: '',
            error: offlineNow
              ? 'You are offline. Reconnect and retry this column.'
              : friendlyError(error, 'This column could not be loaded.'),
            offline: offlineNow
          };
        }
      })
    );

    if (!isCurrent() || !columnGenerationsAreCurrent()) return false;

    const protectedMutations = mutationsForRequest('board', mutationSnapshot);
    const currentPages = board.boardPages;
    const nextPages: Record<string, BoardColumnPage> = {};
    board.columns = columnResult.data;
    board.labels = labelResult.data;

    pageResults.forEach((result) => {
      const currentPage = currentPages[result.columnId] || emptyBoardColumnPage();
      if (result.error) {
        nextPages[result.columnId] = {
          ...currentPage,
          loading: false,
          loaded: true,
          error: result.error
        };
        return;
      }
      const merged = mergeAuthoritativeTaskList(currentPage.tasks, result.data, protectedMutations);
      nextPages[result.columnId] = {
        ...currentPage,
        tasks: sortBoardTasks(merged.filter((task) => task.column_id === result.columnId)),
        nextCursor: result.nextCursor,
        loading: false,
        loaded: true,
        error: ''
      };
    });

    board.boardPages = nextPages;
    board.boardCardOffsets = Object.fromEntries(
      Object.entries(board.boardCardOffsets).filter(([columnId]) => Boolean(nextPages[columnId]))
    );
    board.tasks = flattenBoardPagesForColumns(nextPages, columnResult.data);
    board.boardPartial = Object.values(nextPages).some((page) => Boolean(page.error || page.nextCursor));
    board.boardOffline = pageResults.some((result) => result.offline);
    board.boardMetadataErrors = boardMetadataErrorAfterRefresh(board.boardMetadataErrors, 'full', [], '');
    board.boardError = boardMetadataErrorMessage(board.boardMetadataErrors);
    observeWorkTransitions(board.tasks, false);
    saveBoardSnapshot();
    return pageResults.every((result) => !result.error);
  })();

  live.boardBackgroundInFlight = request;
  try {
    return await request;
  } finally {
    if (live.boardBackgroundInFlight === request) live.boardBackgroundInFlight = null;
  }
}
