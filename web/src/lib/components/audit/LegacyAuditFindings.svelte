<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../api';
  import AuditApplyFlow from './AuditApplyFlow.svelte';
  import AuditFindingGroups from './AuditFindingGroups.svelte';
  import type { createAuditFocus } from './focus';
  import { verdicts, verdictLabels, type AuditHelpers } from './helpers';
  import type { AuditNotice, AuditTaskUpdate } from './presentation';
  import {
    ApiError,
    type AuditDetail,
    type AuditFinding,
    type AuditFindingPatch,
    type AuditReviewState,
    type Column,
    type GithubEvidenceItem,
    type Project,
    type SemanticState,
    type Task
  } from '../../types';

  /**
   * Legacy (v1 and external-agent) finding review: approve, dismiss, edit the
   * destination, then preview/apply one approved finding. Column audits with v2
   * assessments use ColumnAudit instead; this flow is preserved unchanged.
   */
  export let project: Project;
  export let audit: AuditDetail;
  export let columns: Column[] = [];
  export let helpers: AuditHelpers;
  export let serverRun = false;
  export let detailLoading = false;
  export let sessionGeneration = 0;
  export let evidenceItems: Map<string, GithubEvidenceItem> = new Map();
  export let focus: ReturnType<typeof createAuditFocus>;
  export let onOpenTask: ((finding: AuditFinding) => void) | null = null;
  export let onNotice: AuditNotice = () => undefined;
  export let onTaskUpdated: AuditTaskUpdate = () => undefined;
  export let onAuditUpdated: (audit: AuditDetail) => void = () => undefined;
  export let onError: (message: string) => void = () => undefined;
  export let onUnavailable: (message: string) => void = () => undefined;
  export let onTaskLoaded: (task: Task) => void = () => undefined;

  let applyFlow: AuditApplyFlow;
  let activeMutation = '';
  let mutationRequest = 0;
  let editingFindingId = '';
  let destinationDraft: SemanticState | '' = '';

  $: findings = audit.findings || [];
  $: groupedFindings = verdicts.map((verdict) => ({
    verdict,
    label: verdictLabels[verdict],
    items: findings.filter((finding) => finding.verdict === verdict)
  }));
  $: approvedCount = findings.filter((finding) => finding.review_state === 'approved').length;
  $: pendingCount = findings.filter((finding) => finding.review_state === 'pending').length;
  $: changedCount = findings.filter((finding) => helpers.changedSinceAudit(finding)).length;

  function isCurrent(projectId: string, requestedSession: number, requestId: number): boolean {
    return project?.id === projectId && sessionGeneration === requestedSession && requestId === mutationRequest;
  }

  function replaceFinding(updated: AuditFinding): void {
    audit = { ...audit, findings: audit.findings.map((item) => item.id === updated.id ? updated : item) };
    onAuditUpdated(audit);
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
      onError('This finding changed since the audit and cannot be approved. Refresh the board and review the current task.');
      return;
    }
    await updateFinding(finding, { review_state: 'approved' }, 'approved');
  }

  async function dismissFinding(finding: AuditFinding): Promise<void> {
    await updateFinding(finding, { review_state: 'dismissed' }, 'dismissed');
  }

  async function updateFinding(finding: AuditFinding, patch: AuditFindingPatch, actionLabel: string): Promise<void> {
    if (!finding.id || activeMutation) return;
    if (!helpers.auditReadyForReview()) {
      onError('The agent must finish this audit before findings can be reviewed.');
      return;
    }
    const requestId = ++mutationRequest;
    const requestedSession = sessionGeneration;
    const projectId = project.id;
    activeMutation = finding.id;
    onError('');
    try {
      const result = await api.patchAuditFinding(finding.id, patch, helpers.mutationVersion(finding));
      if (!isCurrent(projectId, requestedSession, requestId)) return;
      const updated = helpers.normalizeFinding(result);
      replaceFinding(updated);
      onNotice('success', `${helpers.taskLabel(updated)} ${actionLabel}.`);
      focus.announce(`${helpers.taskLabel(updated)} ${actionLabel}.`);
    } catch (error) {
      if (!isCurrent(projectId, requestedSession, requestId)) return;
      if (error instanceof ApiError && (error.code === 'evidence_unavailable' || [401, 403, 404].includes(error.status))) {
        onUnavailable(helpers.friendlyError(error, 'Source access is unavailable.'));
        return;
      }
      onError(helpers.friendlyError(error, 'The finding changed elsewhere. Refresh and try again.'));
      if (error instanceof ApiError && error.details.current) {
        replaceFinding(helpers.normalizeFinding(error.details.current));
        onError('This finding changed elsewhere. The authoritative review state is shown below.');
      }
    } finally {
      if (requestId === mutationRequest) activeMutation = '';
    }
  }
</script>

{#if detailLoading && !findings.length}
  <div class="audit-loading" aria-label="Loading audit findings"><div></div><div></div><div></div></div>
{:else}
  <section class="audit-summary" aria-label="Audit summary">
    <div><span class="eyebrow">{project.name} · {audit.scope || 'Board scope'}</span><h2>{findings.length} {findings.length === 1 ? 'finding' : 'findings'} to review</h2><p>Captured {helpers.formatDate(audit.started_at || audit.created_at)} · {helpers.ageLabel(audit)}{#if audit.finalized_at} · Finalized {helpers.formatDate(audit.finalized_at)}{/if}</p></div>
    <div class="audit-summary-metrics"><span><strong>{approvedCount}</strong> approved</span><span><strong>{pendingCount}</strong> pending</span>{#if changedCount}<span class="changed-metric"><strong>{changedCount}</strong> changed</span>{/if}</div>
  </section>
  <AuditFindingGroups {helpers} {groupedFindings} {columns} {editingFindingId} bind:destinationDraft
    {activeMutation} {saveDestination} {cancelDestination} {editDestination} {approveFinding}
    {dismissFinding} openPreview={(finding) => applyFlow?.openPreview(finding)} projectSlug={project.slug} {evidenceItems} {onOpenTask} />
{/if}

{#key `${project.id}:${sessionGeneration}:${audit.id}`}
<AuditApplyFlow bind:this={applyFlow} {helpers} {columns} auditId={audit.id} {serverRun} {focus}
  sessionKey={`${project.id}:${sessionGeneration}`} {onTaskUpdated} {onNotice}
  onBlocked={onError} {onUnavailable} {onTaskLoaded} />
{/key}
