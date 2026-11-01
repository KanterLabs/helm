<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type AuditFinding, type Column, type GithubApplyResult, type Task } from '../../types';
  import AuditDialogs from './AuditDialogs.svelte';
  import type { createAuditFocus } from './focus';
  import type { AuditHelpers } from './helpers';
  import type { AuditNotice, AuditTaskUpdate } from './presentation';

  /**
   * Preview, confirmation and apply of one approved finding. External runs use
   * the guarded task move; server-run audits use the audit apply endpoint,
   * which rechecks every guard. Approval itself never moves a task.
   */
  export let helpers: AuditHelpers;
  export let columns: Column[] = [];
  export let auditId: string;
  export let serverRun = false;
  export let focus: ReturnType<typeof createAuditFocus>;
  /** Project and session identity; a change discards late results. */
  export let sessionKey = '';
  export let onTaskUpdated: AuditTaskUpdate = () => undefined;
  export let onNotice: AuditNotice = () => undefined;
  export let onBlocked: (message: string) => void = () => undefined;
  export let onUnavailable: (message: string) => void = () => undefined;
  export let onTaskLoaded: (task: Task) => void = () => undefined;

  let previewFinding: AuditFinding | null = null;
  let applyFinding: AuditFinding | null = null;
  let applyOriginFinding: AuditFinding | null = null;
  let applyError = '';
  let applying = false;
  let serverPreview: GithubApplyResult | null = null;
  let serverPreviewLoading = false;
  let request = 0;
  let destroyed = false;

  onDestroy(() => { destroyed = true; request += 1; });

  function current(requestedSession: string, requestId: number): boolean {
    return requestedSession === sessionKey && requestId === request;
  }

  function unavailable(error: unknown): boolean {
    if (!(error instanceof ApiError) || !(error.code === 'evidence_unavailable' || [401, 403, 404].includes(error.status))) return false;
    onUnavailable(helpers.friendlyError(error, 'Source access is unavailable.'));
    return true;
  }

  async function checkServerPreview(finding: AuditFinding): Promise<void> {
    const requestedAudit = auditId;
    serverPreview = null;
    serverPreviewLoading = true;
    try {
      // The board may not hold this task; apply needs its current version and column.
      if (!helpers.findingTask(finding)) {
        const current = await api.getTask(finding.task_id).catch(() => null);
        if (destroyed) return;
        if (current) onTaskLoaded(current);
      }
      const result = await api.applyAuditFindings(requestedAudit, [finding.id], true);
      if (!destroyed && previewFinding?.id === finding.id) serverPreview = result.data.find((item) => item.finding_id === finding.id) || null;
    } catch (error) {
      if (destroyed || unavailable(error)) return;
      if (previewFinding?.id === finding.id) applyError = helpers.friendlyError(error, 'The server could not check this recommendation.');
    } finally {
      serverPreviewLoading = false;
    }
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
    if (serverRun) void checkServerPreview(finding);
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
    if (serverRun ? serverPreview?.outcome !== 'eligible' : !helpers.canApply(finding)) {
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

  async function applyOnServer(finding: AuditFinding): Promise<void> {
    if (applying) return;
    const requestId = ++request;
    const requestedSession = sessionKey;
    applying = true;
    applyError = '';
    try {
      // The server rechecks approval, version, column, claim and evidence freshness.
      const result = (await api.applyAuditFindings(auditId, [finding.id], false)).data.find((item) => item.finding_id === finding.id);
      if (!current(requestedSession, requestId)) return;
      if (result?.outcome !== 'applied') {
        applyError = `${result?.outcome === 'needs_follow_up' ? 'Needs follow-up' : 'Skipped'}: ${result?.message || 'The server did not apply this move.'} The task was not moved.`;
        return;
      }
      if (result.task) {
        onTaskUpdated(result.task);
        onTaskLoaded(result.task);
      }
      onNotice('success', `${helpers.taskLabel(finding)}: ${result.message} Nothing was completed automatically.`);
      focus.announce(`${helpers.taskLabel(finding)} applied. ${result.message}`);
      applyFinding = null;
      previewFinding = null;
      applyOriginFinding = null;
      focus.restoreModalFocus();
    } catch (error) {
      if (current(requestedSession, requestId) && !unavailable(error)) applyError = helpers.friendlyError(error, 'The recommendation could not be applied. Refresh and try again.');
    } finally {
      if (requestId === request) applying = false;
    }
  }

  async function applyApprovedFinding(): Promise<void> {
    const finding = applyFinding;
    if (finding && serverRun) return applyOnServer(finding);
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

<AuditDialogs {helpers} {previewFinding} {applyFinding} {applyError} {applying} {serverRun} {serverPreview} {serverPreviewLoading}
  {closePreview} {cancelApply} {openApplyConfirmation} {applyApprovedFinding} />
