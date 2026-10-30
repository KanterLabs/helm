// The ordinary new-task modal. Luna drafting is retired from the form
// (TC-318); the assist functions below stay dormant for existing compile-time
// consumers until TC-325 retires the shared drafting runtime.
import { tick } from 'svelte';
import { api } from '../lib/api';
import { dateToIso } from '../lib/state';
import { ApiError } from '../lib/types';
import type { Priority, TaskDraftSuggestion } from '../lib/types';
import { plannedReleaseIdForAssignment } from './appHelpers';
import { syncBoardTaskPages } from './board/pages';
import { ambiguousCreation, creationError, creationOperationKey, hasPendingCreation, openProjectModal, reconcileCreation, rememberCreationRoute } from './creation';
import { rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { loadProjectReleases } from './releases/load';
import { board } from './state/board.svelte';
import { creation } from './state/creation.svelte';
import { dialogs } from './state/dialogs.svelte';
import { drawer } from './state/drawer.svelte';
import { nav } from './state/nav.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { taskModal } from './state/taskModal.svelte';
import { workspace } from './state/workspace.svelte';
import { taskReleaseId } from './taskQueries';
import { recordTaskMutation } from './taskSync';
import type { DialogReturnFocus, TaskModalSuggestionField } from './types';

export function openTaskModalFromCommand(returnFocus: DialogReturnFocus | null) {
  const opening = openTaskModal();
  if (returnFocus) dialogs.dialogReturnFocus = returnFocus;
  return opening;
}

// Fields a person typed. Project, column and an inherited/filter Focus are
// chosen for them, so they alone never make the form a draft worth guarding.
export function taskModalHasManualDraft(): boolean {
  return Boolean(taskModal.taskModalTitle.trim() || taskModal.taskModalDescription.trim() || taskModal.taskModalDueDate
    || taskModal.taskModalAssignee.trim() || taskModal.taskModalPriority !== 'normal');
}

// User dismissal (Escape, backdrop, Cancel, ×). Like the task drawer, a typed
// draft is discarded only after explicit confirmation.
export function closeTaskModal() {
  if (taskModal.taskModalCreating || creation.taskCreation) return;
  if (taskModalHasManualDraft() && !window.confirm('Discard this unsaved task? Its title and details will be lost.')) return;
  dismissTaskModal();
}

function dismissTaskModal() {
  creation.taskModalInstance += 1;
  taskModal.taskModalAssistController?.abort();
  taskModal.taskModalAssistController = null;
  taskModal.showTaskModal = false;
  taskModal.taskModalLoading = false;
  taskModal.taskModalColumnsRequest += 1;
  restoreDialogFocus();
}

export async function openTaskModal(parentTaskId: string | null = null) {
  if (hasPendingCreation()) return;
  const instance = ++creation.taskModalInstance;
  if (!workspace.navProjects.length) {
    openProjectModal();
    return;
  }
  taskModal.taskModalProjectId = (workspace.activeProject && !workspace.activeProject.system_kind ? workspace.activeProject.id : '') || workspace.navProjects[0].id;
  taskModal.taskModalColumnId = '';
  taskModal.taskModalColumns = workspace.activeProject?.id === taskModal.taskModalProjectId ? [...board.columns] : [];
  taskModal.taskModalTitle = '';
  taskModal.taskModalDescription = '';
  taskModal.taskModalPriority = 'normal';
  taskModal.taskModalDueDate = '';
  taskModal.taskModalAssignee = '';
  const parentReleaseId = parentTaskId && drawer.drawerTask?.id === parentTaskId ? taskReleaseId(drawer.drawerTask) : '';
  const inheritsParentRelease = Boolean(parentTaskId && drawer.drawerTask?.id === parentTaskId);
  taskModal.taskModalReleaseId = '';
  taskModal.taskModalParentId = parentTaskId;
  taskModal.taskModalIdea = '';
  taskModal.taskModalSuggestion = null;
  taskModal.taskModalAssisting = false;
  taskModal.taskModalAssistStage = '';
  taskModal.taskModalNeedsCodex = false;
  taskModal.taskModalError = '';
  resetTaskModalSuggestionState();
  const requestedTaskModalProjectId = taskModal.taskModalProjectId;
  rememberDialogFocus('[data-task-modal-trigger]');
  taskModal.showTaskModal = true;
  nav.projectSwitcherOpen = false;
  const columnLoad = loadTaskModalColumns(taskModal.taskModalProjectId);
  const releaseRequest = taskModal.taskModalColumnsRequest;
  const projectReleases = await loadProjectReleases(taskModal.taskModalProjectId).catch(() => []);
  if (taskModal.showTaskModal && creation.taskModalInstance === instance && releaseRequest === taskModal.taskModalColumnsRequest && taskModal.taskModalProjectId === requestedTaskModalProjectId && !creation.taskCreation) {
    const requestedRelease = inheritsParentRelease ? parentReleaseId : board.boardReleaseFilter;
    taskModal.taskModalReleaseId = plannedReleaseIdForAssignment(requestedRelease, projectReleases);
  }
  await columnLoad;
}

export async function loadTaskModalColumns(projectId: string) {
  const requestId = ++taskModal.taskModalColumnsRequest;
  taskModal.taskModalColumns = [];
  taskModal.taskModalColumnId = '';
  taskModal.taskModalError = '';
  if (!projectId) return;
  const requestedProjectId = projectId;
  taskModal.taskModalLoading = true;
  try {
    const nextColumns = projectId === workspace.activeProject?.id && board.columns.length ? [...board.columns] : (await api.listAllColumns(projectId)).data;
    if (requestId !== taskModal.taskModalColumnsRequest || !taskModal.showTaskModal || taskModal.taskModalProjectId !== requestedProjectId) return;
    taskModal.taskModalColumns = nextColumns;
    taskModal.taskModalColumnId = taskModal.taskModalColumns.find((column) => column.semantic_state === 'ready')?.id || taskModal.taskModalColumns[0]?.id || '';
  } catch (error) {
    if (requestId === taskModal.taskModalColumnsRequest && taskModal.showTaskModal && taskModal.taskModalProjectId === requestedProjectId) {
      taskModal.taskModalError = friendlyError(error, 'This project’s columns could not be loaded.');
    }
  } finally {
    if (requestId === taskModal.taskModalColumnsRequest) taskModal.taskModalLoading = false;
  }
}

export function changeTaskModalProject() {
		if (taskModal.taskModalCreating || creation.taskCreation) return;
		cancelTaskAssist();
		taskModal.taskModalSuggestion = null;
		taskModal.taskModalNeedsCodex = false;
		resetTaskModalSuggestionState();
		// A parent is project-local. If the user changes the destination project,
		// clear the inherited relationship instead of submitting a guaranteed
		// cross-project mutation.
		taskModal.taskModalParentId = null;
		taskModal.taskModalReleaseId = '';
		void loadProjectReleases(taskModal.taskModalProjectId).catch(() => []);
		void loadTaskModalColumns(taskModal.taskModalProjectId);
	}

export function resetTaskModalSuggestionState() {
  taskModal.taskModalAppliedFields = { title: false, description: false, priority: false };
  taskModal.taskModalApplyNotice = '';
  taskModal.taskModalHasAppliedAll = false;
  taskModal.taskModalSuggestionCollapsed = false;
}

export function suggestionDescription(suggestion: TaskDraftSuggestion): string {
  const criteria = suggestion.acceptance_criteria.map((criterion) => `- [ ] ${criterion}`).join('\n');
  return [suggestion.description.trim(), `## Acceptance criteria\n${criteria}`].filter(Boolean).join('\n\n');
}

export function lunaFieldLabel(field: TaskModalSuggestionField): string {
  return field === 'description' ? 'Description' : field === 'priority' ? 'Priority' : 'Title';
}

export function lunaSuggestionFieldValue(field: TaskModalSuggestionField): string {
  if (!taskModal.taskModalSuggestion) return '';
  if (field === 'description') return suggestionDescription(taskModal.taskModalSuggestion);
  if (field === 'priority') return taskModal.taskModalSuggestion.priority;
  return taskModal.taskModalSuggestion.title;
}

export function applyLunaField(field: TaskModalSuggestionField) {
  if (!taskModal.taskModalSuggestion) return;
  const value = lunaSuggestionFieldValue(field);
  if (field === 'title') taskModal.taskModalTitle = value;
  if (field === 'description') taskModal.taskModalDescription = value;
  if (field === 'priority') taskModal.taskModalPriority = value as Priority;
  taskModal.taskModalAppliedFields = { ...taskModal.taskModalAppliedFields, [field]: true };
  taskModal.taskModalApplyNotice = `${lunaFieldLabel(field)} suggestion applied to the task form.`;
}

export function applyAllLunaFields() {
  applyLunaField('title');
  applyLunaField('description');
  applyLunaField('priority');
  taskModal.taskModalHasAppliedAll = true;
  taskModal.taskModalSuggestionCollapsed = true;
  taskModal.taskModalApplyNotice = 'Luna applied all suggested fields. Review the populated task details before creating.';
}

export async function assistTaskWithLuna() {
  const query = taskModal.taskModalIdea.trim() || taskModal.taskModalTitle.trim();
  if (!taskModal.taskModalProjectId || !query) {
    taskModal.taskModalError = 'Choose a project and describe the task idea before asking Luna.';
    return;
  }
  taskModal.taskModalAssistController?.abort();
  const controller = new AbortController();
  taskModal.taskModalAssistController = controller;
  taskModal.taskModalAssisting = true;
  taskModal.taskModalAssistStage = 'Reviewing relevant project history…';
  taskModal.taskModalError = '';
  taskModal.taskModalNeedsCodex = false;
  resetTaskModalSuggestionState();
  try {
    const suggestion = await api.draftTask(taskModal.taskModalProjectId, query, controller.signal);
    if (controller.signal.aborted || taskModal.taskModalAssistController !== controller) return;
    taskModal.taskModalAssistStage = 'Checking the draft and priority…';
    await tick();
    taskModal.taskModalSuggestion = suggestion;
    resetTaskModalSuggestionState();
  } catch (error) {
    if (controller.signal.aborted) return;
    if (error instanceof ApiError && error.code === 'codex_not_connected') {
      settings.codexAccount = { connected: false, requires_openai_auth: true };
      taskModal.taskModalNeedsCodex = true;
    } else {
      taskModal.taskModalError = friendlyError(error, 'Luna is unavailable right now. You can retry or keep creating the task manually.');
    }
  } finally {
    if (taskModal.taskModalAssistController === controller) {
      taskModal.taskModalAssistController = null;
      taskModal.taskModalAssisting = false;
      taskModal.taskModalAssistStage = '';
    }
  }
}

export function cancelTaskAssist() {
  taskModal.taskModalAssistController?.abort();
  taskModal.taskModalAssistController = null;
  taskModal.taskModalAssisting = false;
  taskModal.taskModalAssistStage = '';
}

export async function createGlobalTask() {
  if (taskModal.taskModalCreating || !taskModal.showTaskModal) return;
  if (!creation.taskCreation) {
    if (taskModal.taskModalLoading || !taskModal.taskModalProjectId || !taskModal.taskModalTitle.trim() || !taskModal.taskModalColumns.some((column) => column.id === taskModal.taskModalColumnId)) {
      taskModal.taskModalError = taskModal.taskModalTitle.trim() ? 'Choose a project and one of its columns.' : 'Add a task title.';
      return;
    }
    cancelTaskAssist();
    rememberCreationRoute();
    creation.taskCreation = {
      projectId: taskModal.taskModalProjectId,
      key: creationOperationKey(),
      input: {
        title: taskModal.taskModalTitle.trim(), description: taskModal.taskModalDescription.trim(), priority: taskModal.taskModalPriority,
        column_id: taskModal.taskModalColumnId, due_at: dateToIso(taskModal.taskModalDueDate), assignee: taskModal.taskModalAssignee.trim() || null,
        release_id: plannedReleaseIdForAssignment(taskModal.taskModalReleaseId, releasePlan.releasesByProject[taskModal.taskModalProjectId] || []) || null,
        parent_task_id: taskModal.taskModalParentId
      }
    };
  }
  const operation = creation.taskCreation;
  const instance = creation.taskModalInstance;
  const actor = session.user?.id;
  taskModal.taskModalCreating = true;
  taskModal.taskModalError = '';
  try {
    const created = await reconcileCreation(() => api.createTask(operation.projectId, operation.input, operation.key));
    if (actor !== session.user?.id || instance !== creation.taskModalInstance) return;
    recordTaskMutation(created.id, 'upsert', ['board']);
    if (operation.projectId === workspace.activeProject?.id) {
      board.tasks = [...board.tasks.filter((task) => task.id !== created.id), created];
      syncBoardTaskPages(created);
    }
    creation.taskCreation = null;
    taskModal.taskModalCreating = false;
    dismissTaskModal();
    toast('success', `${created.key} created in ${workspace.projects.find((project) => project.id === operation.projectId)?.name || 'your project'}.`);
  } catch (error) {
    if (actor !== session.user?.id || instance !== creation.taskModalInstance) return;
    if (!ambiguousCreation(error)) creation.taskCreation = null;
    taskModal.taskModalError = creationError(error, 'The task could not be created.');
  } finally {
    if (actor === session.user?.id && instance === creation.taskModalInstance) taskModal.taskModalCreating = false;
  }
}
