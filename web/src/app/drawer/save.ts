// Saving and deleting drawer tasks and resolving their labels.
import { api } from '../../lib/api';
import { dateToIso } from '../../lib/state';
import { ApiError } from '../../lib/types';
import type { Label, Task } from '../../lib/types';
import { removeTaskFromBoardPages } from '../board/pages';
import { labelPalette } from '../constants';
import { requestConfirm, restoreDialogFocus } from '../dialogs';
import { announce, friendlyError, toast } from '../feedback';
import { board } from '../state/board.svelte';
import { drawer } from '../state/drawer.svelte';
import { issues } from '../state/issues.svelte';
import { myWork } from '../state/myWork.svelte';
import { session } from '../state/session.svelte';
import { recordTaskMutation, replaceTask } from '../taskSync';
import { closeDrawer, drawerTaskDraftFingerprint, syncTaskDraft } from './drafts';

export function findProjectLabel(projectId: string, value: string): Label | undefined {
  const normalized = value.trim().toLowerCase();
  return board.labels.find(
    (label) =>
      label.project_id === projectId &&
      (label.id === value.trim() || label.name.trim().toLowerCase() === normalized)
  );
}

export function mergeProjectLabels(projectId: string, next: Label[]) {
  const projectLabels = new Map(board.labels.filter((label) => label.project_id === projectId).map((label) => [label.id, label]));
  next.filter((label) => label.project_id === projectId).forEach((label) => projectLabels.set(label.id, label));
  board.labels = [...board.labels.filter((label) => label.project_id !== projectId), ...projectLabels.values()];
}

export async function reloadProjectLabels(projectId: string): Promise<Label[]> {
  const refreshed = (await api.listAllLabels(projectId)).data;
  board.labels = [...board.labels.filter((label) => label.project_id !== projectId), ...refreshed];
  return refreshed;
}

export function unresolvedLabelError(names: string[]): Error {
  const quoted = names.map((name) => `"${name}"`).join(', ');
  return new Error(`Could not resolve label${names.length === 1 ? '' : 's'}: ${quoted}. Check the names and try again.`);
}

export async function resolveTaskLabels(projectId: string, names: string[]): Promise<string[]> {
  const uniqueNames = Array.from(
    new Map(names.map((name) => [name.trim().toLowerCase(), name.trim()])).values()
  );
  const resolved: Label[] = [];
  const unresolved: string[] = [];

  for (const name of uniqueNames) {
    let label = findProjectLabel(projectId, name);
    if (!label) {
      try {
        const projectLabelCount = board.labels.filter((item) => item.project_id === projectId).length;
        const created = await api.createLabel(projectId, {
          name,
          color: labelPalette[projectLabelCount % labelPalette.length]
        });
        mergeProjectLabels(projectId, [created]);
        label = findProjectLabel(projectId, name);
      } catch {
        // A concurrent actor may have created this label between our local
        // lookup and POST. Refresh the authoritative list before failing.
        try {
          await reloadProjectLabels(projectId);
        } catch {
          // The unresolved-label error below remains actionable even when
          // the recovery request itself is unavailable.
        }
        label = findProjectLabel(projectId, name);
      }
    }
    if (label) resolved.push(label);
    else unresolved.push(name);
  }

  if (unresolved.length) throw unresolvedLabelError(unresolved);

  return resolved.map((label) => label.id);
}

export async function deleteProjectLabel(label: Label) {
  const confirmed = await requestConfirm({
    title: `Delete label “${label.name}”?`,
    message: `The ${label.name} label will be removed from tasks in this project.`,
    confirmLabel: 'Delete label',
    fallbackSelector: '#drawer-title'
  });
  if (!confirmed) return;
  drawer.labelDeleting = label.id;
  try {
    await api.deleteLabel(label.id);
    board.labels = board.labels.filter((item) => item.id !== label.id);
    if (board.filters.label === label.id) board.filters = { ...board.filters, label: 'all' };
    board.tasks = board.tasks.map((task) => task.labels?.some((item) => item.id === label.id)
      ? { ...task, labels: task.labels.filter((item) => item.id !== label.id) }
      : task);
    if (drawer.drawerTask?.project_id === label.project_id) {
      drawer.drawerTask = { ...drawer.drawerTask, labels: (drawer.drawerTask.labels || []).filter((item) => item.id !== label.id) };
      drawer.draftLabels = drawer.draftLabels
        .split(',')
        .map((value) => value.trim())
        .filter((value) => value && value.toLowerCase() !== label.name.toLowerCase())
        .join(', ');
    }
    restoreDialogFocus();
    toast('success', `${label.name} deleted.`);
  } catch (error) {
    restoreDialogFocus();
    toast('error', friendlyError(error, 'The label could not be deleted.'));
  } finally {
    drawer.labelDeleting = '';
  }
}

