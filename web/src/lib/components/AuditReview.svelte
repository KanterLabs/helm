<script context="module" lang="ts">
  import { auditStatusLabel } from './audit/presentation';
  export { auditStatusLabel, auditFindingCount } from './audit/presentation';
  export type { AuditNoticeKind, AuditNavigation, AuditTaskUpdate, AuditNotice } from './audit/presentation';
  import type { AuditNavigation, AuditNotice, AuditTaskUpdate } from './audit/presentation';
</script>

<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../api';
  import { createAuditFocus } from './audit/focus';
  import AuditDetailHeading from './audit/AuditDetailHeading.svelte';
  import LegacyAuditFindings from './audit/LegacyAuditFindings.svelte';
  import ColumnAudit from './audit/columns/ColumnAudit.svelte';
  import { isV2 } from './audit/columns/model';
  import { requestScope } from './audit/columns/scopeIntent';
  import AuditRunList from './audit/AuditRunList.svelte';
  import AuditEvidenceManifest from './audit/AuditEvidenceManifest.svelte';
  import GithubAuditJobPanel from './audit/GithubAuditJob.svelte';
  import { evidenceIndex, jobActive, jobEndedWithoutResults, loadMissingTasks } from './audit/githubAudit';
  import { createAuditHelpers } from './audit/helpers';
  import {
    ApiError,
    type AuditDetail,
    type AuditFinding,
    type AuditRun,
    type Column,
    type GithubAuditEvidence,
    type GithubAuditJob,
    type Project,
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
  /** Opens any card by ID over this audit, including cards beyond the board's first page. */
  export let onOpenTaskId: ((taskId: string) => void) | null = null;

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
  let evidenceError = '';
  let accessChecking = false;
  let extraTasks: Task[] = [];
  let detailFor = '';
  let evidenceRevision = 0;
  let listRequest = 0;
  let detailRequest = 0;
  let loadKey = '';
  let protectedContext = `${project.id}:${sessionGeneration}`;

  let helpers = createAuditHelpers({ currentProjectId: project?.id || '', selectedAuditId, selectedAudit, tasks, columns });
  $: captured = new Map((evidence?.tasks || []).map((item) => [item.id, item]));
  $: evidenceItems = evidenceIndex(evidence);
  $: helpers = createAuditHelpers({ currentProjectId, selectedAuditId, selectedAudit, tasks: extraTasks.length ? [...tasks, ...extraTasks] : tasks, columns, captured });
  // Evidence is read once a run has published; an active capture is not lost access.
  $: evidenceReady = serverRun && Boolean(job && job.stage === 'finished' && !jobEndedWithoutResults(job));

  $: currentProjectId = project?.id || '';
  $: if (`${currentProjectId}:${sessionGeneration}` !== protectedContext) {
    protectedContext = `${currentProjectId}:${sessionGeneration}`;
    resetProtectedContext();
  }
  $: routeKey = `${currentProjectId}:${initialAuditId || ''}:${sessionGeneration}:${refreshToken}`;
  $: if (currentProjectId && routeKey !== loadKey) {
    loadKey = routeKey;
    void loadAuditList(initialAuditId);
  }
  $: findings = selectedAudit?.findings || [];
  // v2 assessments use the column audit flow; legacy rows keep their original flow.
  $: columnAudit = findings.some(isV2);
  $: runWithoutResults = serverRun && !findings.length && (jobActive(job) || jobEndedWithoutResults(job));

  function isCurrent(requestedSession: number, requestId: number, kind: 'list' | 'detail'): boolean {
    if (!project?.id || project.id !== currentProjectId || sessionGeneration !== requestedSession) return false;
    return requestId === (kind === 'list' ? listRequest : detailRequest);
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
    // Unmounting the review discards in-memory selections, proofs and requests.
    selectedAudit = null;
    evidence = null;
    extraTasks = [];
    job = null;
    serverRun = false;
    audits = audits.map((audit) => ({ ...audit, findings: undefined }));
    detailError = message;
  }

  function resetProtectedContext(): void {
    // End the old review synchronously, before the new context's reads finish.
    listRequest += 1;
    detailRequest += 1;
    selectedAudit = null;
    evidence = null;
    evidenceError = '';
    extraTasks = [];
    audits = [];
    job = null;
    serverRun = false;
    detailFor = '';
    accessChecking = false;
  }

  function onJob(next: GithubAuditJob | null): void {
    job = next;
    serverRun = Boolean(next);
    // External-agent runs have no manifest: look up off-page task identities.
    if (!next) void loadOffPageTasks();
  }

  async function loadOffPageTasks(): Promise<void> {
    const auditId = selectedAuditId;
    const requestedSession = sessionGeneration;
    const projectId = currentProjectId;
    const missing = findings.filter((finding) => finding.task_id && !helpers.findingTask(finding)).map((finding) => finding.task_id);
    if (!missing.length) return;
    const found = await loadMissingTasks(missing, (id) => api.getTask(id));
    if (auditId === selectedAuditId && requestedSession === sessionGeneration && projectId === currentProjectId) extraTasks = [...extraTasks.filter((task) => !found.some((item) => item.id === task.id)), ...found];
  }

  function showAudit(auditId: string): void {
    selectedAuditId = auditId;
    onNavigate(`/p/${encodeURIComponent(project.slug)}/audits/${encodeURIComponent(auditId)}`);
    void loadAuditList(auditId);
  }

  function onStarted(auditId: string): void {
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

  /** Re-audit: the scope opens with these cards selected. */
  function reaudit(taskIds: string[]): void {
    requestScope(currentProjectId, taskIds);
    showList();
  }

  /** Restored repository access is only trusted after GitHub is checked again. */
  async function checkAccess(): Promise<void> {
    accessChecking = true;
    try {
      await api.checkGithubIntegration();
      await loadAuditDetail(selectedAuditId);
    } catch (error) {
      detailError = helpers.friendlyError(error, 'GitHub access could not be checked.');
    } finally {
      accessChecking = false;
    }
  }

  function rememberTask(task: Task): void {
    extraTasks = [...extraTasks.filter((item) => item.id !== task.id), task];
  }

  onDestroy(auditFocus.destroy);

</script>

<section class="audit-review" aria-labelledby="audit-heading">
  {#if selectedAuditId && selectedAudit}
    <AuditDetailHeading {helpers} {selectedAudit} {selectedAuditId} {detailLoading} {listLoading}
      {detailError} {refresh} {showList} {loadAuditDetail} {serverRun} title={columnAudit || runWithoutResults ? 'Column audit' : 'Board audit'}
      statusLabel={columnAudit ? 'Finished' : ''} />
    <GithubAuditJobPanel auditId={selectedAuditId} compact={columnAudit} {onJob} onFinished={() => void refresh()} onRetried={showAudit}>
      {#if columnAudit}<AuditEvidenceManifest auditId={selectedAuditId} enabled={evidenceReady} reloadKey={`${evidenceRevision}:${job?.stage === 'finished' ? 'final' : 'capture'}`} onEvidence={(next, error) => { evidence = next; evidenceError = error; }} onUnavailable={clearProtectedContent} />{/if}
    </GithubAuditJobPanel>
    {#if !columnAudit}<AuditEvidenceManifest auditId={selectedAuditId} enabled={evidenceReady} reloadKey={`${evidenceRevision}:${job?.stage === 'finished' ? 'final' : 'capture'}`} onEvidence={(next, error) => { evidence = next; evidenceError = error; }} onUnavailable={clearProtectedContent} />{/if}
    {#if columnAudit}
      {#key `${currentProjectId}:${sessionGeneration}:${selectedAuditId}`}
        <ColumnAudit {project} audit={selectedAudit} {columns} tasks={extraTasks.length ? [...tasks, ...extraTasks] : tasks} {evidence} {evidenceError}
          {onTaskUpdated} onOpenTask={onOpenTaskId} onReaudit={reaudit} onRefresh={() => void loadAuditDetail(selectedAuditId)}
          onUnavailable={clearProtectedContent} announce={auditFocus.announce} />
      {/key}
    {:else if !runWithoutResults}
      <LegacyAuditFindings {project} audit={selectedAudit} {columns} {helpers} {serverRun} {detailLoading} {sessionGeneration}
        {evidenceItems} focus={auditFocus} {onOpenTask} {onNotice} {onTaskUpdated} onAuditUpdated={(next) => { selectedAudit = next; audits = audits.map((audit) => audit.id === next.id ? { ...audit, findings: next.findings } : audit); }}
        onError={(message) => { detailError = message; }} onUnavailable={clearProtectedContent} onTaskLoaded={rememberTask} />
    {/if}
  {:else if selectedAuditId && detailError}
    <section class="page-heading audit-heading">
      <div><h1 id="audit-heading">Board audit</h1><p>Restore access before reviewing this audit.</p></div>
      <button class="button quiet-button" type="button" on:click={showList}>All audits</button>
    </section>
    <div class="inline-alert error content-alert" role="alert"><span>{detailError}</span><button class="text-button" type="button" disabled={accessChecking} on:click={() => void checkAccess()}>Check GitHub access</button><button class="text-button" type="button" on:click={() => loadAuditDetail(selectedAuditId)}>Retry</button></div>
  {:else}
    <AuditRunList {helpers} {project} {columns} {audits} {listLoading} {listError}
      {refresh} {openAudit} {onStarted} onOpenExisting={showAudit} />
  {/if}
</section>

<div class="sr-only" data-audit-live-region aria-live="polite" aria-atomic="true"></div>
