<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../api';
  import type { Task } from '../types';
  import AgentPulse, { type AgentWorkLike } from './AgentPulse.svelte';
  import { agentPulseExactTimestamp, agentPulsePresentation } from './agentPulseText';

  export let task: Task;
  export let now = Date.now();
  export let actorLabel = '';

  let lastProgress = '';
  let progressLoading = false;
  let progressRequest = 0;
  let progressIdentity = '';
  let progressRequestKey = '';
  onDestroy(() => { progressRequest += 1; });

  $: work = readWork(task);
  $: phase = work?.phase || '';
  $: summary = work?.summary || '';
  $: nextAction = work?.next_action || '';
  $: started = work?.started_at || '';
  $: updated = work?.updated_at || '';
  $: progress = readProgress(work);
  $: ({ stale, stateLabel, freshnessLabel } = agentPulsePresentation(task, now));
  $: panelLabel = work
    ? `${actorLabel || work.actor_id || 'Agent'} work, ${stateLabel}, ${freshnessLabel}${phase ? `, ${phase}` : ''}`
    : 'No live agent work pulse';
  // Only the detail panel reads progress evidence. A heartbeat may cause a
  // re-read, but never supplies or substitutes the milestone timestamp.
  $: loadLastProgress(task.id, work?.operation_id || '', work?.actor_id || '', updated, task.version);

  async function loadLastProgress(taskID: string, operationID: string, actorID: string, checkIn: string, version: number): Promise<void> {
    const key = JSON.stringify([taskID, operationID, actorID, checkIn, version]);
    if (key === progressRequestKey) return;
    progressRequestKey = key;
    const request = ++progressRequest;
    const identity = `${taskID}/${operationID}/${actorID}`;
    if (identity !== progressIdentity) lastProgress = '';
    progressIdentity = identity;
    progressLoading = Boolean(operationID);
    if (!operationID) {
      lastProgress = '';
      return;
    }
    try {
      let before: string | undefined;
      // Bound both the response size and number of reads when older operations
      // dominate history. Absence within this window remains unavailable.
      for (let page = 0; page < 3; page += 1) {
        const timeline = await api.listTaskTimeline(taskID, { kind: 'agent_progress', limit: 50, before });
        if (request !== progressRequest) return;
        const match = timeline.data.find((item) => item.kind === 'agent_progress'
          && item.task_id === taskID
          && item.progress?.operation_id === operationID
          && (!actorID || item.progress.actor_id === actorID)
          && Number.isFinite(Date.parse(item.created_at)));
        if (match) {
          lastProgress = match.created_at;
          return;
        }
        if (!timeline.next_cursor || timeline.next_cursor === before) break;
        before = timeline.next_cursor;
      }
      lastProgress = '';
    } catch {
      if (request === progressRequest) lastProgress = '';
    } finally {
      if (request === progressRequest) progressLoading = false;
    }
  }

  function readWork(value: Task): AgentWorkLike | null {
    const candidate = (value as Task & { agent_work?: unknown }).agent_work;
    return candidate && typeof candidate === 'object' ? candidate as AgentWorkLike : null;
  }

  function readProgress(value: AgentWorkLike | null): { completed: number; total: number; percent: number } | null {
    if (!value) return null;
    const completed = firstNumber(value.checkpoint_completed);
    const total = firstNumber(
      value.checkpoint_total,
      Array.isArray(value.checkpoint_refs) && value.checkpoint_refs.length ? value.checkpoint_refs.length : undefined
    );
    if (completed === undefined || total === undefined || total <= 0) return null;
    const bounded = Math.max(0, Math.min(total, completed));
    return { completed: bounded, total, percent: Math.round((bounded / total) * 100) };
  }

  function firstNumber(...values: Array<number | null | undefined>): number | undefined {
    return values.find((value): value is number => typeof value === 'number' && Number.isFinite(value));
  }

  function formatDateTime(value: string): string {
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      timeZoneName: 'short'
    }).format(parsed);
  }
</script>

<section class:missing={!work} class:stale class="agent-work-panel" aria-labelledby="agent-work-heading">
  <div class="agent-work-heading">
    <div>
      <span class="eyebrow">Live agent work</span>
      <h2 id="agent-work-heading">{work ? `${actorLabel || work.actor_id || 'Agent'} · ${stateLabel}` : 'No live pulse yet'}</h2>
    </div>
    <span class="agent-work-state" aria-label={panelLabel}>{work ? stateLabel : '?'}</span>
  </div>
  {#if work}
    <AgentPulse {task} {now} {actorLabel} compact={false} />
    {#if stale}<p class="agent-work-stale">{stateLabel} check-in is stale and needs attention.</p>{/if}
    <dl class="agent-work-details">
      {#if phase}<div><dt>Phase</dt><dd>{phase}</dd></div>{/if}
      {#if progress}<div><dt>Progress</dt><dd>{progress.completed} of {progress.total} checkpoints ({progress.percent}%)</dd></div>{/if}
      {#if summary}<div><dt>Summary</dt><dd>{summary}</dd></div>{/if}
      {#if nextAction}<div><dt>Next</dt><dd>{nextAction}</dd></div>{/if}
      {#if started}<div><dt>Started</dt><dd><time datetime={started}>{formatDateTime(started)}</time></dd></div>{/if}
      <div><dt>Last check-in</dt><dd data-agent-last-check-in>{#if updated}<time datetime={updated} title={agentPulseExactTimestamp(updated)}>{formatDateTime(updated)}</time>{:else}Unavailable{/if}</dd></div>
      <div><dt>Last progress</dt><dd data-agent-last-progress>{#if lastProgress}<time datetime={lastProgress} title={agentPulseExactTimestamp(lastProgress)}>{formatDateTime(lastProgress)}</time>{:else}{progressLoading ? 'Loading…' : 'Unavailable'}{/if}</dd></div>
    </dl>
  {:else}
    <p class="agent-work-empty">This task has no reported agent progress. A live pulse will appear when an agent checks in.</p>
  {/if}
</section>
