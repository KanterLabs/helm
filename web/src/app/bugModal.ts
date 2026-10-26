// The bug report modal.
import { api } from '../lib/api';
import type { TaskMutationScope } from '../lib/liveness';
import { plannedReleaseIdForAssignment } from './appHelpers';
import { syncBoardTaskPages } from './board/pages';
import { ambiguousCreation, creationError, creationOperationKey, hasPendingCreation, openProjectModal, reconcileCreation, rememberCreationRoute } from './creation';
import { rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { resolveTaskLabels } from './drawer/save';
import { friendlyError, toast } from './feedback';
import { loadProjectReleases } from './releases/load';
import { board } from './state/board.svelte';
import { bugModal } from './state/bugModal.svelte';
import { creation } from './state/creation.svelte';
import { dialogs } from './state/dialogs.svelte';
import { issues } from './state/issues.svelte';
import { nav } from './state/nav.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';
import { recordTaskMutation } from './taskSync';
import type { DialogReturnFocus, TaskCreation } from './types';

export function openBugModalFromCommand(returnFocus: DialogReturnFocus | null) {
  const opening = openBugModal();
  if (returnFocus) dialogs.dialogReturnFocus = returnFocus;
  return opening;
}

export function closeBugModal() {
  if (bugModal.bugModalCreating || creation.bugCreation) return;
  creation.bugModalInstance += 1;
  bugModal.bugModalColumnsRequest += 1;
  bugModal.showBugModal = false;
  bugModal.bugModalLoading = false;
  restoreDialogFocus();
}

export async function openBugModal() {
  if (hasPendingCreation()) return;
  const instance = ++creation.bugModalInstance;
  if (!workspace.navProjects.length) {
    openProjectModal();
    return;
  }
  bugModal.bugModalProjectId = (workspace.activeProject && !workspace.activeProject.system_kind ? workspace.activeProject.id : '') || workspace.navProjects[0].id;
  bugModal.bugModalColumnId = '';
  bugModal.bugModalColumns = [];
  bugModal.bugModalTitle = '';
  bugModal.bugModalDescription = '';
  bugModal.bugModalActual = '';
  bugModal.bugModalExpected = '';
  bugModal.bugModalReproduction = '';
  bugModal.bugModalEnvironment = '';
  bugModal.bugModalVersion = '';
  bugModal.bugModalLabels = '';
  bugModal.bugModalSeverity = '';
  bugModal.bugModalPriority = 'normal';
  bugModal.bugModalReleaseId = '';
  bugModal.bugModalError = '';
  const projectId = bugModal.bugModalProjectId;
  rememberDialogFocus('[data-report-bug-trigger]');
  bugModal.showBugModal = true;
  nav.projectSwitcherOpen = false;
  const columnLoad = loadBugModalColumns(projectId);
  const releaseRequest = bugModal.bugModalColumnsRequest;
  const projectReleases = await loadProjectReleases(projectId).catch(() => []);
  if (bugModal.showBugModal && creation.bugModalInstance === instance && releaseRequest === bugModal.bugModalColumnsRequest && bugModal.bugModalProjectId === projectId && !creation.bugCreation) {
    bugModal.bugModalReleaseId = plannedReleaseIdForAssignment(board.boardReleaseFilter, projectReleases);
  }
  await columnLoad;
}

export async function reportBug() {
  if (bugModal.bugModalCreating || !bugModal.showBugModal) return;
  if (!creation.bugCreation && (bugModal.bugModalLoading || !bugModal.bugModalProjectId || !bugModal.bugModalTitle.trim() || !bugModal.bugModalActual.trim() || !bugModal.bugModalColumns.some((column) => column.id === bugModal.bugModalColumnId))) {
    bugModal.bugModalError = 'Choose a project, a valid column, add a title, and describe what actually happened.';
    return;
  }
  const instance = creation.bugModalInstance;
  const actor = session.user?.id;
  // Capture every field before label resolution yields to the browser.
  const labelNames = bugModal.bugModalLabels.split(',').map((value) => value.trim()).filter(Boolean);
  const snapshot: TaskCreation = creation.bugCreation || {
    projectId: bugModal.bugModalProjectId,
    key: creationOperationKey(),
    input: {
      title: bugModal.bugModalTitle.trim(), description: bugModal.bugModalDescription.trim(), kind: 'bug', priority: bugModal.bugModalPriority,
      column_id: bugModal.bugModalColumnId,
      release_id: plannedReleaseIdForAssignment(bugModal.bugModalReleaseId, releasePlan.releasesByProject[bugModal.bugModalProjectId] || []) || null,
      bug: {
        actual_behavior: bugModal.bugModalActual.trim(), expected_behavior: bugModal.bugModalExpected.trim(),
        reproduction_steps: bugModal.bugModalReproduction.trim(), environment: bugModal.bugModalEnvironment.trim(),
        affected_version: bugModal.bugModalVersion.trim(), severity: bugModal.bugModalSeverity || undefined
      }
    }
  };
  if (!creation.bugCreation) rememberCreationRoute();
  bugModal.bugModalCreating = true;
  bugModal.bugModalError = '';
  try {
    if (!creation.bugCreation) {
      const labels = await resolveTaskLabels(snapshot.projectId, labelNames);
      if (actor !== session.user?.id || instance !== creation.bugModalInstance) return;
      snapshot.input.labels = labels;
      creation.bugCreation = snapshot;
    }
    const created = await reconcileCreation(() => api.createTask(snapshot.projectId, snapshot.input, snapshot.key));
    if (actor !== session.user?.id || instance !== creation.bugModalInstance) return;
    const createdScopes: TaskMutationScope[] = snapshot.projectId === workspace.activeProject?.id ? ['board', 'issues'] : ['issues'];
    recordTaskMutation(created.id, 'upsert', createdScopes);
    issues.issueTasks = [...issues.issueTasks.filter((task) => task.id !== created.id), created];
    if (snapshot.projectId === workspace.activeProject?.id) {
      board.tasks = [...board.tasks.filter((task) => task.id !== created.id), created];
      syncBoardTaskPages(created);
    }
    creation.bugCreation = null;
    bugModal.bugModalCreating = false;
    closeBugModal();
    toast('success', `${created.key} reported.`);
  } catch (error) {
    if (actor !== session.user?.id || instance !== creation.bugModalInstance) return;
    if (!ambiguousCreation(error)) creation.bugCreation = null;
    bugModal.bugModalError = creation.bugCreation
      ? creationError(error, 'The bug could not be reported.')
      : friendlyError(error, 'The bug could not be reported.');
  } finally {
    if (actor === session.user?.id && instance === creation.bugModalInstance) bugModal.bugModalCreating = false;
  }
}

export async function loadBugModalColumns(projectId: string) {
  const requestId = ++bugModal.bugModalColumnsRequest;
  bugModal.bugModalColumns = [];
  bugModal.bugModalColumnId = '';
  bugModal.bugModalError = '';
  if (!projectId) return;
  bugModal.bugModalLoading = true;
  try {
    const nextColumns = projectId === workspace.activeProject?.id && board.columns.length
      ? [...board.columns] : (await api.listAllColumns(projectId)).data;
    if (requestId !== bugModal.bugModalColumnsRequest || !bugModal.showBugModal || bugModal.bugModalProjectId !== projectId) return;
    bugModal.bugModalColumns = nextColumns;
    bugModal.bugModalColumnId = nextColumns.find((column) => column.semantic_state === 'ready')?.id || nextColumns[0]?.id || '';
  } catch (error) {
    if (requestId === bugModal.bugModalColumnsRequest && bugModal.showBugModal && bugModal.bugModalProjectId === projectId) {
      bugModal.bugModalError = friendlyError(error, 'This project’s columns could not be loaded.');
    }
  } finally {
    if (requestId === bugModal.bugModalColumnsRequest) bugModal.bugModalLoading = false;
  }
}

export async function changeBugModalProject() {
  if (bugModal.bugModalCreating || creation.bugCreation) return;
  bugModal.bugModalReleaseId = '';
  void loadProjectReleases(bugModal.bugModalProjectId).catch(() => []);
  await loadBugModalColumns(bugModal.bugModalProjectId);
}
