// Reactive state: Board columns, task pages, filters, drag state and quick add.
import type { OfflineBoard as OfflineBoardSnapshot } from '../../lib/offlineBoards';
import { agentWorkStatusCounts, filterTasks } from '../../lib/state';
import type { BoardFilters } from '../../lib/state';
import type { Column, Label, Task } from '../../lib/types';
import type { BoardMetadataErrorState } from '../appHelpers';
import { makeBoardFilterChips, sortBoardTasks } from '../board/pages';
import { matchesWorkFilter, semanticStateForTask, taskMatchesReleaseFilter } from '../taskQueries';
import type { BoardColumnPage, BoardSort, WorkFilter } from '../types';
import { live } from './live.svelte';

class BoardState {
  columns = $state.raw<Column[]>([]);
  tasks = $state.raw<Task[]>([]);
  labels = $state.raw<Label[]>([]);
  boardLoading = $state.raw(false);
  boardError = $state.raw('');
  boardMetadataErrors = $state.raw<BoardMetadataErrorState>({ full: '', targeted: {} });
  boardPartial = $state.raw(false);
  boardOffline = $state.raw(false);
  offlineBoards = $state.raw<OfflineBoardSnapshot[]>([]);
  offlineReadRevision = $state.raw(0);
  reconnecting = $state.raw(false);
  reconnectError = $state.raw('');
  boardReconciliationNotice = $state.raw('');
  boardPages = $state.raw<Record<string, BoardColumnPage>>({});
  boardCardOffsets = $state.raw<Record<string, number>>({});
  boardColumnGenerations = $state.raw<Record<string, number>>({});
  boardColumnRefreshSequence = $state.raw(0);
  boardColumnRefreshes = $state.raw<Record<string, number>>({});
  boardCriteriaRevision = $state.raw(0);
  boardCriteriaTransition = $state.raw(false);
  boardMutationRequest = $state.raw(0);
  boardFilterTimer = $state.raw<number | undefined>();
  boardSort = $state.raw<BoardSort>('position');
  boardOrder = $state.raw<'asc' | 'desc'>('asc');
  // Release filters are kept outside BoardFilters so older cached board
  // snapshots remain readable while the release-aware API rolls out.
  boardReleaseFilter = $state.raw('all');
  filters = $state.raw<BoardFilters>({ query: '', priority: 'all', label: 'all', assignee: 'all', state: 'all', dependency: 'all' });
  boardWorkFilter = $state.raw<WorkFilter>('all');
  expandedBoardTaskIds = $state.raw(new Set<string>());
  boardSearchInput = $state.raw<HTMLInputElement | null>(null);
  boardRequest = $state.raw(0);
  taskMutationRevision = $state.raw(0);
  draggingTaskId = $state.raw('');
  dragOverColumnId = $state.raw('');
  quickAddColumn = $state.raw('');
  quickAddTitle = $state.raw<Record<string, string>>({});
  quickAddInput = $state.raw<HTMLInputElement | null>(null);
  quickAddReturnFocus = $state.raw<HTMLButtonElement | null>(null);

  // Derived values
  visibleTasks = $derived(filterTasks(board.tasks, board.columns, board.filters)
    .filter((task) => taskMatchesReleaseFilter(task, board.boardReleaseFilter))
    .filter((task) => matchesWorkFilter(task, board.boardWorkFilter, live.pulseClock)));
  boardWorkCounts = $derived(agentWorkStatusCounts(board.tasks, live.pulseClock, (task) => semanticStateForTask(task)));
  sortedColumns = $derived([...board.columns].sort((a, b) => a.position - b.position));
  // Keep the board's column buckets as a reactive value. Calling a helper
  // from the template does not give Svelte a dependency on visibleTasks, so
  // mutations could otherwise leave cards/counts rendered in their old
  // column until an unrelated update occurred.
  tasksByColumn = $derived(board.sortedColumns.reduce<Record<string, Task[]>>((groups, column) => {
    groups[column.id] = sortBoardTasks(board.visibleTasks.filter((task) => task.column_id === column.id));
    return groups;
  }, {}));
  boardFilterChips = $derived(makeBoardFilterChips(board.filters, board.boardWorkFilter, board.boardReleaseFilter, board.boardSort, board.boardOrder));
}

export const board = new BoardState();
