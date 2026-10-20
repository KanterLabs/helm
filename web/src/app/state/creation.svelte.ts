// Reactive state: Replay-safe creation records and the new-project form.
import { api } from '../../lib/api';
import type { TaskCreation } from '../types';

class CreationState {
  showProjectModal = $state.raw(false);
  projectCreating = $state.raw(false);
  projectFormError = $state.raw('');
  projectKeyDraft = $state.raw('');
  projectNameDraft = $state.raw('');
  projectDescriptionDraft = $state.raw('');
  projectColorDraft = $state.raw('#6d5efc');
  projectCreation = $state.raw<{ input: Parameters<typeof api.createProject>[0]; key: string } | null>(null);
  taskCreation = $state.raw<TaskCreation | null>(null);
  bugCreation = $state.raw<TaskCreation | null>(null);
  quickCreation = $state.raw<TaskCreation | null>(null);
  pendingCreationRoute = $state.raw<{ url: string; state: unknown } | null>(null);
  quickAddCreating = $state.raw(false);
  quickAddError = $state.raw('');
  projectModalInstance = $state.raw(0);
  taskModalInstance = $state.raw(0);
  bugModalInstance = $state.raw(0);
}

export const creation = new CreationState();
