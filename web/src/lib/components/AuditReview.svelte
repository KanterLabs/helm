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
  import AuditApplyFlow from './audit/AuditApplyFlow.svelte';
  import AuditRunList from './audit/AuditRunList.svelte';
  import AuditEvidenceManifest from './audit/AuditEvidenceManifest.svelte';
  import GithubAuditJobPanel from './audit/GithubAuditJob.svelte';
  import { evidenceIndex, loadMissingTasks } from './audit/githubAudit';
  import { createAuditHelpers, verdicts, verdictLabels } from './audit/helpers';
  import { auditReviewActionsAvailable as actionsAvailable } from './audit/capability';
  import {
    ApiError,
    type AuditDetail,
    type AuditFinding,
    type AuditFindingPatch,
    type AuditReviewState,
    type AuditRun,
    type Column,
    type GithubAuditEvidence,
    type GithubAuditJob,
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
  /** Optional in-app opener for a finding's task; links work without it. */
  export let onOpenTask: ((finding: AuditFinding) => void) | null = null;

  const auditFocus = createAuditFocus();

  let audits: AuditRun[] = [];
  let selectedAudit: AuditDetail | null = null;
  let selectedAuditId = '';
  let listLoading = false;
  let detailLoading = false;
  let listError = '';
  let detailError = '';
  // Server-run GitHub audits: job, immutable evidence and guarded apply preview.
  let job: GithubAuditJob | null = null;
  let serverRun = false;
  let evidence: GithubAuditEvidence | null = null;
  let extraTasks: Task[] = [];
  let detailFor = '';
  let evidenceRevision = 0;
  let applyFlow: AuditApplyFlow;
  let activeMutation = '';
  let listRequest = 0;
  let detailRequest = 0;
  let mutationRequest = 0;
  let loadKey = '';
  let editingFindingId = '';
  let destinationDraft: SemanticState | '' = '';

  let helpers = createAuditHelpers({ currentProjectId: project?.id || '', selectedAuditId, selectedAudit, tasks, columns });
  $: captured = new Map((evidence?.tasks || []).map((item) => [item.id, item]));
  $: evidenceItems = evidenceIndex(evidence);
  $: helpers = createAuditHelpers({ currentProjectId, selectedAuditId, selectedAudit, tasks: extraTasks.length ? [...tasks, ...extraTasks] : tasks, columns, captured });
  $: evidenceReady = serverRun && Boolean(job && ['analyzing', 'validating', 'finished'].includes(job.stage));

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
      if (isCurrent(requestedSession, requestId, 'list')) {
        listError = helpers.friendlyError(error, 'Audit runs could not be loaded.');
        if (selectedAuditId && error instanceof ApiError && [401, 403, 404].includes(error.status)) clearProtectedContent(listError);
      }
    } finally {
      if (requestId === listRequest) listLoading = false;
    }
  }

  async function loadAuditDetail(auditId: string): Promise<void> {
    if (!auditId) return;
    const requestId = ++detailRequest;
    const requestedSession = sessionGeneration;
    if (auditId !== detailFor) {
      detailFor = auditId;
      job = null;
      serverRun = false;
      evidence = null;
      extraTasks = [];
    }
    selectedAuditId = auditId;
    detailLoading = true;
    detailError = '';
    try {
      const result = await api.getAudit(auditId);
      if (!isCurrent(requestedSession, requestId, 'detail')) return;
      const detail = helpers.normalizeDetail(result);
      selectedAudit = detail;
      evidenceRevision += 1;
      audits = audits.map((audit) => audit.id === detail.id ? { ...audit, ...detail, findings: detail.findings } : audit);
    } catch (error) {
      if (isCurrent(requestedSession, requestId, 'detail')) clearProtectedContent(helpers.friendlyError(error, 'This audit could not be loaded.'));
    } finally {
      if (requestId === detailRequest) detailLoading = false;
    }
  }

  async function refresh(): Promise<void> {
    await loadAuditList(selectedAuditId);
  }

  function clearProtectedContent(message: string): void {
    mutationRequest += 1;
    activeMutation = '';
    selectedAudit = null;
    evidence = null;
    extraTasks = [];
    job = null;
    serverRun = false;
    audits = audits.map((audit) => ({ ...audit, findings: undefined }));
    detailError = message;
  }

  function onJob(next: GithubAuditJob | null): void {
    job = next;
    serverRun = Boolean(next);
    // External-agent runs have no manifest: look up off-page task identities.
    if (!next) void loadOffPageTasks();
  }

  async function loadOffPageTasks(): Promise<void> {
    const auditId = selectedAuditId;
    const missing = findings.filter((finding) => finding.task_id && !helpers.findingTask(finding)).map((finding) => finding.task_id);
    if (!missing.length) return;
    const found = await loadMissingTasks(missing, (id) => api.getTask(id));
    if (auditId === selectedAuditId) extraTasks = [...extraTasks.filter((task) => !found.some((item) => item.id === task.id)), ...found];
  }

  function showAudit(auditId: string): void {
    selectedAuditId = auditId;
    onNavigate(`/p/${encodeURIComponent(project.slug)}/audits/${encodeURIComponent(auditId)}`);
    void loadAuditList(auditId);
  }

  function onStarted(auditId: string): void {
    if (!actionsAvailable) return;
    onNotice('success', 'Audit started on the server. You can leave this page; nothing moves until you apply it.');
    auditFocus.announce('Audit started. No task moves were applied.');
    showAudit(auditId);
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
    if (!actionsAvailable || !selectedAudit || !finding.id || activeMutation) return;
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
        if (error instanceof ApiError && (error.code === 'evidence_unavailable' || [401, 403, 404].includes(error.status))) {
          clearProtectedContent(helpers.friendlyError(error, 'Source access is unavailable.'));
          return;
        }
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
    if (actionsAvailable) applyFlow?.openPreview(finding);
  }

  function rememberTask(task: Task): void {
    extraTasks = [...extraTasks.filter((item) => item.id !== task.id), task];
  }

  onDestroy(auditFocus.destroy);

