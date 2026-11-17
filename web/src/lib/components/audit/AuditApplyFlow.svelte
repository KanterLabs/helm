<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type AuditFinding, type Column, type Task } from '../../types';
  import AuditDialogs from './AuditDialogs.svelte';
  import type { createAuditFocus } from './focus';
  import type { AuditHelpers } from './helpers';
  import type { AuditNotice, AuditTaskUpdate } from './presentation';

  /**
   * Preview, confirmation and apply of one approved finding through the
   * guarded task move. Approval itself never moves a task.
   */
  export let helpers: AuditHelpers;
  export let columns: Column[] = [];
  export let focus: ReturnType<typeof createAuditFocus>;
  /** Project and session identity; a change discards late results. */
  export let sessionKey = '';
  export let onTaskUpdated: AuditTaskUpdate = () => undefined;
  export let onNotice: AuditNotice = () => undefined;
  export let onBlocked: (message: string) => void = () => undefined;

  let previewFinding: AuditFinding | null = null;
  let applyFinding: AuditFinding | null = null;
  let applyOriginFinding: AuditFinding | null = null;
  let applyError = '';
  let applying = false;
  let request = 0;

  onDestroy(() => { request += 1; });

  function current(requestedSession: string, requestId: number): boolean {
    return requestedSession === sessionKey && requestId === request;
  }

  export function openPreview(finding: AuditFinding): void {
    if (finding.review_state !== 'approved' || !helpers.auditReadyForReview()) return;
    if (helpers.changedSinceAudit(finding)) {
      onBlocked('This finding changed since the audit and cannot be previewed or applied.');
      return;
    }
    focus.rememberModalFocus();
    previewFinding = finding;
    applyFinding = null;
    applyOriginFinding = null;
    applyError = '';
    void tick().then(() => document.querySelector<HTMLElement>('[data-audit-preview-initial-focus]')?.focus());
  }

  function closePreview(): void {
    if (!previewFinding) return;
    previewFinding = null;
    applyFinding = null;
    applyOriginFinding = null;
    applyError = '';
    focus.restoreModalFocus();
  }

  function openApplyConfirmation(finding: AuditFinding): void {
    if (!helpers.canApply(finding)) {
      applyError = helpers.applyBlockReason(finding) || 'This recommendation is not ready to apply.';
      return;
    }
    previewFinding = null;
    applyFinding = finding;
    applyOriginFinding = finding;
    applyError = '';
    void tick().then(() => document.querySelector<HTMLElement>('[data-audit-apply-initial-focus]')?.focus());
  }

  function cancelApply(): void {
    const origin = applyOriginFinding;
    applyFinding = null;
    applyOriginFinding = null;
    applyError = '';
    if (origin) {
      previewFinding = origin;
      void tick().then(() => document.querySelector<HTMLElement>('[data-audit-preview-initial-focus]')?.focus());
    }
  }

  async function applyApprovedFinding(): Promise<void> {
    const finding = applyFinding;
    if (!finding || !helpers.canApply(finding) || applying) return;
    const task = helpers.findingTask(finding);
    const destination = columns.find((column) => column.semantic_state === helpers.destinationState(finding));
    if (!task || !destination) return;
    const requestId = ++request;
    const requestedSession = sessionKey;
    applying = true;
    applyError = '';
    try {
      const updated = await api.moveTask(task.id, {
        destination_column_id: destination.id,
        expected_source_column_id: helpers.columnIdentifier(finding.source_column),
        source: 'board_audit',
        reason: finding.reason
      }, task.version);
      if (!current(requestedSession, requestId)) return;
      onTaskUpdated(updated);
      onNotice('success', `${helpers.taskLabel(finding)} moved to ${destination.name}.`);
      focus.announce(`${helpers.taskLabel(finding)} moved to ${destination.name}.`);
      applyFinding = null;
      previewFinding = null;
      applyOriginFinding = null;
      focus.restoreModalFocus();
    } catch (error) {
      if (current(requestedSession, requestId)) {
        applyError = helpers.friendlyError(error, 'The recommendation could not be applied. Refresh and try again.');
        if (error instanceof ApiError && error.details.current) {
          onTaskUpdated(error.details.current as Task);
          applyError = 'The task changed before apply. It was not moved; review the current task and audit finding.';
        }
      }
    } finally {
      if (requestId === request) applying = false;
    }
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return;
    if (applyFinding) {
      cancelApply();
    } else if (previewFinding) {
      closePreview();
    }
  }
</script>

<svelte:window on:keydown={handleKeydown} />

<AuditDialogs {helpers} {previewFinding} {applyFinding} {applyError} {applying}
  {closePreview} {cancelApply} {openApplyConfirmation} {applyApprovedFinding} />
