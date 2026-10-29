<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { Column, GithubAuditEvidence, GithubEvidenceItem } from '../../../types';
  import { focusTrap } from '../focus';
  import {
    actionLabel, citedItems, commitUrl, conditionTally, conditionWords, evidenceClassLabels, excerptLines, groupOf, lineLabel,
    observedCheckOutcome, prOnlyItems, refLabel, shortSha, sourceName, targetName
  } from './model';
  import type { V2Finding } from './types';

  /**
   * Evidence for one card, organized by its quoted conditions. A dialog: side
   * panel on desktop, full sheet on phones. Source text is inserted as text.
   */
  export let finding: V2Finding;
  export let columns: Column[] = [];
  export let evidence: GithubAuditEvidence | null = null;
  export let evidenceError = '';
  export let title = '';
  export let selectable = false;
  export let selected = false;
  export let onToggle: (checked: boolean) => void = () => undefined;
  export let onClose: () => void = () => undefined;

  let heading: HTMLElement;

  $: assessment = finding.assessment;
  $: items = new Map((evidence?.items || []).map((item) => [item.id, item]));
  $: pin = assessment.baselines[0];
  $: prOnly = prOnlyItems(finding.task_id, evidence?.items || []);
  $: gaps = (evidence?.criterion_gaps || []).filter((gap) => gap.task_id === finding.task_id && gap.source_role === 'baseline');
  $: group = groupOf(finding);
  $: action = actionLabel(assessment.lifecycle_action, targetName(assessment, columns));

  onMount(() => { void tick().then(() => heading?.focus()); });

  function keydown(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    onClose();
  }

  function itemRef(item: GithubEvidenceItem): string {
    const ref = item.ref && item.ref !== pin?.ref ? item.ref : refLabel(pin);
    return `${ref.replace(/^refs\/pull\/(\d+)\/head$/, 'PR #$1 head')} @ ${shortSha(item.sha)}`;
  }

  const prNumber = (item: GithubEvidenceItem) => item.ref?.match(/^refs\/pull\/(\d+)\/head$/)?.[1] || item.url.match(/\/pull\/(\d+)/)?.[1] || '';
</script>

<div class="ca-scrim" role="presentation" on:click={onClose}></div>
<div class="ca-evidence" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="ca-evidence-heading" use:focusTrap on:keydown={keydown}>
  <header class="ca-evidence-head">
    <div>
      <h2 id="ca-evidence-heading" tabindex="-1" bind:this={heading}>Evidence for {assessment.task_key}</h2>
      <p class="ca-evidence-title">{title}</p>
      {#if group === 'unverified'}<p><span class="ca-chip">{sourceName(assessment, columns)}</span> <span class="ca-pill neutral">Couldn't verify</span></p>
      {:else}<p class="ca-route"><span class="ca-chip">{sourceName(assessment, columns)}</span>{#if group === 'corrections'}<span aria-hidden="true">→</span><span class="ca-chip strong">{targetName(assessment, columns)}</span>{:else}<span class="ca-hint">already correct</span>{/if}</p>{/if}
      <p class="ca-hint">{conditionTally(assessment)}{pin ? ` · Judged against ${pin.repository} · ${refLabel(pin)} @ ${shortSha(pin.sha)}` : ''}</p>
    </div>
    <button class="icon-button" type="button" aria-label="Close evidence" on:click={onClose}>×</button>
  </header>

  {#if evidenceError}
    <div class="inline-alert error" role="alert"><span>!</span><span>{evidenceError} Private source excerpts are hidden until access is restored.</span></div>
  {/if}

  <ol class="ca-conditions">
    {#each assessment.conditions as condition (condition.id)}
      {@const word = conditionWords[condition.result] || conditionWords.unknown}
      <li class="ca-condition" data-criterion-result={condition.result}>
        <p class={`ca-result ca-result-${condition.result}`}><span aria-hidden="true">{word.icon}</span> {word.label}</p>
        <blockquote>{condition.text}</blockquote>
        <p>{condition.explanation}</p>
        {#each condition.gaps as gap}<p class="ca-gap"><strong>Missing:</strong> {gap}. This prevents a conclusive judgment for this condition.</p>{/each}
        {#if !evidenceError}
          {#each citedItems(condition.citation_ids, items) as item (item.id)}
            <figure class="ca-citation">
              <figcaption><span class="ca-kind">{evidenceClassLabels[item.evidence_class || ''] || item.kind}</span>
                <span class="ca-mono">{lineLabel(item)}</span> <span class="ca-hint">{itemRef(item)}</span></figcaption>
              {#if item.kind === 'file'}<pre class="ca-excerpt">{#each excerptLines(item) as line}<span class="ca-line"><span class="ca-ln" aria-hidden="true">{line.n}</span>{line.text}
</span>{/each}</pre>{:else if item.kind === 'check'}<p class="ca-hint">Observed: {observedCheckOutcome(item)}</p>{:else}<p class="ca-hint">{item.title}</p>{/if}
              {#if commitUrl(item)}<a class="ca-link" href={commitUrl(item)} target="_blank" rel="noopener noreferrer">Open at commit</a>{/if}
            </figure>
          {/each}
        {/if}
      </li>
    {/each}
  </ol>

  {#if assessment.basis.length}
    <section class="ca-basis" aria-label="Why this column"><h3>Why this column</h3>
      <ul>{#each assessment.basis as basis}<li>{basis.explanation}</li>{/each}</ul></section>
  {/if}

  {#if prOnly.length && !evidenceError}
    <section class="ca-pr-only" data-evidence-pr-only aria-label="Linked pull request evidence">
      <h3>Linked PR #{prNumber(prOnly[0])} head · not on {refLabel(pin) || 'the baseline'}</h3>
      <p class="ca-hint">Separate evidence only. It does not establish what is committed on the baseline.</p>
      <ul>{#each prOnly as item (item.id)}<li><span class="ca-kind">{evidenceClassLabels[item.evidence_class || ''] || item.kind}</span> <span class="ca-mono">{item.path ? lineLabel(item) : item.title}</span> <span class="ca-hint">@ {shortSha(item.sha)}</span></li>{/each}</ul>
    </section>
  {/if}

  {#if gaps.length && !evidenceError}
    <details class="ca-tech"><summary>Inspection gaps</summary><ul>{#each gaps as gap}<li>{gap.path ? `${gap.path}: ` : ''}{gap.message}</li>{/each}</ul></details>
  {/if}

  <details class="ca-tech"><summary>Technical details</summary>
    <dl>
      {#each assessment.baselines as baseline}<dt>Baseline</dt><dd class="ca-mono">{baseline.repository} {baseline.ref} {baseline.sha}</dd>{/each}
      <dt>Captured</dt><dd>{new Date(assessment.captured_at).toLocaleString()} · card version {assessment.captured_version}</dd>
      <dt>Publication eligibility</dt><dd>{assessment.application_eligibility.status}{#if assessment.application_eligibility.reasons.length} ({assessment.application_eligibility.reasons.join(', ')}){/if}. The final review rechecks everything.</dd>
    </dl>
  </details>

  {#if selectable}
    <footer class="ca-evidence-foot">
      <label class="check-label"><input type="checkbox" checked={selected} on:change={(event) => onToggle(event.currentTarget.checked)} /><span>Select: {action}</span></label>
      <span class="ca-hint">Selecting records review intent only; nothing moves.</span>
    </footer>
  {/if}
</div>
