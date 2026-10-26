// Replay-safe creation sessions and the new-project modal.
import { api } from '../lib/api';
import { ApiError } from '../lib/types';
import { rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { selectProject } from './routing';
import { board } from './state/board.svelte';
import { bugModal } from './state/bugModal.svelte';
import { creation } from './state/creation.svelte';
import { nav } from './state/nav.svelte';
import { session } from './state/session.svelte';
import { taskModal } from './state/taskModal.svelte';
import { workspace } from './state/workspace.svelte';
import { cancelTaskAssist } from './taskModal';

export function closeProjectModal() {
  if (creation.projectCreating || creation.projectCreation) return;
  creation.projectModalInstance += 1;
  creation.showProjectModal = false;
  restoreDialogFocus();
}

export function hasPendingCreation(): boolean {
  return creation.projectCreating || taskModal.taskModalCreating || bugModal.bugModalCreating || creation.quickAddCreating
    || Boolean(creation.projectCreation || creation.taskCreation || creation.bugCreation || creation.quickCreation);
}

export function rememberCreationRoute() {
  creation.pendingCreationRoute = {
    url: `${window.location.pathname}${window.location.search}${window.location.hash}`,
    state: window.history.state
  };
}

export function resetCreationSession() {
  // A real authentication change invalidates form instances. A temporary
  // offline transition keeps the same actor and must retain unresolved keys.
  creation.projectModalInstance += 1;
  creation.taskModalInstance += 1;
  creation.bugModalInstance += 1;
  taskModal.taskModalColumnsRequest += 1;
  bugModal.bugModalColumnsRequest += 1;
  cancelTaskAssist();
  creation.showProjectModal = taskModal.showTaskModal = bugModal.showBugModal = false;
  creation.projectCreating = taskModal.taskModalCreating = bugModal.bugModalCreating = creation.quickAddCreating = false;
  creation.projectCreation = null;
  creation.taskCreation = creation.bugCreation = creation.quickCreation = null;
  creation.pendingCreationRoute = null;
  board.quickAddColumn = '';
}

export function hasCreationDrafts(): boolean {
  return Boolean(
    (creation.showProjectModal && (creation.projectNameDraft.trim() || creation.projectKeyDraft.trim() || creation.projectDescriptionDraft.trim() || creation.projectColorDraft !== '#6d5efc'))
    || (taskModal.showTaskModal && (taskModal.taskModalTitle.trim() || taskModal.taskModalDescription.trim() || taskModal.taskModalIdea.trim() || taskModal.taskModalDueDate || taskModal.taskModalAssignee.trim() || taskModal.taskModalPriority !== 'normal' || taskModal.taskModalReleaseId))
    || (bugModal.showBugModal && (bugModal.bugModalTitle.trim() || bugModal.bugModalActual.trim() || bugModal.bugModalExpected.trim() || bugModal.bugModalReproduction.trim() || bugModal.bugModalEnvironment.trim() || bugModal.bugModalVersion.trim() || bugModal.bugModalDescription.trim() || bugModal.bugModalLabels.trim() || bugModal.bugModalSeverity || bugModal.bugModalPriority !== 'normal' || bugModal.bugModalReleaseId))
    || (board.quickAddColumn && board.quickAddTitle[board.quickAddColumn]?.trim())
  );
}

export function ambiguousCreation(error: unknown): boolean {
  return !(error instanceof ApiError) || error.status === 0 || error.status >= 500 || error.status === 408;
}

export function creationOperationKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `create-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

// Replay only the original operation. Browser cancellation cannot undo a
// committed create, so a lost response must never allocate a new key.
export async function reconcileCreation<T>(commit: () => Promise<T>): Promise<T> {
  try {
    return await commit();
  } catch (error) {
    if (!ambiguousCreation(error)) throw error;
    return await commit();
  }
}

export function creationError(error: unknown, fallback: string): string {
  return ambiguousCreation(error)
    ? 'The result is not confirmed. Keep this form open and retry to check the original submission safely.'
    : friendlyError(error, fallback);
}

export function openProjectModal() {
  if (hasPendingCreation()) return;
  creation.projectModalInstance += 1;
  rememberDialogFocus('[data-project-modal-trigger], [data-project-picker-trigger]');
  creation.projectKeyDraft = '';
  creation.projectNameDraft = '';
  creation.projectDescriptionDraft = '';
  creation.projectColorDraft = '#6d5efc';
  creation.projectFormError = '';
  creation.showProjectModal = true;
  nav.projectSwitcherOpen = false;
}

export async function createProject() {
  if (creation.projectCreating || !creation.showProjectModal) return;
  creation.projectFormError = '';
  if (!creation.projectCreation) {
    const key = creation.projectKeyDraft.trim().toUpperCase();
    if (!/^[A-Z][A-Z0-9_-]{0,15}$/.test(key)) {
      creation.projectFormError = 'Use 1–16 uppercase letters or numbers for the project key.';
      return;
    }
    if (!creation.projectNameDraft.trim()) {
      creation.projectFormError = 'Give the project a name.';
      return;
    }
    rememberCreationRoute();
    creation.projectCreation = {
      key: creationOperationKey(),
      input: { key, name: creation.projectNameDraft.trim(), description: creation.projectDescriptionDraft.trim(), color: creation.projectColorDraft, favorite: true }
    };
  }
  const operation = creation.projectCreation;
  const instance = creation.projectModalInstance;
  const actor = session.user?.id;
  creation.projectCreating = true;
  try {
    const project = await reconcileCreation(() => api.createProject(operation.input, operation.key));
    if (actor !== session.user?.id || instance !== creation.projectModalInstance) return;
    workspace.projects = [...workspace.projects.filter((item) => item.id !== project.id), project];
    creation.projectCreation = null;
    creation.projectCreating = false;
    closeProjectModal();
    toast('success', `${project.name} is ready to plan.`);
    await selectProject(project);
  } catch (error) {
    if (actor !== session.user?.id || instance !== creation.projectModalInstance) return;
    if (!ambiguousCreation(error)) creation.projectCreation = null;
    creation.projectFormError = creationError(error, 'The project could not be created.');
  } finally {
    if (actor === session.user?.id && instance === creation.projectModalInstance) creation.projectCreating = false;
  }
}