export async function saveTask(silent = false): Promise<boolean> {
  if (drawer.drawerSaving) return false;
  if (!drawer.drawerTask || !drawer.draftTitle.trim()) {
    drawer.drawerError = 'A task needs a title.';
    return false;
  }
  if (drawer.drawerTask.kind === 'bug' && !drawer.draftBugActual.trim()) {
    drawer.drawerError = 'A bug report needs actual behavior.';
    return false;
  }
  const task = drawer.drawerTask;
  const requestedSession = session.sessionGeneration;
  const requestedDrawer = drawer.taskDetailRequest;
  const submittedFingerprint = drawerTaskDraftFingerprint();
  const sessionIsCurrent = () => Boolean(session.user && session.sessionGeneration === requestedSession);
  const drawerIsCurrent = () => sessionIsCurrent()
    && drawer.taskDetailRequest === requestedDrawer
    && drawer.drawerTask?.id === task.id;
  // Capture the entire submission before label creation yields. Navigation
  // and further typing must never change the task or fields being saved.
  const labelNames = drawer.draftLabels.split(',').map((value) => value.trim()).filter(Boolean);
  const patch = {
    title: drawer.draftTitle.trim(),
    description: drawer.draftDescription,
    priority: drawer.draftPriority,
    due_at: dateToIso(drawer.draftDueDate),
    assignee: drawer.draftAssignee.trim() || null,
    release_id: drawer.draftReleaseId || null,
    ...(task.kind === 'bug'
      ? {
          bug: {
            actual_behavior: drawer.draftBugActual.trim(),
            expected_behavior: drawer.draftBugExpected.trim(),
            reproduction_steps: drawer.draftBugReproduction.trim(),
            environment: drawer.draftBugEnvironment.trim(),
            affected_version: drawer.draftBugVersion.trim()
          }
        }
      : {})
  };
  drawer.drawerSaving = true;
  drawer.drawerError = '';
  try {
    const labelIds = await resolveTaskLabels(task.project_id, labelNames);
    if (!sessionIsCurrent()) return false;
    const updated = await api.patchTask(
      task.id,
      { ...patch, labels: labelIds, label_ids: labelIds },
      task.version
    );
    if (!sessionIsCurrent()) return false;
    replaceTask(updated, true);
    if (drawerIsCurrent() && drawer.drawerTask) {
      // Normalize only the submitted draft. Newer edits and drafts in a
      // reopened drawer belong to their own save; action drafts are separate.
      if (drawerTaskDraftFingerprint() === submittedFingerprint) {
        syncTaskDraft(drawer.drawerTask);
        drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
      } else {
        drawer.drawerSavedTaskDraftFingerprint = submittedFingerprint;
      }
    }
    if (!silent) toast('success', `${updated.key} saved.`);
    // An immediate action may proceed only for the drawer whose complete
    // draft was saved, never for a task opened while this request waited.
    return drawerIsCurrent() && drawerTaskDraftFingerprint() === drawer.drawerSavedTaskDraftFingerprint;
  } catch (error) {
    if (!sessionIsCurrent()) return false;
    if (error instanceof ApiError && error.details.current) {
      replaceTask(error.details.current as Task);
    }
    if (drawerIsCurrent()) {
      drawer.drawerError = error instanceof ApiError && error.details.current
        ? 'This task changed in another session. Your draft was not overwritten.'
        : friendlyError(error, 'The task changed elsewhere. Refresh and try again.');
    } else if (!silent) {
      toast('error', `${task.key}: ${friendlyError(error, 'The task could not be saved.')}`);
    }
    return false;
  } finally {
    drawer.drawerSaving = false;
  }
}

export async function deleteDrawerTask() {
  const task = drawer.drawerTask;
  if (!task) return;
  const confirmed = await requestConfirm({
    title: `Delete ${task.key}?`,
    message: `“${task.title}” will be removed from the board. You can undo this deletion from the next notification.`,
    confirmLabel: 'Delete task',
    fallbackSelector: '[data-destructive-focus-target]'
  });
  if (!confirmed) return;
  drawer.taskActionLoading = task.id;
  drawer.drawerError = '';
  try {
    await api.deleteTask(task.id, task.version);
    recordTaskMutation(task.id, 'remove', ['board', 'issues', 'my-work-live', 'my-work-assigned']);
    board.tasks = board.tasks.filter((item) => item.id !== task.id);
    removeTaskFromBoardPages(task.id);
    issues.issueTasks = issues.issueTasks.filter((item) => item.id !== task.id);
    myWork.myWorkTasks = myWork.myWorkTasks.filter((item) => item.id !== task.id);
    closeDrawer(true);
    toast('success', `${task.key} deleted.`, {
      label: 'Undo',
      run: () => undoTaskDelete(task)
    });
  } catch (error) {
    restoreDialogFocus();
    drawer.drawerError = friendlyError(error, 'The task could not be deleted. Refresh and try again.');
    if (error instanceof ApiError && error.details.current) {
      replaceTask(error.details.current as Task);
      drawer.drawerError = 'This task changed in another session. Refresh and try again.';
    }
  } finally {
    drawer.taskActionLoading = '';
  }
}

export async function undoTaskDelete(deletedTask: Task) {
  try {
    // DELETE increments the task version atomically. The restore endpoint
    // accepts that deleted version, so a concurrent edit or second restore
    // fails closed with a conflict instead of resurrecting stale data.
    const restored = await api.restoreTask(deletedTask.id, deletedTask.version + 1);
    replaceTask(restored, true);
    announce(`${restored.key} restored to the board.`);
  } catch (error) {
    toast('error', friendlyError(error, 'Undo could not be applied because the task changed elsewhere.'));
  }
}

export async function saveDraftBeforeImmediateAction(actionLabel: string): Promise<boolean> {
  // Triage/resolve/reopen submit their own action-only drafts. Only task
  // fields need a preceding PATCH; comments, block reasons, and lifecycle
  // options must remain available to the action that consumes them.
  if (drawerTaskDraftFingerprint() === drawer.drawerSavedTaskDraftFingerprint) return true;
  const saved = await saveTask(true);
  if (!saved) {
    drawer.drawerError = drawer.drawerError || `Save your changes before ${actionLabel}.`;
    return false;
  }
  return true;
}
