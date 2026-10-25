// Drawer lifecycle actions, bug triage and comments.
import { api } from '../../lib/api';
import { dependencyActionExplanation, dependencyBlocked } from '../../lib/dependencies';
import { ApiError } from '../../lib/types';
import type { Comment, Task } from '../../lib/types';
import { requestConfirm, restoreDialogFocus } from '../dialogs';
import { friendlyError, toast } from '../feedback';
import { drawer } from '../state/drawer.svelte';
import { live } from '../state/live.svelte';
import { claimConflict, claimCountdown, claimExpiryExact, claimOwnerLabel } from '../taskQueries';
import { replaceTask } from '../taskSync';
import { drawerActionDraftFingerprint, drawerTaskDraftFingerprint, syncDraft } from './drafts';
import { saveDraftBeforeImmediateAction } from './save';
import { loadDrawerTimeline, refreshDrawerTimelineAfterMutation } from './timeline';

export async function runTaskAction(action: 'claim' | 'renew' | 'release' | 'complete' | 'block', reason = '') {
  if (!drawer.drawerTask) return;
  if (['claim', 'renew', 'complete'].includes(action) && dependencyBlocked(drawer.drawerTask)) {
    drawer.drawerError = dependencyActionExplanation(
      drawer.drawerTask,
      action === 'complete' ? 'complete this task' : action === 'renew' ? 'renew this claim' : 'claim this task'
    );
    return;
  }
  if (action === 'block' && !reason.trim()) {
    drawer.drawerError = 'Add a reason before blocking this task.';
    drawer.blockReasonOpen = true;
    return;
  }
  if (action === 'claim' && claimConflict(drawer.drawerTask, live.pulseClock)) {
    drawer.drawerError = `Claim conflict: ${claimOwnerLabel(drawer.drawerTask)} holds this task until ${claimExpiryExact(drawer.drawerTask.claim_expires_at)} (${claimCountdown(drawer.drawerTask, live.pulseClock)}). Current task version: v${drawer.drawerTask.version}.`;
    return;
  }
  drawer.taskActionLoading = drawer.drawerTask.id;
  drawer.drawerError = '';
  try {
    let updated: Task;
    if (action === 'claim') updated = await api.claimTask(drawer.drawerTask.id, drawer.drawerTask.version);
    else if (action === 'renew') updated = await api.renewTask(drawer.drawerTask.id, drawer.drawerTask.version);
    else if (action === 'release') updated = await api.releaseTask(drawer.drawerTask.id, drawer.drawerTask.version);
    else if (action === 'complete') updated = await api.completeTask(drawer.drawerTask.id, drawer.drawerTask.version);
    else updated = await api.blockTask(drawer.drawerTask.id, drawer.drawerTask.version, reason.trim());
    replaceTask(updated, true);
    if (action === 'block') {
      drawer.blockReasonOpen = false;
      drawer.blockReasonDraft = '';
    }
    refreshDrawerTimelineAfterMutation(updated.id);
    const actionLabel = action === 'renew' ? 'claim renewed' : action === 'release' ? 'released' : action === 'complete' ? 'completed' : `${action}ed`;
    toast('success', `${updated.key} ${actionLabel}.`);
  } catch (error) {
    drawer.drawerError = friendlyError(error, 'That task action could not be completed.');
    if (error instanceof ApiError && error.details.current) {
      replaceTask(error.details.current as Task);
      drawer.drawerError = action === 'claim'
        ? 'Claim conflict: this task changed or is held by another actor. Review the current lease below.'
        : 'This task changed in another session. Your draft was not overwritten.';
    }
  } finally {
    drawer.taskActionLoading = '';
  }
}

export function openBlockReason() {
  drawer.blockReasonOpen = true;
  drawer.blockReasonDraft = '';
  drawer.drawerError = '';
}

export async function triageBug() {
  if (!drawer.drawerTask?.bug) return;
  if (dependencyBlocked(drawer.drawerTask)) {
    drawer.drawerError = dependencyActionExplanation(drawer.drawerTask, 'start triage');
    return;
  }
  if (!(await saveDraftBeforeImmediateAction('triaging this issue'))) return;
  if (!drawer.drawerTask?.bug) return;
  drawer.taskActionLoading = drawer.drawerTask.id;
  drawer.drawerError = '';
  try {
    const updated = await api.triageTask(drawer.drawerTask.id, drawer.drawerTask.version, {
      severity: drawer.triageSeverityDraft,
      priority: drawer.draftPriority,
      assignee: drawer.draftAssignee.trim() || null
    });
    replaceTask(updated, true);
    syncDraft(updated);
    drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
    drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
    refreshDrawerTimelineAfterMutation(updated.id);
    toast('success', `${updated.key} triaged as ${drawer.triageSeverityDraft.toUpperCase()}.`);
  } catch (error) {
    drawer.drawerError = friendlyError(error, 'The issue could not be triaged. Refresh and try again.');
    if (error instanceof ApiError && error.details.current) replaceTask(error.details.current as Task);
  } finally {
    drawer.taskActionLoading = '';
  }
}

