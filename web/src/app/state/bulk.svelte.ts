// Reactive state: Board task selection and bulk change form.
import type { BulkTaskMutationOperation, BulkTaskMutationResponse, Priority, Task } from '../../lib/types';
import { board } from './board.svelte';

class BulkState {
  selectedTaskIds = $state.raw(new Set<string>());
  bulkReviewTasks = $state.raw<Task[]>([]);
  showBulkModal = $state.raw(false);
  bulkSubmitting = $state.raw(false);
  bulkError = $state.raw('');
  bulkResult = $state.raw<BulkTaskMutationResponse | null>(null);
  bulkMode = $state.raw<'partial' | 'atomic'>('partial');
  bulkOperation = $state.raw<BulkTaskMutationOperation>('priority');
  bulkPriority = $state.raw<Priority>('normal');
  bulkAssignee = $state.raw('');
  bulkLabels = $state.raw('');
  bulkDueDate = $state.raw('');
  bulkDestinationColumnId = $state.raw('');
  bulkReason = $state.raw('');
  bulkRequest = $state.raw(0);
  // Retaining snapshots mutates the selection cache, so this runs eagerly
  // (like the former reactive statement) rather than as a lazy $derived.
  selectedTasks = $state.raw<Task[]>([]);

  // Derived values
  allVisibleTasksSelected = $derived(board.visibleTasks.length > 0 && board.visibleTasks.every((task) => bulk.selectedTaskIds.has(task.id)));
}

export const bulk = new BulkState();
