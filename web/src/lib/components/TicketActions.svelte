<script lang="ts">
  import { api } from '../api';
  import type { Task, TicketStatus } from '../types';
  import { ticketStatus } from '../tickets';

  export let detail: Task;
  export let actionPending = false;
  export let waitOpen = false;
  export let waitReason = '';
  export let onAct: (label: string, run: (task: Task) => Promise<Task>) => void | Promise<void> = () => undefined;
  export let onMove: (status: TicketStatus, label: string) => void | Promise<void> = () => undefined;
  export let onOpenTask: (task: Task) => void | Promise<void> = () => undefined;

  async function markWaiting() {
    const reason = waitReason.trim();
    if (!reason) return;
    await onAct('Waiting', (task) => api.blockTask(task.id, task.version, reason));
  }
</script>

<div class="ticket-actions" role="group" aria-label="Ticket actions">
  {#if detail.kind !== 'bug' && ticketStatus(detail) === 'needs_triage'}<button class="button primary" type="button" disabled={actionPending} on:click={() => onMove('ready', 'Accept')}>Accept to Ready</button>{/if}
  {#if (detail.kind !== 'bug' && ticketStatus(detail) === 'needs_triage') || ticketStatus(detail) === 'ready'}<button class="button quiet-button" type="button" disabled={actionPending} on:click={() => onMove('in_progress', 'Start')}>Start</button>{/if}
  {#if ticketStatus(detail) === 'waiting'}<button class="button primary" type="button" disabled={actionPending} on:click={() => onMove('ready', 'Resume')}>Resume</button>{/if}
  {#if ticketStatus(detail) !== 'completed' && ticketStatus(detail) !== 'waiting'}<button class="button quiet-button" type="button" disabled={actionPending} aria-expanded={waitOpen} on:click={() => { waitOpen = !waitOpen; }}>Waiting…</button>{/if}
  {#if detail.kind === 'bug'}<button class="button quiet-button" type="button" disabled={actionPending} on:click={() => onOpenTask(detail)}>{detail.bug?.resolution ? 'Review resolution / reopen' : 'Resolve bug…'}</button>
  {:else if ticketStatus(detail) !== 'completed'}<button class="button complete-button" type="button" disabled={actionPending} on:click={() => onAct('Complete', (task) => api.completeTask(task.id, task.version))}>✓ Complete</button>
  {:else}<button class="button quiet-button" type="button" disabled={actionPending} on:click={() => onMove('needs_triage', 'Reopen')}>Reopen</button>{/if}
  <button class="text-button ticket-open-board" type="button" on:click={() => onOpenTask(detail)}>Open full task ↗</button>
</div>
{#if waitOpen}
  <form class="ticket-wait" on:submit|preventDefault={markWaiting}>
    <label>What is this waiting on?<textarea rows="2" bind:value={waitReason} required placeholder="e.g. Maintenance window on Saturday"></textarea></label>
    <div class="ticket-create-actions"><button class="text-button" type="button" on:click={() => { waitOpen = false; waitReason = ''; }}>Cancel</button><button class="button primary" type="submit" disabled={!waitReason.trim() || actionPending}>Mark waiting</button></div>
  </form>
{/if}

<style>
  .ticket-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .ticket-open-board { margin-left: auto; }
  .ticket-wait { display: grid; gap: 10px; padding: 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .ticket-wait label { display: grid; gap: 5px; font-size: var(--text-xs); font-weight: 600; }
  .ticket-create-actions { display: flex; justify-content: flex-end; align-items: center; gap: 12px; }
  @media (max-width: 760px) {
    .ticket-open-board { margin-left: 0; }
  }
</style>
