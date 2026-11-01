<script lang="ts">
  import { onDestroy } from 'svelte';
  import type { Task } from '../../types';
  import { artifactSummaries, noteArtifactSummaryVersion, trackArtifactSummary } from './artifactSummaries';
  import { requestPullRequestFocus, summaryAccessibleLabel, summaryTone } from './pullRequests';

  /** Quiet board-card PR badge. It sits outside the card's main button. */
  export let task: Pick<Task, 'id' | 'key' | 'version'>;
  export let onOpen: () => void = () => undefined;

  let trackedId = '';
  let release: (() => void) | null = null;

  $: if (task?.id && task.id !== trackedId) {
    release?.();
    trackedId = task.id;
    release = trackArtifactSummary(task.id, task.version);
  }
  $: if (task?.id === trackedId) noteArtifactSummaryVersion(task.id, task.version);
  $: summary = task?.id ? $artifactSummaries[task.id] : undefined;
  $: label = summary ? summaryAccessibleLabel(summary, task.key) : '';

  onDestroy(() => release?.());

  function open(): void {
    requestPullRequestFocus(task.id);
    onOpen();
  }
</script>

{#if summary && summary.count > 0}
  <button class="pr-badge" type="button" data-pr-badge data-state={summaryTone(summary)} aria-label={label} title={label} on:click|stopPropagation={open}>
    <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="3.5" r="1.7" /><circle cx="4" cy="12.5" r="1.7" /><circle cx="12" cy="12.5" r="1.7" /><path d="M4 5.2v5.6M12 10.8V6.5a2 2 0 0 0-2-2H7.5M9 2.8 7.3 4.5 9 6.2" /></svg>
    <span class="pr-badge-label">{summary.label || `${summary.count} PRs`}</span>
    <span class="pr-badge-dot" aria-hidden="true"></span>
  </button>
{/if}

<style>
  /* Matches the footer's 44px hit area with an inset pill, like Details. */
  .pr-badge {
    --tone: var(--muted);
    height: 44px;
    min-height: 44px;
    display: inline-flex;
    flex-shrink: 0;
    align-items: center;
    gap: 5px;
    padding: 0 6px;
    border: 6px solid transparent;
    border-width: 6px 2px;
    border-radius: 12px;
    color: var(--muted);
    background-color: transparent;
    background-clip: padding-box;
    font: 500 var(--text-xs) var(--font-mono);
    cursor: pointer;
  }
  .pr-badge:hover, .pr-badge:focus-visible { color: var(--purple); background-color: var(--purple-soft); }
  .pr-badge-label { white-space: nowrap; }
  .pr-badge-dot { width: 7px; height: 7px; flex-shrink: 0; border-radius: 50%; background: var(--tone); }
  /* Unknown, unverified and unavailable are hollow so colour is never the only cue. */
  .pr-badge[data-state='failing'] { --tone: var(--red); }
  .pr-badge[data-state='pending'] { --tone: var(--amber); }
  .pr-badge[data-state='passing'] { --tone: var(--green); }
  .pr-badge[data-state='merged'] { --tone: var(--purple); }
  .pr-badge[data-state='closed'] { --tone: var(--faint); }
  .pr-badge[data-state='unknown'] .pr-badge-dot,
  .pr-badge[data-state='unverified'] .pr-badge-dot,
  .pr-badge[data-state='unavailable'] .pr-badge-dot { border: 1.5px solid var(--muted); background: transparent; }
</style>
