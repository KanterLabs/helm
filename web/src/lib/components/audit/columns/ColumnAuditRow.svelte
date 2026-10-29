<script lang="ts">
  import type { Column, CorrectionResult } from '../../../types';
  import { actionLabel, conditionTally, groupOf, publicationBlocker, rowState, sourceName, targetName } from './model';
  import type { V2Finding } from './types';

  /** One result row: identity, current → recommended, reason, coverage, actions. */
  export let finding: V2Finding;
  export let columns: Column[] = [];
  export let title = '';
  export let href = '';
  export let receipt: CorrectionResult | undefined = undefined;
  export let selected = false;
  export let onToggle: (checked: boolean) => void = () => undefined;
  export let onEvidence: (opener: HTMLElement) => void = () => undefined;
  export let onOpenTask: (event: MouseEvent) => void = () => undefined;
  export let onReaudit: () => void = () => undefined;

  $: assessment = finding.assessment;
  $: group = groupOf(finding);
  $: state = rowState(finding, receipt);
  $: source = sourceName(assessment, columns);
  $: target = targetName(assessment, columns);
  $: action = actionLabel(assessment.lifecycle_action, target);
</script>

<article class={`ca-row ca-row-${group}`} data-audit-row={assessment.task_key} data-row-state={state} aria-label={`${assessment.task_key} ${title}`}>
  <div class="ca-row-main">
    <div class="ca-row-id"><span class="task-key">{assessment.task_key}</span>
      {#if href}<a href={href} on:click={onOpenTask}>{title}</a>{:else}<span>{title}</span>{/if}</div>
    {#if group === 'corrections'}
      {#if state === 'applied'}
        <p class="ca-route ca-applied" data-audit-route><span class="ca-pill good">{receipt?.outcome === 'already_applied' ? 'Already applied' : 'Applied'}: {source} → {target}</span></p>
      {:else}
        <p class="ca-route" class:muted={state === 'stale'} data-audit-route><span class="ca-chip">{source}</span><span aria-hidden="true">→</span><span class="ca-chip strong" title="Recommended column">{target}</span></p>
      {/if}
    {:else if group === 'correct'}
      <p class="ca-route"><span class="ca-chip">{source}</span><span class="ca-pill good">Already correct</span></p>
    {:else}
      <p class="ca-route"><span class="ca-chip">{source}</span><span class="ca-pill neutral">Couldn't verify</span></p>
    {/if}
    <p class="ca-reason" class:muted={state === 'stale'}>{assessment.reason}</p>
    <p class="ca-hint">{conditionTally(assessment)}{#if group === 'unverified'} · This is not a column change.{/if}</p>
    {#if state === 'blocked'}
      <p class="ca-blocker"><strong>{target} recommended</strong> · {publicationBlocker(assessment)}</p>
    {:else if state === 'stale'}
      <p class="ca-blocker"><strong>Card changed after this audit.</strong> This recommendation judged the earlier card; re-audit to judge it again.</p>
    {/if}
  </div>
  <div class="ca-row-actions">
    <button class="button quiet-button compact-button" type="button" on:click={(event) => onEvidence(event.currentTarget)}>{group === 'unverified' ? 'Review inspected evidence' : 'Review evidence'}</button>
    {#if state === 'selectable'}
      <label class="ca-select"><input type="checkbox" checked={selected} aria-label={`Select ${assessment.task_key}: ${action}`} on:change={(event) => onToggle(event.currentTarget.checked)} /><span aria-hidden="true">Select: {action}</span></label>
    {:else if state === 'stale' || group === 'unverified'}
      <button class="button quiet-button compact-button" type="button" on:click={onReaudit}>{group === 'unverified' ? 'Re-audit this card' : 'Re-audit'}</button>
    {:else if (state === 'blocked' || state === 'applied') && href}
      <a class="button quiet-button compact-button" href={href} on:click={onOpenTask}>Open card</a>
    {/if}
  </div>
</article>
