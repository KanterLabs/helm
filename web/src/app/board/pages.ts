// Board column page bookkeeping, filters and board URL state.
import type { TaskListParams } from '../../lib/api';
import { createColumnScroll } from '../../lib/boardLayout';
import { claimBoardColumnRequest, sortBoardTasks as sortBoardTaskList } from '../../lib/boardOrdering';
import { boardViewParams, readBoardView } from '../../lib/boardViewState';
import { mergeAuthoritativeTask } from '../../lib/liveness';
import type { TaskMutationKind } from '../../lib/liveness';
import { filterTasks } from '../../lib/state';
import type { BoardFilters } from '../../lib/state';
import type { Column, Task } from '../../lib/types';
import { boardPageSize } from '../constants';
import { announce, clearAnnouncement, observeWorkTransitions } from '../feedback';
import { isTaskLocation, navigate } from '../routing';
import { board } from '../state/board.svelte';
import { releasePlan } from '../state/releasePlan.svelte';
import { session } from '../state/session.svelte';
import { workspace } from '../state/workspace.svelte';
import { matchesWorkFilter, releaseQueryValue, taskMatchesReleaseFilter } from '../taskQueries';
import type { BoardColumnPage, BoardMutationContext, BoardSort, WorkFilter } from '../types';
import { loadBoard } from './load';

export const columnScroll = createColumnScroll();

export function toggleBoardTaskExpanded(taskId: string): void {
  const next = new Set(board.expandedBoardTaskIds);
  if (next.has(taskId)) next.delete(taskId);
  else next.add(taskId);
  board.expandedBoardTaskIds = next;
}

export function emptyBoardColumnPage(): BoardColumnPage {
  return { tasks: [], nextCursor: '', loading: false, loaded: false, error: '' };
}

export function claimBoardColumnRequestGeneration(columnId: string): number {
  const claim = claimBoardColumnRequest(board.boardColumnGenerations, columnId);
  board.boardColumnGenerations = claim.generations;
  return claim.generation;
}

export function invalidateBoardColumnRequests(columnIds: string[]) {
  let next = board.boardColumnGenerations;
  [...new Set(columnIds)].forEach((columnId) => {
    next = claimBoardColumnRequest(next, columnId).generations;
  });
  board.boardColumnGenerations = next;
}

export function beginBoardColumnRefresh(columnIds: string[]): number {
  const refreshToken = ++board.boardColumnRefreshSequence;
  const next = { ...board.boardColumnRefreshes };
  [...new Set(columnIds)].forEach((columnId) => {
    next[columnId] = refreshToken;
  });
  board.boardColumnRefreshes = next;
  return refreshToken;
}

export function endBoardColumnRefresh(columnIds: string[], refreshToken: number) {
  const next = { ...board.boardColumnRefreshes };
  [...new Set(columnIds)].forEach((columnId) => {
    if (next[columnId] === refreshToken) delete next[columnId];
  });
  board.boardColumnRefreshes = next;
}

export function beginBoardMutation(): BoardMutationContext | null {
  if (!workspace.activeProject || !session.user) return null;
  clearAnnouncement();
  return {
    requestId: ++board.boardMutationRequest,
    boardRequest: board.boardRequest,
    session: session.sessionGeneration,
    mutationRevision: board.taskMutationRevision,
    projectId: workspace.activeProject.id,
    projectSlug: workspace.activeProjectSlug
  };
}

export function boardMutationIsCurrent(context: BoardMutationContext): boolean {
  return context.requestId === board.boardMutationRequest
    && context.boardRequest === board.boardRequest
    && context.session === session.sessionGeneration
    && context.mutationRevision === board.taskMutationRevision
    && context.projectId === workspace.activeProject?.id
    && context.projectSlug === workspace.activeProjectSlug
    && Boolean(session.user);
}

export function boardFiltersActive(): boolean {
  return Boolean(
    board.filters.query.trim()
    || board.filters.priority !== 'all'
    || board.filters.label !== 'all'
    || board.filters.assignee !== 'all'
    || board.filters.state !== 'all'
    || board.filters.dependency !== 'all'
    || board.boardReleaseFilter !== 'all'
    || board.boardWorkFilter !== 'all'
  );
}

