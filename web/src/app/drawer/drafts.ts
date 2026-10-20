// Drawer draft synchronization, dirty tracking and closing.
import { actorId, toInputDate } from '../../lib/state';
import type { Task } from '../../lib/types';
import { restoreDialogFocus } from '../dialogs';
import { isTaskLocation, navigate } from '../routing';
import { drawer } from '../state/drawer.svelte';
import { live } from '../state/live.svelte';
import { workspace } from '../state/workspace.svelte';
import { taskReleaseId } from '../taskQueries';
import { restoreTaskRouteOrigin } from './open';

export function syncTaskDraft(task: Task) {
  drawer.draftTitle = task.title;
  drawer.draftDescription = task.description || '';
  drawer.draftPriority = task.priority;
  drawer.draftDueDate = toInputDate(task.due_at);
  drawer.draftAssignee = actorId(task.assignee);
  drawer.draftReleaseId = taskReleaseId(task);
  drawer.draftLabels = (task.labels || []).map((label) => label.name).join(', ');
  drawer.draftBugActual = task.bug?.actual_behavior || '';
  drawer.draftBugExpected = task.bug?.expected_behavior || '';
  drawer.draftBugReproduction = task.bug?.reproduction_steps || '';
  drawer.draftBugEnvironment = task.bug?.environment || '';
  drawer.draftBugVersion = task.bug?.affected_version || '';
}

export function syncActionDraft(task: Task) {
  drawer.triageSeverityDraft = task.bug?.severity || 's3';
  drawer.resolutionDraft = task.bug?.resolution || 'fixed';
  drawer.duplicateOfDraft = task.bug?.duplicate_of || '';
  drawer.resolutionNoteDraft = '';
  drawer.reopenReasonDraft = '';
}

export function syncDraft(task: Task, resetActionDrafts = true) {
  syncTaskDraft(task);
  if (resetActionDrafts) {
    syncActionDraft(task);
  }
}

export function syncCleanDrawerDrafts(authoritative: Task) {
  if (!drawer.drawerTask || drawer.drawerTask.id !== authoritative.id) return;
  if (drawerTaskDraftFingerprint() === drawer.drawerSavedTaskDraftFingerprint) {
    syncTaskDraft(authoritative);
    drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
  }
  if (drawerActionDraftFingerprint() === drawer.drawerSavedActionDraftFingerprint) {
    syncActionDraft(authoritative);
    drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
  }
}

export function drawerTaskDraftFingerprint(): string {
  return JSON.stringify(drawerTaskDraftValues());
}

export function drawerTaskDraftValues(): unknown[] {
  return [
    drawer.draftTitle,
    drawer.draftDescription,
    drawer.draftPriority,
    drawer.draftDueDate,
    drawer.draftAssignee,
    drawer.draftReleaseId,
    drawer.draftLabels,
    drawer.draftBugActual,
    drawer.draftBugExpected,
    drawer.draftBugReproduction,
    drawer.draftBugEnvironment,
    drawer.draftBugVersion
  ];
}

export function drawerActionDraftFingerprint(): string {
  return JSON.stringify(drawerActionDraftValues());
}

export function drawerActionDraftValues(): unknown[] {
  return [
    drawer.triageSeverityDraft,
    drawer.resolutionDraft,
    drawer.duplicateOfDraft,
    drawer.resolutionNoteDraft,
    drawer.reopenReasonDraft,
    drawer.blockReasonDraft,
    drawer.commentBody
  ];
}

export function drawerDraftFingerprint(): string {
  return JSON.stringify([...drawerTaskDraftValues(), ...drawerActionDraftValues()]);
}

export function confirmDrawerDiscard(): boolean {
  if (!drawer.drawerDraftDirty) return true;
  return window.confirm('You have unsaved task details. Discard them and close the drawer?');
}

export function confirmDrawerTaskSwitch(task: Task): boolean {
  if (!drawer.drawerTask || drawer.drawerTask.id === task.id) return true;
  return confirmDrawerDiscard();
}

export function closeDrawer(force = false, skipRouteNavigation = false): boolean {
  if (!force && !confirmDrawerDiscard()) return false;
  const routeOrigin = drawer.taskRouteOrigin;
  drawer.taskDetailRequest += 1;
  live.drawerLivenessRequest += 1;
  drawer.drawerTask = null;
  drawer.editedCommentDirty = false;
  drawer.editedCommentPending = false;
  drawer.checklistDirty = false;
  drawer.checklistPending = false;
  drawer.drawerDependencyPanel = null;
  drawer.drawerHierarchyPanel = null;
  drawer.taskRouteOrigin = '';
  drawer.taskRouteIntent = 'details';
  drawer.drawerView = 'details';
  drawer.drawerError = '';
  drawer.drawerTimelineRequest += 1;
  drawer.drawerTimelineItems = [];
  drawer.drawerTimelineNextCursor = '';
  drawer.drawerTimelineTaskId = '';
  drawer.drawerTimelineError = '';
  drawer.drawerTimelineLoading = false;
  drawer.drawerTimelineLoadingOlder = false;
  drawer.blockReasonDraft = '';
  drawer.blockReasonOpen = false;
  drawer.drawerSavedTaskDraftFingerprint = '';
  drawer.drawerSavedActionDraftFingerprint = '';
  let originRendered: Promise<unknown> = Promise.resolve();
  if (!skipRouteNavigation && isTaskLocation()) {
    const destination = routeOrigin || `/p/${encodeURIComponent(workspace.activeProjectSlug)}`;
    navigate(destination);
    originRendered = Promise.resolve(restoreTaskRouteOrigin(destination));
  }
  restoreDialogFocus(originRendered);
  return true;
}
