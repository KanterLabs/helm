<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../../api';
  import { key } from '../../../api/client';
  import { ApiError, type Column, type CorrectionRequest, type CorrectionResult, type Task } from '../../../types';
  import {
    applyRequest, displayOutcome, inputProblem, previewRequest, receiptCounts, receiptDetail, receiptHeadline, recoveryHint, transportUnknown,
    type ItemInputs, type PendingApply
  } from './corrections';
  import { actionLabel, outcomeLabels, sourceName, targetName } from './model';
  import type { V2Finding } from './types';

  /**
   * Select → Review N corrections (read-only preview) → one Apply. The preview is
   * the confirmation screen. Proofs and the pending request exist only here.
   */
  export let auditId: string;
  export let columns: Column[] = [];
  export let findings: Map<string, V2Finding>;
  export let taskTitle: (finding: V2Finding) => string;
  export let taskHref: (finding: V2Finding) => string;
  /** False once the project or audit changed; late responses are discarded. */
  export let isCurrent: () => boolean = () => true;
  export let onReceipts: (results: CorrectionResult[]) => void = () => undefined;
  export let onTaskUpdated: (task: Task) => void = () => undefined;
  export let onUnavailable: (message: string) => void = () => undefined;
  export let onOpenTask: (finding: V2Finding, event: MouseEvent) => void = () => undefined;
  export let onBack: () => void = () => undefined;
  export let announce: (message: string) => void = () => undefined;

  let ids: string[] = [];
  let inputs: Record<string, ItemInputs> = {};
  let preview: { body: CorrectionRequest; results: CorrectionResult[] } | null = null;
  let dirty = false;
  let previewing = false;
  let previewError = '';
  let pending: PendingApply | null = null;
  let applying = false;
  let results: CorrectionResult[] | null = null;
  /** Key of the request that produced `results`; identifies this request's own receipts. */
  let sentKey = '';
  let unknown = false;
  let applyError = '';
  let refreshNote = '';
  let heading: HTMLElement;

  $: entries = ids.map((id) => findings.get(id)).filter((item): item is V2Finding => Boolean(item));
  $: byFinding = new Map((preview?.results || []).map((result) => [result.finding_id, result]));
  $: eligible = (preview?.results || []).filter((result) => result.outcome === 'eligible' && result.confirmation_token);
  $: receiptMode = Boolean(results) || unknown;
  $: counts = results ? receiptCounts(results, sentKey) : null;
  $: total = pending?.body.items.length || results?.length || 0;

  /** Open the review for these findings. An unknown outcome must be resolved first. */
  export function start(selected: string[]): void {
    if (!(unknown && pending)) {
      ids = selected;
      inputs = Object.fromEntries(selected.map((id) => [id, inputs[id] || {}]));
      results = null;
      sentKey = '';
      unknown = false;
      applyError = '';
      refreshNote = '';
      pending = null;
      void runPreview();
    }
    void tick().then(() => heading?.focus());
  }

  function unavailable(error: unknown): boolean {
    if (!(error instanceof ApiError) || !(error.code === 'evidence_unavailable' || [401, 403, 404].includes(error.status))) return false;
    clearProofs();
    onUnavailable(error.message);
    return true;
  }

  function clearProofs(): void {
    preview = null;
    pending = null;
  }

  async function runPreview(): Promise<void> {
    // Read the current IDs directly: start() runs before reactive values update.
    const chosen = ids.map((id) => findings.get(id)).filter((item): item is V2Finding => Boolean(item));
    const body = previewRequest(chosen.map((finding) => ({ id: finding.id, action: finding.assessment.lifecycle_action })), inputs);
    previewing = true;
    previewError = '';
    preview = null;
    try {
      const response = await api.correctAuditFindings(auditId, body);
      if (!isCurrent()) return;
      preview = { body, results: response.data };
      dirty = false;
      const ready = response.data.filter((result) => result.outcome === 'eligible').length;
      announce(`Review ready: ${ready} of ${body.items.length} corrections can be applied.`);
    } catch (error) {
      if (!isCurrent() || unavailable(error)) return;
      previewError = error instanceof Error ? error.message : 'The corrections could not be reviewed.';
    } finally {
      previewing = false;
    }
  }

  function setInput(id: string, patch: ItemInputs): void {
    inputs = { ...inputs, [id]: { ...inputs[id], ...patch } };
    // Inputs are part of the proof: any change needs a fresh read-only review.
    dirty = true;
  }

  async function apply(): Promise<void> {
    if (!preview || dirty || !eligible.length || applying || pending) return;
    pending = { body: applyRequest(preview.body, preview.results), key: key() };
    preview = null;
    await send();
  }

  async function send(): Promise<void> {
    if (!pending || applying) return;
    const request = pending;
    applying = true;
    applyError = '';
    try {
      const response = await api.correctAuditFindings(auditId, request.body, request.key);
      if (!isCurrent()) return;
      results = response.data;
      sentKey = request.key;
      unknown = response.data.some((result) => !result.outcome_known);
      // A known outcome releases the proofs; an unknown one keeps the exact request.
      if (!unknown) pending = null;
      const tally = receiptCounts(response.data, request.key);
      announce(`${receiptHeadline(tally, response.data.length)}. ${receiptDetail(tally)}`);
      onReceipts(response.data.map((result) => (displayOutcome(result, request.key) === 'applied' ? { ...result, outcome: 'applied' } : result)));
      void refreshTasks(response.data);
    } catch (error) {
      if (!isCurrent()) return;
      if (transportUnknown(error)) {
        unknown = true;
        announce('The outcome is unknown. Retry checks the same request.');
      } else if (!unavailable(error)) {
        pending = null;
        applyError = error instanceof Error ? error.message : 'The corrections were not applied.';
      }
    } finally {
      applying = false;
      void tick().then(() => heading?.focus());
    }
  }

  /** Receipts carry a lifecycle snapshot only; load the full card before updating the board. */
  async function refreshTasks(items: CorrectionResult[]): Promise<void> {
    const changed = items.filter((item) => (item.outcome === 'applied' || item.outcome === 'already_applied') && item.task_id);
    for (const item of changed) {
      try {
        const task = await api.getTask(item.task_id as string);
        if (isCurrent() && task.id === item.task_id) onTaskUpdated(task);
      } catch {
        if (isCurrent()) refreshNote = 'Applied results are confirmed. The board could not refresh; reload it to see the current cards.';
      }
    }
  }

  function resultFor(id: string): CorrectionResult | undefined {
    return results?.find((result) => result.finding_id === id);
  }

  const plural = (n: number) => `${n} ${n === 1 ? 'correction' : 'corrections'}`;