/** Build one bounded, server-filtered request for one board column. */
export function boardTaskParams(columnId: string, cursor = ''): TaskListParams {
  const params: TaskListParams & { release?: string } = {
    column: columnId,
    sort: board.boardSort,
    order: board.boardOrder,
    limit: boardPageSize
  };
  if (cursor) params.cursor = cursor;
  if (board.filters.state !== 'all') params.state = board.filters.state;
  if (board.filters.priority !== 'all') params.priority = board.filters.priority;
  if (board.filters.label !== 'all') params.label = board.filters.label;
  if (board.filters.assignee !== 'all') params.assignee = board.filters.assignee;
  if (board.filters.dependency !== 'all') params.dependency = board.filters.dependency;
  if (board.filters.query.trim()) params.q = board.filters.query.trim();
  if (board.boardWorkFilter === 'action-needed') {
    params.action_needed = true;
  } else if (board.boardWorkFilter !== 'all') {
    params.agent_state = board.boardWorkFilter;
  }
  const releaseQuery = releaseQueryValue(board.boardReleaseFilter);
  if (releaseQuery) params.release = releaseQuery;
  return params;
}

export function sortBoardTasks(items: Task[]): Task[] {
  return sortBoardTaskList(items, board.boardSort, board.boardOrder);
}

export function flattenBoardPagesForColumns(
  pages: Record<string, BoardColumnPage>,
  columnList: readonly Column[]
): Task[] {
  const seen = new Set<string>();
  const flattened: Task[] = [];
  [...columnList].sort((a, b) => a.position - b.position).forEach((column) => {
    for (const task of pages[column.id]?.tasks || []) {
      if (seen.has(task.id)) continue;
      seen.add(task.id);
      flattened.push(task);
    }
  });
  return flattened;
}

export function flattenBoardPages(): Task[] {
  return flattenBoardPagesForColumns(board.boardPages, board.sortedColumns);
}

export function boardTaskMatches(task: Task, columnId = task.column_id): boolean {
  return Boolean(
    workspace.activeProject
    && task.project_id === workspace.activeProject.id
    && task.column_id === columnId
    && filterTasks([task], board.columns, board.filters).length
    && taskMatchesReleaseFilter(task, board.boardReleaseFilter)
    && matchesWorkFilter(task, board.boardWorkFilter)
  );
}

/** Keep a just-mutated task visible only when its loaded page can represent it. */
export function syncBoardTaskPages(updated: Task, announceChanges = true) {
  if (!Object.keys(board.boardPages).length) return;
  let changed = false;
  const nextPages: Record<string, BoardColumnPage> = { ...board.boardPages };
  Object.entries(board.boardPages).forEach(([columnId, page]) => {
    const hadTask = page.tasks.some((task) => task.id === updated.id);
    const shouldInclude = boardTaskMatches(updated, columnId) && (hadTask || page.loaded);
    const retained = page.tasks.filter((task) => task.id !== updated.id);
    if (shouldInclude) retained.push(updated);
    const nextTasks = sortBoardTasks(retained);
    if (
      nextTasks.length !== page.tasks.length
      || nextTasks.some((task, index) => task.id !== page.tasks[index]?.id || task.version !== page.tasks[index]?.version)
    ) {
      changed = true;
      nextPages[columnId] = { ...page, tasks: nextTasks };
    }
    if (announceChanges && hadTask && !shouldInclude) {
      board.boardReconciliationNotice = `${updated.key} moved outside the loaded board page or current filters.`;
      announce(board.boardReconciliationNotice);
    }
  });
  if (changed) {
    board.boardPages = nextPages;
    board.tasks = flattenBoardPages();
    observeWorkTransitions(board.tasks, announceChanges);
  }
}

export function removeTaskFromBoardPages(taskId: string) {
  let changed = false;
  const nextPages: Record<string, BoardColumnPage> = { ...board.boardPages };
  Object.entries(board.boardPages).forEach(([columnId, page]) => {
    if (!page.tasks.some((task) => task.id === taskId)) return;
    changed = true;
    nextPages[columnId] = { ...page, tasks: page.tasks.filter((task) => task.id !== taskId) };
  });
  if (changed) {
    board.boardPages = nextPages;
    board.tasks = flattenBoardPages();
  }
}

