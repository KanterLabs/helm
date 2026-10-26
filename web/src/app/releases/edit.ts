// Creating, editing, completing and reopening focus areas.
import { ApiError } from '../../lib/types';
import { rememberDialogFocus, requestConfirm, restoreDialogFocus } from '../dialogs';
import { friendlyError, toast } from '../feedback';
import { releasePlan } from '../state/releasePlan.svelte';
import { workspace } from '../state/workspace.svelte';
import type { ReleaseModel } from '../types';
import { callReleaseApi, loadReleaseQueue } from './load';

export function openReleaseModal(release?: ReleaseModel) {
  releasePlan.releaseEditingId = release?.id || '';
  releasePlan.releaseModalName = release?.name || '';
  releasePlan.releaseModalDescription = release?.description || '';
  releasePlan.releaseModalTargetDate = release?.target_date || '';
  releasePlan.releaseModalError = '';
  rememberDialogFocus('[data-release-modal-trigger]');
  releasePlan.showReleaseModal = true;
}

export function closeReleaseModal() {
  releasePlan.showReleaseModal = false;
  releasePlan.releaseModalSaving = false;
  restoreDialogFocus();
}

export function upsertRelease(updated: ReleaseModel) {
  const current = releasePlan.releasesByProject[updated.project_id] || [];
  const next = [...current.filter((release) => release.id !== updated.id), updated];
  releasePlan.releasesByProject = { ...releasePlan.releasesByProject, [updated.project_id]: next };
  if (workspace.activeProject?.id === updated.project_id) releasePlan.releases = next;
  releasePlan.releaseSelectedId = updated.id;
}

export async function saveRelease() {
  if (!workspace.activeProject || !releasePlan.releaseModalName.trim()) {
    releasePlan.releaseModalError = 'A focus needs a name.';
    return;
  }
  releasePlan.releaseModalSaving = true;
  releasePlan.releaseModalError = '';
  try {
    const input = {
      name: releasePlan.releaseModalName.trim(),
      description: releasePlan.releaseModalDescription.trim(),
      target_date: releasePlan.releaseModalTargetDate || null
    };
    const updated = releasePlan.releaseEditingId
      ? await callReleaseApi<ReleaseModel>('patchRelease', releasePlan.releaseEditingId, input, releasePlan.releases.find((release) => release.id === releasePlan.releaseEditingId)?.version)
      : await callReleaseApi<ReleaseModel>('createRelease', workspace.activeProject.id, input);
    upsertRelease(updated);
    closeReleaseModal();
    toast('success', `${updated.name} ${releasePlan.releaseEditingId ? 'updated' : 'created'}.`);
    await loadReleaseQueue(updated.id);
  } catch (error) {
    releasePlan.releaseModalError = friendlyError(error, 'The focus could not be saved.');
    const current = error instanceof ApiError && error.details?.current;
    if (current && typeof current === 'object' && 'id' in current) upsertRelease(current as ReleaseModel);
  } finally {
    releasePlan.releaseModalSaving = false;
  }
}

export async function deleteRelease(release: ReleaseModel) {
  const confirmed = await requestConfirm({
    title: `Delete ${release.name}?`,
    message: 'Only an empty active focus can be deleted. Task assignments are never cleared implicitly.',
    confirmLabel: 'Delete focus',
    fallbackSelector: '[data-release-modal-trigger]'
  });
  if (!confirmed) return;
  try {
    await callReleaseApi<unknown>('deleteRelease', release.id, release.version);
    const next = releasePlan.releases.filter((item) => item.id !== release.id);
    releasePlan.releases = next;
    releasePlan.releasesByProject = { ...releasePlan.releasesByProject, [release.project_id]: next };
    if (releasePlan.releaseSelectedId === release.id) {
      releasePlan.releaseSelectedId = next[0]?.id || '';
      releasePlan.releaseQueue = null;
      if (releasePlan.releaseSelectedId) void loadReleaseQueue(releasePlan.releaseSelectedId);
    }
    toast('success', `${release.name} deleted.`);
  } catch (error) {
    toast('error', friendlyError(error, 'The focus could not be deleted.'));
  } finally {
    restoreDialogFocus();
  }
}

export async function completeRelease(release: ReleaseModel) {
  const confirmed = await requestConfirm({
    title: `Complete ${release.name}?`,
    message: release.summary?.ready_to_release
      ? 'This will complete and freeze the focus after Helm rechecks every direct task and prerequisite.'
      : 'This focus is not ready yet. Helm will reject the action while any required work remains incomplete.',
    confirmLabel: 'Complete focus',
    fallbackSelector: `[data-release-id="${release.id}"]`
  });
  if (!confirmed) return;
  try {
    const updated = await callReleaseApi<ReleaseModel>('completeRelease', release.id, release.version);
    upsertRelease(updated);
    releasePlan.releaseQueue = null;
    await loadReleaseQueue(updated.id);
    toast('success', `${updated.name} is now completed.`);
  } catch (error) {
    toast('error', friendlyError(error, 'The focus could not be completed. Refresh its progress and try again.'));
    if (error instanceof ApiError && error.details?.current && typeof error.details.current === 'object') upsertRelease(error.details.current as ReleaseModel);
  } finally {
    restoreDialogFocus();
  }
}

export function openReleaseReopen(release: ReleaseModel) {
  releasePlan.releaseReopenTarget = release;
  releasePlan.releaseReopenReason = '';
  rememberDialogFocus(`[data-release-id="${release.id}"]`);
}

export function closeReleaseReopen() {
  releasePlan.releaseReopenTarget = null;
  releasePlan.releaseReopenReason = '';
  releasePlan.releaseReopening = false;
  restoreDialogFocus();
}

export async function reopenRelease() {
  const release = releasePlan.releaseReopenTarget;
  if (!release || !releasePlan.releaseReopenReason.trim()) return;
  releasePlan.releaseReopening = true;
  try {
    const updated = await callReleaseApi<ReleaseModel>('reopenRelease', release.id, release.version, { reason: releasePlan.releaseReopenReason.trim() });
    upsertRelease(updated);
    closeReleaseReopen();
    await loadReleaseQueue(updated.id);
    toast('success', `${updated.name} restored to active.`);
  } catch (error) {
    toast('error', friendlyError(error, 'The focus could not be reopened.'));
    if (error instanceof ApiError && error.details?.current && typeof error.details.current === 'object') upsertRelease(error.details.current as ReleaseModel);
  } finally {
    releasePlan.releaseReopening = false;
  }
}