</script>

<section class="ca-review" data-correction-review={receiptMode ? undefined : ''} data-correction-receipt={receiptMode ? '' : undefined} aria-labelledby="ca-review-heading">
  {#if receiptMode}
    <div class="ca-review-head">
      <h2 id="ca-review-heading" tabindex="-1" bind:this={heading}>{counts && !unknown ? receiptHeadline(counts, results?.length || 0) : 'Outcome unknown'}</h2>
      {#if unknown}
        <p class="ca-warning" role="alert">We couldn't confirm the result. These {plural(total)} may already have been applied. Retry checks the same request; it never applies a correction twice.</p>
      {:else if counts}<p>{receiptDetail(counts) || 'Every selected correction was applied.'}</p>{/if}
    </div>
    {#if results}
      <ul class="ca-review-list">
        {#each results as result (result.finding_id)}
          {@const finding = findings.get(result.finding_id)}
          {#if finding}
            {@const outcome = displayOutcome(result, sentKey)}
            <li class="ca-review-item" data-receipt-item={finding.assessment.task_key} data-outcome={outcome}>
              <div class="ca-row-id"><span class="task-key">{finding.assessment.task_key}</span>
                {#if taskHref(finding)}<a href={taskHref(finding)} on:click={(event) => onOpenTask(finding, event)}>{taskTitle(finding)}</a>{:else}<span>{taskTitle(finding)}</span>{/if}</div>
              <p><strong class={`ca-outcome ca-outcome-${outcome}`}>{outcomeLabels[outcome] || outcome}</strong>
                · {actionLabel(finding.assessment.lifecycle_action, targetName(finding.assessment, columns))}: {sourceName(finding.assessment, columns)} → {targetName(finding.assessment, columns)}</p>
              {#if result.outcome === 'skipped' || result.outcome === 'failed'}<p class="ca-hint">{[result.message, recoveryHint(result)].filter(Boolean).join('. ')}</p>{/if}
            </li>
          {/if}
        {/each}
      </ul>
    {/if}
    {#if refreshNote}<p class="ca-hint" role="status">{refreshNote}</p>{/if}
    <div class="ca-review-actions">
      <button class="button quiet-button" type="button" on:click={onBack}>Back to results</button>
      {#if unknown && pending}<button class="button primary" type="button" disabled={applying} on:click={() => void send()}>{#if applying}<span class="button-spinner"></span>{/if}Retry same request</button>{/if}
    </div>
  {:else}
    <div class="ca-review-head">
      <h2 id="ca-review-heading" tabindex="-1" bind:this={heading}>Review {plural(entries.length)}</h2>
      <p>A read-only check of each card's current facts. Nothing has changed yet; Apply performs exactly these lifecycle actions.</p>
    </div>
    {#if previewError}<div class="inline-alert error" role="alert"><span>!</span><span>{previewError}</span><button class="text-button" type="button" on:click={() => void runPreview()}>Try again</button></div>{/if}
    {#if applyError}<div class="inline-alert error" role="alert"><span>!</span><span>{applyError} No correction in this request was applied.</span></div>{/if}
    <ul class="ca-review-list" aria-busy={previewing}>
      {#each entries as finding (finding.id)}
        {@const result = byFinding.get(finding.id)}
        {@const action = finding.assessment.lifecycle_action}
        {@const problem = inputProblem(action, inputs[finding.id])}
        <li class="ca-review-item" data-review-item={finding.assessment.task_key} data-outcome={result?.outcome || 'pending'}>
          <div class="ca-row-id"><span class="task-key">{finding.assessment.task_key}</span><span>{taskTitle(finding)}</span></div>
          <p><strong>{actionLabel(action, targetName(finding.assessment, columns))}</strong> · {sourceName(finding.assessment, columns)} → {targetName(finding.assessment, columns)}</p>
          {#if action === 'bug_resolve'}
            <label class="ca-field">Resolution<select value={inputs[finding.id]?.resolution || ''} on:change={(event) => setInput(finding.id, { resolution: event.currentTarget.value === 'fixed' ? 'fixed' : '' })}>
              <option value="">Choose a resolution…</option><option value="fixed">Fixed</option></select></label>
          {:else if action === 'bug_reopen'}
            <label class="ca-field">Reopen reason<textarea rows="2" maxlength="10000" value={inputs[finding.id]?.reason || ''} on:input={(event) => setInput(finding.id, { reason: event.currentTarget.value })}></textarea></label>
          {/if}
          {#if previewing}<p class="ca-hint">Checking current facts…</p>
          {:else if result?.outcome === 'eligible'}<p class="ca-ready-line">Ready to apply</p>
          {:else if result}<p class="ca-blocked-line"><strong>Can't apply · not counted below.</strong> {problem || result.message}</p>{/if}
        </li>
      {/each}
    </ul>
    <div class="ca-review-actions">
      <button class="button quiet-button" type="button" on:click={onBack}>Back to results</button>
      {#if dirty}<button class="button quiet-button" type="button" disabled={previewing} on:click={() => void runPreview()}>Update review</button>{/if}
      <button class="button primary" type="button" disabled={!preview || dirty || previewing || applying || !eligible.length} on:click={() => void apply()}>
        {#if applying}<span class="button-spinner"></span>{/if}Apply {plural(dirty ? 0 : eligible.length)}</button>
    </div>
  {/if}
</section>