export function scheduleBoardReload() {
  board.boardCriteriaRevision += 1;
  board.boardCriteriaTransition = true;
  const criteriaRevision = board.boardCriteriaRevision;
  if (board.boardFilterTimer) window.clearTimeout(board.boardFilterTimer);
  board.boardFilterTimer = window.setTimeout(() => {
    board.boardFilterTimer = undefined;
    if (criteriaRevision === board.boardCriteriaRevision && workspace.view === 'board' && workspace.activeProject) {
      syncBoardViewURL();
      void loadBoard({ criteriaRevision });
    } else if (criteriaRevision === board.boardCriteriaRevision) {
      board.boardCriteriaTransition = false;
    }
  }, 160);
}

export function syncBoardReleaseURL() {
  syncBoardViewURL();
}

export function applyBoardRoute(params: URLSearchParams) {
  const state = readBoardView(params);
  board.filters = state.filters;
  board.boardWorkFilter = state.work as WorkFilter;
  board.boardReleaseFilter = state.release;
  board.boardSort = state.sort as BoardSort;
  board.boardOrder = state.order;
  board.boardCriteriaRevision += 1;
}

export function syncBoardViewURL() {
  if (typeof window === 'undefined' || !workspace.activeProject || workspace.view !== 'board' || isTaskLocation()) return;
  const params = boardViewParams(board.filters, board.boardWorkFilter, board.boardReleaseFilter, board.boardSort, board.boardOrder);
  navigate(`/p/${encodeURIComponent(workspace.activeProject.slug)}${params.size ? `?${params}` : ''}`);
}

export function mergeBoardPageTasks(
  existing: Task[],
  fetched: Task[],
  protectedMutations: ReadonlyMap<string, TaskMutationKind>
): Task[] {
  const fetchedById = new Map(fetched.map((task) => [task.id, task]));
  const localById = new Map(existing.map((task) => [task.id, task]));
  const merged = new Map<string, Task>();
  existing.forEach((task) => merged.set(task.id, task));
  fetched.forEach((task) => {
    if (protectedMutations.get(task.id) === 'remove') return;
    const local = localById.get(task.id);
    merged.set(task.id, mergeAuthoritativeTask(local, task));
  });
  protectedMutations.forEach((kind, taskId) => {
    if (kind !== 'upsert' || fetchedById.has(taskId) || merged.has(taskId)) return;
    const local = board.tasks.find((task) => task.id === taskId);
    if (local) merged.set(taskId, local);
  });
  return [...merged.values()].filter((task) => boardTaskMatches(task));
}

export function updateFilter(name: keyof BoardFilters, value: string) {
  board.filters = { ...board.filters, [name]: value };
}

export function clearFilters() {
  board.filters = { query: '', priority: 'all', label: 'all', assignee: 'all', state: 'all', dependency: 'all' };
  board.boardSort = 'position';
  board.boardOrder = 'asc';
  board.boardWorkFilter = 'all';
  board.boardReleaseFilter = 'all';
  syncBoardReleaseURL();
  scheduleBoardReload();
}

export function makeBoardFilterChips(current: BoardFilters, work: string, release: string, sort: string, order: string) {
  const chips: Array<{ id: string; label: string; clear: () => void }> = [];
  for (const key of ['query', 'priority', 'state', 'label', 'assignee', 'dependency'] as const) {
    const value = current[key];
    if (value && value !== 'all') chips.push({ id: key, label: `${key === 'query' ? 'Search' : key}: ${key === 'label' ? board.labels.find((l) => l.id === value)?.name || value : value}`, clear: () => { board.filters = { ...board.filters, [key]: key === 'query' ? '' : 'all' }; scheduleBoardReload(); } });
  }
  if (work !== 'all') chips.push({ id: 'work', label: `Agent: ${work}`, clear: () => { board.boardWorkFilter = 'all'; scheduleBoardReload(); } });
  if (release !== 'all') chips.push({ id: 'release', label: `Focus: ${releasePlan.activeProjectReleases.find((r) => r.id === release)?.name || release}`, clear: () => { board.boardReleaseFilter = 'all'; scheduleBoardReload(); } });
  if (sort !== 'position' || order !== 'asc') chips.push({ id: 'sort', label: `Sort: ${sort} ${order}`, clear: () => { board.boardSort = 'position'; board.boardOrder = 'asc'; scheduleBoardReload(); } });
  return chips;
}
