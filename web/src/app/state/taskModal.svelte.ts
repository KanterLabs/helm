// Reactive state: New-task dialog and Luna assistance.
import type { Column, Priority, TaskDraftSuggestion } from '../../lib/types';
import { suggestionDescription } from '../taskModal';
import type { TaskModalAppliedFields } from '../types';
import { workspace } from './workspace.svelte';

class TaskModalState {
  taskModalColumnsRequest = $state.raw(0);
  showTaskModal = $state.raw(false);
  taskModalLoading = $state.raw(false);
  taskModalCreating = $state.raw(false);
  taskModalError = $state.raw('');
  taskModalProjectId = $state.raw('');
  taskModalColumnId = $state.raw('');
  taskModalColumns = $state.raw<Column[]>([]);
  taskModalTitle = $state.raw('');
  taskModalDescription = $state.raw('');
  taskModalPriority = $state.raw<Priority>('normal');
  taskModalDueDate = $state.raw('');
  taskModalAssignee = $state.raw('');
  taskModalReleaseId = $state.raw('');
  taskModalParentId = $state.raw<string | null>(null);
  taskModalIdea = $state.raw('');
  taskModalSuggestion = $state.raw<TaskDraftSuggestion | null>(null);
  taskModalAssisting = $state.raw(false);
  taskModalAssistStage = $state.raw('');
  taskModalNeedsCodex = $state.raw(false);
  taskModalAssistController = $state.raw<AbortController | null>(null);
  taskModalAppliedFields = $state.raw<TaskModalAppliedFields>({ title: false, description: false, priority: false });
  taskModalApplyNotice = $state.raw('');
  taskModalHasAppliedAll = $state.raw(false);
  taskModalSuggestionCollapsed = $state.raw(false);

  // Derived values
  taskModalProject = $derived(workspace.projects.find((project) => project.id === taskModal.taskModalProjectId));
  taskModalTitleApplied = $derived(Boolean(taskModal.taskModalSuggestion && taskModal.taskModalAppliedFields.title && taskModal.taskModalTitle === taskModal.taskModalSuggestion.title));
  taskModalDescriptionApplied = $derived(Boolean(taskModal.taskModalSuggestion && taskModal.taskModalAppliedFields.description && taskModal.taskModalDescription === suggestionDescription(taskModal.taskModalSuggestion)));
  taskModalPriorityApplied = $derived(Boolean(taskModal.taskModalSuggestion && taskModal.taskModalAppliedFields.priority && taskModal.taskModalPriority === taskModal.taskModalSuggestion.priority));
  taskModalAllFieldsApplied = $derived(taskModal.taskModalTitleApplied && taskModal.taskModalDescriptionApplied && taskModal.taskModalPriorityApplied);
}

export const taskModal = new TaskModalState();
