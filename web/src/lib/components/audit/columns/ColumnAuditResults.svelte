<script lang="ts">
  import type { AssessmentSummary, AuditBaselinePin, Column, CorrectionResult } from '../../../types';
  import ColumnAuditRow from './ColumnAuditRow.svelte';
  import { baselineLabel, groupLabels, summaryHeadline, summaryLine, type AuditGroup } from './model';
  import type { V2Finding } from './types';

  /** The answer first: balanced server counts, baseline, then grouped rows. */
  export let summary: AssessmentSummary;
  export let baselines: AuditBaselinePin[] = [];
  export let groups: Record<AuditGroup, V2Finding[]>;
  export let tab: AuditGroup;
  export let columns: Column[] = [];
  export let receipts: Map<string, CorrectionResult> = new Map();
  export let selected: Set<string> = new Set();
  export let titleFor: (finding: V2Finding) => string;
  export let hrefFor: (finding: V2Finding) => string;
  export let onTab: (tab: AuditGroup) => void = () => undefined;
  export let onToggle: (finding: V2Finding, checked: boolean) => void = () => undefined;
  export let onEvidence: (finding: V2Finding, opener: HTMLElement) => void = () => undefined;
  export let onOpenTask: (finding: V2Finding, event: MouseEvent) => void = () => undefined;
  export let onReaudit: (finding: V2Finding) => void = () => undefined;
  export let onReview: () => void = () => undefined;

  const order: AuditGroup[] = ['corrections', 'correct', 'unverified'];
  const counts = (s: AssessmentSummary): Record<AuditGroup, number> => ({ corrections: s.corrections, correct: s.correct, unverified: s.unable_to_verify });

  // Empty groups are never shown; counts come from the server summary.
  $: visible = order.filter((group) => counts(summary)[group] > 0);
  $: rows = groups[tab] || [];
  $: count = selected.size;

  function tabKey(event: KeyboardEvent, index: number): void {
    if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft') return;
    event.preventDefault();
    const next = visible[(index + (event.key === 'ArrowRight' ? 1 : visible.length - 1)) % visible.length];
    onTab(next);
    document.getElementById(`ca-tab-${next}`)?.focus();
  }

  const groupHelp: Record<AuditGroup, string> = {
    corrections: 'Committed work supports a different column. Select the corrections to review; nothing moves until you apply them.',
    correct: 'Committed work supports the current column. Read-only.',
    unverified: 'These cards keep their columns. Each explains what could not be verified and how to recover.'
  };
</script>

<section class="ca-results" data-column-audit-results aria-labelledby="ca-results-heading">
  <header class="ca-summary">
    <h2 id="ca-results-heading">{summaryHeadline(summary)}</h2>
    <p>{summaryLine(summary)}</p>
    {#each baselines as pin}<p class="ca-baseline" data-audit-baseline>Based on <span class="ca-mono">{baselineLabel(pin)}</span></p>{/each}
  </header>

  {#if visible.length}
    <div class="ca-tabs" role="tablist" aria-label="Audit results">
      {#each visible as group, index (group)}
        <button id={`ca-tab-${group}`} class="ca-tab" type="button" role="tab" aria-selected={tab === group} aria-controls="ca-tabpanel"
          tabindex={tab === group ? 0 : -1} on:click={() => onTab(group)} on:keydown={(event) => tabKey(event, index)}>
          {groupLabels[group]} <span class="ca-count">{counts(summary)[group]}</span></button>
      {/each}
    </div>
    <div id="ca-tabpanel" role="tabpanel" aria-labelledby={`ca-tab-${tab}`} class="ca-tabpanel">
      <p class="ca-hint">{groupHelp[tab]}</p>
      <div class="ca-rows">
        {#each rows as finding (finding.id)}
          <ColumnAuditRow {finding} {columns} title={titleFor(finding)} href={hrefFor(finding)} receipt={receipts.get(finding.id)}
            selected={selected.has(finding.id)} onToggle={(checked) => onToggle(finding, checked)}
            onEvidence={(opener) => onEvidence(finding, opener)} onOpenTask={(event) => onOpenTask(finding, event)} onReaudit={() => onReaudit(finding)} />
        {/each}
      </div>
    </div>
  {:else}
    <p class="ca-hint">No cards were in this audit's scope.</p>
  {/if}

  {#if count}
    <div class="ca-review-bar" data-review-bar>
      <span role="status">{count} {count === 1 ? 'correction' : 'corrections'} selected</span>
      <button class="button primary" type="button" on:click={onReview}>Review {count} {count === 1 ? 'correction' : 'corrections'}</button>
    </div>
  {/if}
</section>