</script>

<section class="audit-review" aria-labelledby="audit-heading">
  {#if selectedAuditId && selectedAudit}
    <AuditDetailHeading {helpers} {selectedAudit} {selectedAuditId} {detailLoading} {listLoading}
      {detailError} {refresh} {showList} {loadAuditDetail} {serverRun} {actionsAvailable} />
    <GithubAuditJobPanel auditId={selectedAuditId} {actionsAvailable} {onJob} onFinished={() => void refresh()} onRetried={showAudit} />
    <AuditEvidenceManifest auditId={selectedAuditId} enabled={evidenceReady} reloadKey={`${evidenceRevision}:${job?.stage === 'finished' ? 'final' : 'capture'}`} onEvidence={(next) => { evidence = next; }} onUnavailable={clearProtectedContent} />
    {#if detailLoading && !findings.length}
      <div class="audit-loading" aria-label="Loading audit findings"><div></div><div></div><div></div></div>
    {:else}
      <section class="audit-summary" aria-label="Audit summary">
        <div><span class="eyebrow">{project.name} · {selectedAudit.scope || 'Board scope'}</span><h2>{findings.length} {findings.length === 1 ? 'finding' : 'findings'} {actionsAvailable ? 'to review' : 'recorded'}</h2><p>Captured {helpers.formatDate(selectedAudit.started_at || selectedAudit.created_at)} · {helpers.ageLabel(selectedAudit)}{#if selectedAudit.finalized_at} · Finalized {helpers.formatDate(selectedAudit.finalized_at)}{/if}</p></div>
        <div class="audit-summary-metrics"><span><strong>{approvedCount}</strong> approved</span><span><strong>{pendingCount}</strong> pending</span>{#if changedCount}<span class="changed-metric"><strong>{changedCount}</strong> changed</span>{/if}</div>
      </section>
      <AuditFindingGroups {helpers} {groupedFindings} {columns} {editingFindingId} bind:destinationDraft
        {activeMutation} {saveDestination} {cancelDestination} {editDestination} {approveFinding}
        {dismissFinding} {openPreview} projectSlug={project.slug} {evidenceItems} {onOpenTask} {actionsAvailable} />
    {/if}
  {:else if selectedAuditId && detailError}
    <section class="page-heading audit-heading">
      <div><h1 id="audit-heading">Board audit</h1><p>Restore access before reviewing this audit.</p></div>
      <button class="button quiet-button" type="button" on:click={showList}>All audits</button>
    </section>
    <div class="inline-alert error content-alert" role="alert"><span>{detailError}</span><button class="text-button" type="button" on:click={() => loadAuditDetail(selectedAuditId)}>Retry</button></div>
  {:else}
    <AuditRunList {helpers} {project} {audits} {listLoading} {listError}
      {refresh} {openAudit} {onStarted} onOpenExisting={showAudit} {actionsAvailable} />
  {/if}
</section>

{#if selectedAudit && actionsAvailable}
{#key `${currentProjectId}:${sessionGeneration}:${selectedAuditId}`}
<AuditApplyFlow bind:this={applyFlow} {helpers} {columns} auditId={selectedAuditId} {serverRun} focus={auditFocus}
  sessionKey={`${currentProjectId}:${sessionGeneration}`} {onTaskUpdated} {onNotice}
  onBlocked={(message) => { detailError = message; }} onUnavailable={clearProtectedContent} onTaskLoaded={rememberTask} />
{/key}
{/if}

<div class="sr-only" data-audit-live-region aria-live="polite" aria-atomic="true"></div>
