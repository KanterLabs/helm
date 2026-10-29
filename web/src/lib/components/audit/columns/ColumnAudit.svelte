<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { taskDeepLink } from '../../../state';
  import type { AssessmentSummary, AuditDetail, Column, CorrectionResult, GithubAuditEvidence, Project, Task } from '../../../types';
  import ColumnAuditEvidence from './ColumnAuditEvidence.svelte';
  import ColumnAuditResults from './ColumnAuditResults.svelte';
  import ColumnAuditReview from './ColumnAuditReview.svelte';
  import { groupOf, isV2, rowState, type AuditGroup } from './model';
  import type { V2Finding } from './types';

  /**
   * A v2 column audit: results, evidence and the single review/apply flow.
   * Mounted per project and audit, so a change of either discards selections,
   * proofs and any pending request held in memory.
   */
  export let project: Project;
  export let audit: AuditDetail;
  export let columns: Column[] = [];
  export let tasks: Task[] = [];
  export let evidence: GithubAuditEvidence | null = null;
  export let evidenceError = '';
  export let onTaskUpdated: (task: Task) => void = () => undefined;
  export let onOpenTask: ((taskId: string) => void) | null = null;
  export let onReaudit: (taskIds: string[]) => void = () => undefined;
  export let onRefresh: () => void = () => undefined;
  export let onUnavailable: (message: string) => void = () => undefined;
  export let announce: (message: string) => void = () => undefined;

  const projectId = project.id;
  const auditId = audit.id;
  let destroyed = false;
  let selected = new Set<string>();
  let receipts = new Map<string, CorrectionResult>();
  let mode: 'results' | 'review' = 'results';
  let evidenceFor: V2Finding | null = null;
  let opener: HTMLElement | null = null;
  let pushedHistory = false;
  let review: ColumnAuditReview;
  let tab: AuditGroup | '' = '';

  $: findings = audit.findings.filter(isV2);
  $: byId = new Map(findings.map((finding) => [finding.id, finding]));
  $: groups = {
    corrections: findings.filter((finding) => groupOf(finding) === 'corrections'),
    correct: findings.filter((finding) => groupOf(finding) === 'correct'),
    unverified: findings.filter((finding) => groupOf(finding) === 'unverified')
  } as Record<AuditGroup, V2Finding[]>;
  $: summary = audit.assessment_summary || fallbackSummary(groups);
  $: if (!tab) tab = summary.corrections ? 'corrections' : summary.unable_to_verify ? 'unverified' : 'correct';
  $: baselines = findings[0]?.assessment.baselines || [];
  $: captured = new Map((evidence?.tasks || []).map((task) => [task.id, task]));
  $: board = new Map(tasks.map((task) => [task.id, task]));
  // Reactive so rows re-render once captured titles (including off-page cards) arrive.
  $: titleFor = (finding: V2Finding) => captured.get(finding.task_id)?.title || board.get(finding.task_id)?.title || finding.assessment.task_key;
  $: hrefFor = (finding: V2Finding) => (project.slug ? taskDeepLink(project.slug, finding.assessment.task_key) : '');
  // Selection only holds rows that can still be applied.
  $: if ([...selected].some((id) => !byId.has(id) || rowState(byId.get(id)!, receipts.get(id)) !== 'selectable')) {
    selected = new Set([...selected].filter((id) => byId.has(id) && rowState(byId.get(id)!, receipts.get(id)) === 'selectable'));
  }

  function fallbackSummary(all: Record<AuditGroup, V2Finding[]>): AssessmentSummary {
    const checked = all.corrections.length + all.correct.length + all.unverified.length;
    return { checked, corrections: all.corrections.length, correct: all.correct.length, unable_to_verify: all.unverified.length };
  }

  const isCurrent = () => !destroyed && project.id === projectId && audit.id === auditId;


  function toggle(finding: V2Finding, checked: boolean): void {
    const next = new Set(selected);
    if (checked && rowState(finding, receipts.get(finding.id)) === 'selectable') next.add(finding.id);
    else next.delete(finding.id);
    selected = next;
    announce(`${next.size} ${next.size === 1 ? 'correction' : 'corrections'} selected`);
  }

  function openTask(finding: V2Finding, event: MouseEvent): void {
    if (!onOpenTask || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    closeEvidence();
    onOpenTask(finding.task_id);
  }

  function openEvidence(finding: V2Finding, from: HTMLElement): void {
    opener = from;
    evidenceFor = finding;
    if (!pushedHistory) {
      // Browser Back closes the panel and stays on this audit.
      history.pushState({ ...(history.state || {}), columnAuditEvidence: finding.id }, '', location.href);
      pushedHistory = true;
    }
  }

  function closeEvidence(fromHistory = false): void {
    if (!evidenceFor) return;
    evidenceFor = null;
    if (pushedHistory && !fromHistory) history.back();
    pushedHistory = false;
    const target = opener;
    opener = null;
    void tick().then(() => { if (target?.isConnected) target.focus(); });
  }

  function popstate(): void {
    if (evidenceFor) closeEvidence(true);
  }

  function startReview(): void {
    if (!selected.size) return;
    mode = 'review';
    review?.start([...selected]);
  }

  function backToResults(): void {
    mode = 'results';
    void tick().then(() => document.getElementById('ca-results-heading')?.focus());
  }

  function receiptsArrived(results: CorrectionResult[]): void {
    const next = new Map(receipts);
    for (const result of results) {
      if (result.outcome_known && (result.outcome === 'applied' || result.outcome === 'already_applied')) next.set(result.finding_id, result);
    }
    receipts = next;
    selected = new Set([...selected].filter((id) => !next.has(id)));
    // Re-read findings so changed cards show as stale; receipts stay in memory.
    onRefresh();
  }

  function reaudit(finding: V2Finding): void {
    onReaudit([finding.task_id]);
  }

  onDestroy(() => {
    destroyed = true;
    selected = new Set();
  });
</script>

<svelte:window on:popstate={popstate} />

<div class="ca-root" class:reviewing={mode === 'review'}>
  <div hidden={mode !== 'results'}>
    <ColumnAuditResults {summary} {baselines} {groups} tab={tab || 'corrections'} {columns} {receipts} {selected} {titleFor} {hrefFor}
      onTab={(next) => { tab = next; }} onToggle={toggle} onEvidence={openEvidence} onOpenTask={openTask} onReaudit={reaudit} onReview={startReview} />
  </div>
  <div hidden={mode !== 'review'}>
    <ColumnAuditReview bind:this={review} {auditId} {columns} findings={byId} taskTitle={titleFor} taskHref={hrefFor} {isCurrent}
      onReceipts={receiptsArrived} {onTaskUpdated} {onUnavailable} onOpenTask={openTask} onBack={backToResults} {announce} />
  </div>
</div>

{#if evidenceFor}
  <ColumnAuditEvidence finding={evidenceFor} {columns} {evidence} {evidenceError} title={titleFor(evidenceFor)}
    selectable={rowState(evidenceFor, receipts.get(evidenceFor.id)) === 'selectable'} selected={selected.has(evidenceFor.id)}
    onToggle={(checked) => evidenceFor && toggle(evidenceFor, checked)} onClose={() => closeEvidence()} />
{/if}