export async function resolveBug() {
  if (!drawer.drawerTask?.bug) return;
  if (dependencyBlocked(drawer.drawerTask)) {
    drawer.drawerError = dependencyActionExplanation(drawer.drawerTask, 'resolve this issue');
    return;
  }
  if (drawer.resolutionDraft === 'duplicate' && !drawer.duplicateOfDraft.trim()) {
    drawer.drawerError = 'Add the task key or ID this issue duplicates.';
    return;
  }
  if (!(await saveDraftBeforeImmediateAction('resolving this issue'))) return;
  if (!drawer.drawerTask?.bug) return;
  drawer.taskActionLoading = drawer.drawerTask.id;
  drawer.drawerError = '';
  try {
    const updated = await api.resolveTask(drawer.drawerTask.id, drawer.drawerTask.version, {
      resolution: drawer.resolutionDraft,
      duplicate_of: drawer.duplicateOfDraft.trim() || null,
      note: drawer.resolutionNoteDraft.trim() || undefined
    });
    replaceTask(updated, true);
    syncDraft(updated);
    drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
    drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
    refreshDrawerTimelineAfterMutation(updated.id);
    toast('success', `${updated.key} resolved.`);
  } catch (error) {
    drawer.drawerError = friendlyError(error, 'The issue could not be resolved. Refresh and try again.');
    if (error instanceof ApiError && error.details.current) replaceTask(error.details.current as Task);
  } finally {
    drawer.taskActionLoading = '';
  }
}

export async function reopenBug() {
  if (!drawer.drawerTask?.bug) return;
  if (!drawer.reopenReasonDraft.trim()) {
    drawer.drawerError = 'Explain why this issue is being reopened.';
    return;
  }
  if (!(await saveDraftBeforeImmediateAction('reopening this issue'))) return;
  if (!drawer.drawerTask?.bug) return;
  drawer.taskActionLoading = drawer.drawerTask.id;
  drawer.drawerError = '';
  try {
    const updated = await api.reopenTask(drawer.drawerTask.id, drawer.drawerTask.version, { reason: drawer.reopenReasonDraft.trim() });
    replaceTask(updated, true);
    syncDraft(updated);
    drawer.drawerSavedTaskDraftFingerprint = drawerTaskDraftFingerprint();
    drawer.drawerSavedActionDraftFingerprint = drawerActionDraftFingerprint();
    refreshDrawerTimelineAfterMutation(updated.id);
    toast('success', `${updated.key} reopened.`);
  } catch (error) {
    drawer.drawerError = friendlyError(error, 'The issue could not be reopened. Refresh and try again.');
    if (error instanceof ApiError && error.details.current) replaceTask(error.details.current as Task);
  } finally {
    drawer.taskActionLoading = '';
  }
}

export async function postComment() {
  if (!drawer.drawerTask || !drawer.commentBody.trim()) return;
  drawer.commentSending = true;
  const taskId = drawer.drawerTask.id;
  try {
    await api.postComment(taskId, drawer.commentBody.trim());
    drawer.commentBody = '';
    // The POST response is a comment resource without a timeline cursor.
    // Refresh one newest page so the canonical timeline item is merged once
    // and generated progress rows cannot be duplicated in the drawer.
    drawer.drawerTimelineRequest += 1;
    drawer.drawerTimelineLoading = false;
    drawer.drawerTimelineLoadingOlder = false;
    await loadDrawerTimeline(taskId);
    toast('success', 'Comment added.');
  } catch (error) {
    drawer.drawerError = friendlyError(error, 'Your comment could not be added.');
  } finally {
    drawer.commentSending = false;
  }
}

export async function editDrawerComment(comment: Comment, body: string): Promise<void> {
  if (!drawer.drawerTask || drawer.drawerTask.id !== comment.task_id) {
    throw new Error('The task changed while this comment was saving. Your draft was kept.');
  }
  const taskId = comment.task_id;
  try {
    const updatedComment = await api.patchComment(taskId, comment.id, body.trim(), comment.version ?? 1);
    drawer.drawerTimelineRequest += 1;
    drawer.drawerTimelineLoading = false;
    drawer.drawerTimelineLoadingOlder = false;
    await loadDrawerTimeline(taskId, { reconciliation: { updatedComments: new Map([[comment.id, updatedComment]]) } });
    toast('success', 'Comment updated.');
  } catch (error) {
    const message = friendlyError(error, 'Your comment could not be updated.');
    drawer.drawerError = message;
    throw new Error(message);
  }
}

export async function deleteDrawerComment(comment: Comment): Promise<void> {
  if (!drawer.drawerTask) return;
  const taskId = drawer.drawerTask.id;
  try {
    await api.deleteComment(taskId, comment.id, comment.version ?? 1);
    drawer.drawerTimelineRequest += 1;
    drawer.drawerTimelineLoading = false;
    drawer.drawerTimelineLoadingOlder = false;
    await loadDrawerTimeline(taskId, { reconciliation: { deletedCommentIds: [comment.id] } });
    toast('success', 'Comment deleted.');
  } catch (error) {
    const message = friendlyError(error, 'Your comment could not be deleted.');
    drawer.drawerError = message;
    throw new Error(message);
  } finally {
    restoreDialogFocus();
  }
}

export function confirmDrawerCommentDelete(comment: Comment): Promise<boolean> {
  return requestConfirm({
    title: 'Delete this comment?',
    message: 'The comment body will be removed, while the deletion remains recorded in activity.',
    confirmLabel: 'Delete comment',
    fallbackSelector: '#drawer-activity-tab'
  });
}
