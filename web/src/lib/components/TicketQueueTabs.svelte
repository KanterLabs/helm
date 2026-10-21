<script lang="ts">
  import type { TicketCounts, TicketQueue } from '../types';
  import { ticketQueues as queues } from '../tickets';

  export let queue: TicketQueue = 'open';
  export let counts: TicketCounts | null = null;
  export let onSelect: (queue: TicketQueue) => void = () => undefined;
</script>

<div class="ticket-queues" role="tablist" aria-label="Ticket queues">
  {#each queues as item (item.value)}
    <button class="ticket-queue" class:active={queue === item.value} type="button" role="tab" aria-selected={queue === item.value} on:click={() => onSelect(item.value)}>
      <span>{item.label}</span>{#if counts}<span class="ticket-queue-count" data-queue-count={item.value}>{counts[item.value]}</span>{/if}
    </button>
  {/each}
</div>

<style>
  .ticket-queues { display: flex; flex-wrap: wrap; gap: 6px; }
  .ticket-queue { display: inline-flex; align-items: center; gap: 6px; min-height: 32px; padding: 0 11px; border: 1px solid var(--border); border-radius: 999px; color: var(--ink-soft); background: var(--surface); font-size: var(--text-xs); font-weight: 600; }
  .ticket-queue.active { color: var(--purple); border-color: var(--purple); background: var(--purple-soft); }
  .ticket-queue-count { min-width: 18px; padding: 0 5px; border-radius: 999px; background: var(--surface-muted); font-size: var(--text-xs); text-align: center; }
  .ticket-queue:focus-visible { outline: 2px solid var(--purple); outline-offset: 2px; }
</style>
