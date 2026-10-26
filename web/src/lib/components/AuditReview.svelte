<script context="module" lang="ts">
  import { auditStatusLabel } from './audit/presentation';
  export { auditStatusLabel, auditFindingCount } from './audit/presentation';
  export type { AuditNoticeKind, AuditNavigation, AuditTaskUpdate, AuditNotice } from './audit/presentation';
  import type { AuditNavigation, AuditNotice, AuditTaskUpdate } from './audit/presentation';
</script>

<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api } from '../api';
  import { createAuditFocus } from './audit/focus';
  import AuditDetailHeading from './audit/AuditDetailHeading.svelte';
  import AuditFindingGroups from './audit/AuditFindingGroups.svelte';
  import AuditDialogs from './audit/AuditDialogs.svelte';
  import AuditRunList from './audit/AuditRunList.svelte';
  import { createAuditHelpers, verdicts, verdictLabels } from './audit/helpers';
  import {
    ApiError,
    type AuditDetail,
    type AuditFinding,
    type AuditFindingPatch,
    type AuditReviewState,
    type AuditRun,
    type Column,
    type Project,
    type SemanticState,
    type Task
  } from '../types';

  export let project: Project;
  export let columns: Column[] = [];
  export let tasks: Task[] = [];
  export let initialAuditId = '';
  export let sessionGeneration = 0;
  /** Parent increments this on its liveness cadence to recalculate drift. */
  export let refreshToken = 0;
  export let onNavigate: AuditNavigation = () => undefined;
  export let onNotice: AuditNotice = () => undefined;
  export let onTaskUpdated: AuditTaskUpdate = () => undefined;

  const auditFocus = createAuditFocus();

  let audits: AuditRun[] = [];
  let selectedAudit: AuditDetail | null = null;
  let selectedAuditId = '';
  let listLoading = false;
  let detailLoading = false;
  let listError = '';
  let detailError = '';
  let creating = false;
  let activeMutation = '';
  let listRequest = 0;
  let detailRequest = 0;
  let mutationRequest = 0;
  let loadKey = '';
  let editingFindingId = '';
  let destinationDraft: SemanticState | '' = '';
  let previewFinding: AuditFinding | null = null;
  let applyFinding: AuditFinding | null = null;
  let applyOriginFinding: AuditFinding | null = null;
  let applyError = '';
  let applying = false;

  let helpers = createAuditHelpers({ currentProjectId: project?.id || '', selectedAuditId, selectedAudit, tasks, columns });
  $: helpers = createAuditHelpers({ currentProjectId, selectedAuditId, selectedAudit, tasks, columns });

  $: currentProjectId = project?.id || '';
  $: routeKey = `${currentProjectId}:${initialAuditId || ''}:${sessionGeneration}:${refreshToken}`;
  $: if (currentProjectId && routeKey !== loadKey) {
    loadKey = routeKey;
    void loadAuditList(initialAuditId);
  }
  $: findings = selectedAudit?.findings || [];
  $: groupedFindings = verdicts.map((verdict) => ({
    verdict,
    label: verdictLabels[verdict],
    items: findings.filter((finding) => finding.verdict === verdict)
  }));
  $: approvedCount = findings.filter((finding) => finding.review_state === 'approved').length;
  $: pendingCount = findings.filter((finding) => finding.review_state === 'pending').length;
  $: changedCount = findings.filter((finding) => helpers.changedSinceAudit(finding)).length;

  function isCurrent(requestedSession: number, requestId: number, kind: 'list' | 'detail' | 'mutation'): boolean {
    if (!project?.id || project.id !== currentProjectId || sessionGeneration !== requestedSession) return false;
    if (kind === 'list') return requestId === listRequest;
    if (kind === 'detail') return requestId === detailRequest;
    return requestId === mutationRequest;
  }

  async function loadAuditList(preferredId = ''): Promise<void> {
    const requestId = ++listRequest;
    const requestedSession = sessionGeneration;
    listLoading = true;
    listError = '';
    try {
      const result = await api.listAllAudits(project.id);
      if (!isCurrent(requestedSession, requestId, 'list')) return;
      audits = result.data.map(helpers.normalizeAudit).filter((audit) => audit.id);
      const targetId = preferredId || selectedAuditId;
      if (targetId) {
        const target = audits.find((audit) => audit.id === targetId);
        if (target) await loadAuditDetail(target.id);
        else if (targetId === selectedAuditId) {
          selectedAudit = null;
          selectedAuditId = '';
          onNavigate(`/p/${encodeURIComponent(project.slug)}/audits`);
        }
      }
    } catch (error) {
      if (isCurrent(requestedSession, requestId, 'list')) listError = helpers.friendlyError(error, 'Audit runs could not be loaded.');
    } finally {
      if (requestId === listRequest) listLoading = false;
    }
  }

  async function loadAuditDetail(auditId: string): Promise<void> {
    if (!auditId) return;
    const requestId = ++detailRequest;
    const requestedSession = sessionGeneration;
    selectedAuditId = auditId;
    detailLoading = true;
    detailError = '';
    try {
      const result = await api.getAudit(auditId);
      if (!isCurrent(requestedSession, requestId, 'detail')) return;
      const detail = helpers.normalizeDetail(result);
      selectedAudit = detail;
      audits = audits.map((audit) => audit.id === detail.id ? { ...audit, ...detail, findings: detail.findings } : audit);
    } catch (error) {
      if (isCurrent(requestedSession, requestId, 'detail')) detailError = helpers.friendlyError(error, 'This audit could not be loaded.');
    } finally {
      if (requestId === detailRequest) detailLoading = false;
    }
  }

  async function refresh(): Promise<void> {
    if (selectedAuditId) {
      const requestedId = selectedAuditId;
      await Promise.all([loadAuditList(requestedId), loadAuditDetail(requestedId)]);
    } else {
      await loadAuditList();
    }
  }

  async function createAudit(): Promise<void> {
    if (!project?.id || creating) return;
    creating = true;
    listError = '';
    try {
      // `scope` is required by the current audit table. The backend may add
      // more scope choices later; this UI intentionally starts a board audit.
      const result = helpers.normalizeAudit(await api.createAudit(project.id, { scope: 'board', status: 'queued' }));
      audits = [result, ...audits.filter((audit) => audit.id !== result.id)];
      onNotice('success', `Audit ${auditStatusLabel(result.status).toLowerCase()} — recommendations are review-only until you apply one.`);
      auditFocus.announce(`Audit ${auditStatusLabel(result.status)}. No task moves were applied.`);
      if (result.id) {
        selectedAuditId = result.id;
        onNavigate(`/p/${encodeURIComponent(project.slug)}/audits/${encodeURIComponent(result.id)}`);
        await loadAuditDetail(result.id);
      }
    } catch (error) {
      listError = helpers.friendlyError(error, 'A new audit could not be started.');
    } finally {
      creating = false;
    }
  }

  function openAudit(audit: AuditRun): void {
    if (!audit.id) return;
    selectedAuditId = audit.id;
    onNavigate(`/p/${encodeURIComponent(project.slug)}/audits/${encodeURIComponent(audit.id)}`);
    void loadAuditDetail(audit.id);
  }

  function showList(): void {
    detailRequest += 1;
    selectedAudit = null;
    selectedAuditId = '';
    detailError = '';
    onNavigate(`/p/${encodeURIComponent(project.slug)}/audits`);
  }

  function editDestination(finding: AuditFinding): void {
    editingFindingId = finding.id;
    destinationDraft = helpers.destinationState(finding) || '';
    void tick().then(() => document.querySelector<HTMLSelectElement>(`[data-audit-destination="${CSS.escape(finding.id)}"]`)?.focus());
  }

  function cancelDestination(): void {
    editingFindingId = '';
    destinationDraft = '';
  }

  async function saveDestination(finding: AuditFinding): Promise<void> {
    const proposed = destinationDraft || null;
    await updateFinding(finding, {
      review_state: (finding.review_state as AuditReviewState) || 'pending',
      proposed_semantic_destination: proposed
    }, 'destination updated');
    if (!activeMutation) cancelDestination();
  }

  async function approveFinding(finding: AuditFinding): Promise<void> {
    if (helpers.changedSinceAudit(finding)) {
      detailError = 'This finding changed since the audit and cannot be approved. Refresh the board and review the current task.';
      return;
    }
    await updateFinding(finding, { review_state: 'approved' }, 'approved');
  }

  async function dismissFinding(finding: AuditFinding): Promise<void> {
    await updateFinding(finding, { review_state: 'dismissed' }, 'dismissed');
  }

  async function updateFinding(finding: AuditFinding, patch: AuditFindingPatch, actionLabel: string): Promise<void> {
    if (!selectedAudit || !finding.id || activeMutation) return;
    if (!helpers.auditReadyForReview()) {
      detailError = 'The agent must finish this audit before findings can be reviewed.';
      return;
    }
    const requestId = ++mutationRequest;
    const requestedSession = sessionGeneration;
    activeMutation = finding.id;
    detailError = '';
    try {
      const result = await api.patchAuditFinding(finding.id, patch, helpers.mutationVersion(finding));
      if (!isCurrent(requestedSession, requestId, 'mutation')) return;
      const updated = helpers.normalizeFinding(result);
      selectedAudit = {
        ...selectedAudit,
        findings: selectedAudit.findings.map((item) => item.id === updated.id ? updated : item)
      };
      audits = audits.map((audit) => audit.id === selectedAudit?.id
        ? { ...audit, findings: selectedAudit?.findings }
        : audit);
      onNotice('success', `${helpers.taskLabel(updated)} ${actionLabel}.`);
      auditFocus.announce(`${helpers.taskLabel(updated)} ${actionLabel}.`);
    } catch (error) {
      if (isCurrent(requestedSession, requestId, 'mutation')) {
        detailError = helpers.friendlyError(error, 'The finding changed elsewhere. Refresh and try again.');
        if (error instanceof ApiError && error.details.current) {
          const current = helpers.normalizeFinding(error.details.current);
          selectedAudit = {
            ...selectedAudit,
            findings: selectedAudit.findings.map((item) => item.id === current.id ? current : item)
          };
          detailError = 'This finding changed elsewhere. The authoritative review state is shown below.';
        }
      }
    } finally {
      if (requestId === mutationRequest) activeMutation = '';
    }
  }

  function openPreview(finding: AuditFinding): void {
    if (finding.review_state !== 'approved' || !helpers.auditReadyForReview()) return;
    if (helpers.changedSinceAudit(finding)) {
      detailError = 'This finding changed since the audit and cannot be previewed or applied.';
      return;
    }
    auditFocus.rememberModalFocus();
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
    auditFocus.restoreModalFocus();
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
    const requestId = ++mutationRequest;
    const requestedSession = sessionGeneration;
    applying = true;
    applyError = '';
    try {
      const updated = await api.moveTask(task.id, {
        destination_column_id: destination.id,
        expected_source_column_id: helpers.columnIdentifier(finding.source_column),
        source: 'board_audit',
        reason: finding.reason
      }, task.version);
      if (!isCurrent(requestedSession, requestId, 'mutation')) return;
      onTaskUpdated(updated);
      onNotice('success', `${helpers.taskLabel(finding)} moved to ${destination.name}.`);
      auditFocus.announce(`${helpers.taskLabel(finding)} moved to ${destination.name}.`);
      applyFinding = null;
      previewFinding = null;
      applyOriginFinding = null;
      auditFocus.restoreModalFocus();
    } catch (error) {
      if (isCurrent(requestedSession, requestId, 'mutation')) {
        applyError = helpers.friendlyError(error, 'The recommendation could not be applied. Refresh and try again.');
        if (error instanceof ApiError && error.details.current) {
          onTaskUpdated(error.details.current as Task);
          applyError = 'The task changed before apply. It was not moved; review the current task and audit finding.';
        }
      }
    } finally {
      if (requestId === mutationRequest) applying = false;
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

  onDestroy(auditFocus.destroy);

</script>

<svelte:window on:keydown={handleKeydown} />

<section class="audit-review" aria-labelledby="audit-heading">
  {#if selectedAuditId && selectedAudit}
    <AuditDetailHeading {helpers} {selectedAudit} {selectedAuditId} {detailLoading} {listLoading}
      {detailError} {refresh} {showList} {loadAuditDetail} />
    {#if detailLoading && !findings.length}
      <div class="audit-loading" aria-label="Loading audit findings"><div></div><div></div><div></div></div>
    {:else}
      <section class="audit-summary" aria-label="Audit summary">
        <div><span class="eyebrow">{project.name} · {selectedAudit.scope || 'Board scope'}</span><h2>{findings.length} {findings.length === 1 ? 'finding' : 'findings'} to review</h2><p>Captured {helpers.formatDate(selectedAudit.started_at || selectedAudit.created_at)} · {helpers.ageLabel(selectedAudit)}{#if selectedAudit.finalized_at} · Finalized {helpers.formatDate(selectedAudit.finalized_at)}{/if}</p></div>
        <div class="audit-summary-metrics"><span><strong>{approvedCount}</strong> approved</span><span><strong>{pendingCount}</strong> pending</span>{#if changedCount}<span class="changed-metric"><strong>{changedCount}</strong> changed</span>{/if}</div>
      </section>
      <AuditFindingGroups {helpers} {groupedFindings} {columns} {editingFindingId} bind:destinationDraft
        {activeMutation} {saveDestination} {cancelDestination} {editDestination} {approveFinding}
        {dismissFinding} {openPreview} />
    {/if}
  {:else}
    <AuditRunList {helpers} {project} {audits} {listLoading} {creating} {listError}
      {refresh} {createAudit} {openAudit} />
  {/if}
</section>

<AuditDialogs {helpers} {previewFinding} {applyFinding} {applyError} {applying}
  {closePreview} {cancelApply} {openApplyConfirmation} {applyApprovedFinding} />

<div class="sr-only" data-audit-live-region aria-live="polite" aria-atomic="true"></div>
