<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../api';
  import { readyColumnForBug } from '../bugIntake';
  import { ticketStatus, ticketPriorities, ticketPriorityLabels } from '../tickets';
  import type { BugSeverity, Task } from '../types';

  export let detail: Task;
  export let actionPending = false;
  export let dirty = false;
  export let onAct: (label: string, run: (task: Task) => Promise<Task>) => void | Promise<void> = () => undefined;

  let draftFor = '';
  let classifying = false;
  let actual = '';
  let expected = '';
  let reproduction = '';
  let severity: BugSeverity | '' = '';
  let priority: Task['priority'] = 'normal';
  let loadedVersion = -1;
  let loadedSeverity: BugSeverity | '' = '';
  let loadedPriority: Task['priority'] = 'normal';
  // Follow authoritative refreshes only for fields the user has not edited.
  // A conflict retains the user's draft for review against the refreshed task.
  function syncDetail(task: Task) {
    if (draftFor !== task.id) {
      draftFor = task.id;
      classifying = false;
      actual = task.description || '';
      expected = '';
      reproduction = '';
      severity = task.bug?.severity || '';
      priority = task.priority;
    } else if (loadedVersion === task.version) return;
    else {
      if (severity === loadedSeverity) severity = task.bug?.severity || '';
      if (priority === loadedPriority) priority = task.priority;
    }
    loadedSeverity = task.bug?.severity || '';
    loadedPriority = task.priority;
    loadedVersion = task.version;
  }
  $: syncDetail(detail);
  onDestroy(() => { dirty = false; });
  $: intake = ticketStatus(detail) === 'needs_triage';
  $: dirty = classifying || (detail.kind === 'bug' && !detail.bug?.resolution
    && (severity !== (detail.bug?.severity || '') || priority !== detail.priority));

  async function classify() {
    if (!actual.trim() || actionPending) return;
    const bug = { actual_behavior: actual.trim(), expected_behavior: expected.trim(), reproduction_steps: reproduction.trim() };
    await onAct('Bug classification', task => api.patchTask(task.id, { kind: 'bug', bug }, task.version));
    if (detail.kind === 'bug') classifying = false;
  }

  async function triage() {
    if (!severity || actionPending) return;
    const selectedSeverity = severity;
    const selectedPriority = priority;
    await onAct(intake ? 'Bug triage to Ready' : 'Bug triage', async task => {
      const column_id = await readyColumnForBug(task);
      return api.triageTask(task.id, task.version, { severity: selectedSeverity, priority: selectedPriority, column_id });
    });
  }
</script>

{#if detail.kind === 'bug'}
  <section class="ticket-bug-triage" aria-labelledby="ticket-bug-heading">
    <div class="heading"><h3 id="ticket-bug-heading">Bug triage</h3><span class="severity-badge" class:untriaged={!detail.bug?.severity}>{detail.bug?.severity?.toUpperCase() || 'Needs triage'}</span></div>
    <dl><dt>Actual behavior</dt><dd>{detail.bug?.actual_behavior}</dd>
      {#if detail.bug?.expected_behavior}<dt>Expected behavior</dt><dd>{detail.bug.expected_behavior}</dd>{/if}
      {#if detail.bug?.reproduction_steps}<dt>Reproduction steps</dt><dd>{detail.bug.reproduction_steps}</dd>{/if}
    </dl>
    {#if !detail.bug?.resolution}
      <form aria-label="Triage bug" on:submit|preventDefault={triage}>
        <fieldset disabled={actionPending}>
          <label>Severity<select aria-label="Bug severity" bind:value={severity} required><option value="">Choose severity</option><option value="s1">S1 · Critical</option><option value="s2">S2 · High</option><option value="s3">S3 · Medium</option><option value="s4">S4 · Low</option></select></label>
          <label>Priority<select aria-label="Bug priority" bind:value={priority}>{#each ticketPriorities as value}<option value={value}>{ticketPriorityLabels[value]}</option>{/each}</select></label>
          <button class="button primary" type="submit" disabled={!severity || actionPending}>{intake ? 'Triage & move to Ready' : 'Update bug triage'}</button>
        </fieldset>
      </form>
      <p class="hint">{intake ? 'Accept into Ready after reviewing severity, priority, and project.' : 'Bug details and ticket history stay together throughout the workflow.'}</p>
    {:else}<p class="hint">Resolved: {detail.bug.resolution.replaceAll('_', ' ')}. Open the full task to review or reopen.</p>{/if}
  </section>
{:else if ticketStatus(detail) !== 'completed'}
  {#if classifying}
    <form class="ticket-bug-triage classify" aria-label="Classify ticket as bug" on:submit|preventDefault={classify}>
      <h3>Classify as bug</h3><p class="hint">Keep this ticket, its comments, and its history. Add the bug report below.</p>
      <fieldset disabled={actionPending}>
        <label>Actual behavior<textarea bind:value={actual} rows="3" maxlength="20000" required></textarea></label>
        <label>Expected behavior<textarea bind:value={expected} rows="2" maxlength="20000"></textarea></label>
        <label>Reproduction steps<textarea bind:value={reproduction} rows="2" maxlength="20000"></textarea></label>
        <div class="actions"><button class="text-button" type="button" on:click={() => { classifying = false; }}>Cancel</button><button class="button primary" type="submit" disabled={!actual.trim() || actionPending}>Save as bug</button></div>
      </fieldset>
    </form>
  {:else}<button class="button quiet-button classify-trigger" type="button" disabled={actionPending} on:click={() => { classifying = true; }}>Classify as bug</button>{/if}
{/if}

<style>
  .ticket-bug-triage { padding: 16px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .heading, .actions { display: flex; gap: 10px; align-items: center; justify-content: space-between; }
  h3 { margin: 0; font-size: var(--text-sm); }
  dl { margin: 12px 0; font-size: var(--text-xs); }
  dt { font-weight: 650; margin-top: 10px; }
  dd { margin: 4px 0 0; color: var(--ink-soft); white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.6; }
  fieldset { display: flex; flex-wrap: wrap; gap: 10px; align-items: end; margin: 0; border: 0; padding: 0; min-width: 0; }
  label { display: grid; gap: 5px; font-size: var(--text-xs); font-weight: 600; }
  .classify fieldset { display: grid; }
  .hint { font-size: var(--text-xs); color: var(--muted); line-height: 1.6; margin: 10px 0 0; }
  .classify .hint { margin-bottom: 12px; }
  .classify-trigger { justify-self: start; }
  .actions { justify-content: flex-end; }
</style>
