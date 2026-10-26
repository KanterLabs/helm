<script lang="ts">
  import type { Task } from '../types';
  import { ticketAge, ticketAssigneeLabel, ticketPriorityLabels, ticketStatus, ticketStatusLabels } from '../tickets';

  export let tickets: Task[] = [];
  export let selectedKey = '';
  export let loading = false;
  export let error = '';
  export let nextCursor = '';
  export let queueLabel = '';
  export let userId = '';
  export let actorNames: Record<string, string> = {};
  /** The filed project's key, or an empty string while the ticket is unfiled. */
  export let projectKey: (task: Task) => string = () => '';
  export let onSelect: (task: Task) => void = () => undefined;
  export let onRetry: () => void = () => undefined;
  export let onMore: () => void = () => undefined;
  export let listElement: HTMLElement | null = null;

  function listKeydown(event: KeyboardEvent) {
    const rows = Array.from(listElement?.querySelectorAll<HTMLElement>('[data-ticket-key]') || []);
    const index = rows.findIndex((row) => row === document.activeElement);
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const next = rows[Math.min(rows.length - 1, Math.max(0, index + (event.key === 'ArrowDown' ? 1 : -1)))];
      next?.focus();
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      (event.key === 'Home' ? rows[0] : rows[rows.length - 1])?.focus();
    }
  }
</script>

<div class="tickets-list-pane">
  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}<button class="text-button" type="button" on:click={onRetry}>Retry</button></div>{/if}
  {#if loading && !tickets.length}
    <div class="list-skeleton" aria-label="Loading tickets"><div></div><div></div><div></div></div>
  {:else if !tickets.length}
    <div class="empty-state"><div class="empty-icon">✓</div><h2>Nothing in {queueLabel}</h2><p>New alerts and requests land in Needs triage.</p></div>
  {:else}
    <ul class="ticket-list" bind:this={listElement} aria-label="Tickets">
      {#each tickets as task (task.id)}
        <li>
          <button class="ticket-row" class:selected={selectedKey === task.key} type="button" data-ticket-key={task.key} aria-current={selectedKey === task.key ? 'true' : undefined} on:keydown={listKeydown} on:click={() => onSelect(task)}>
            <span class="ticket-row-top">
              <span class="task-key">{task.key}</span>
              {#if task.kind === 'bug'}<span class="issue-kind-badge">Bug</span><span class="severity-badge" class:untriaged={!task.bug?.severity}>{task.bug?.severity?.toUpperCase() || 'Untriaged'}</span>{/if}
              <span class={`priority-pill priority-${task.priority}`}>{ticketPriorityLabels[task.priority]}</span>
              <span class={`ticket-status status-${ticketStatus(task)}`}>{ticketStatusLabels[ticketStatus(task)]}</span>
              {#if (task.alert_source?.occurrence_count || 0) > 1}<span class="ticket-repeat" title="Times this alert was received">×{task.alert_source?.occurrence_count}</span>{/if}
            </span>
            <strong>{task.title}</strong>
            <span class="ticket-row-meta">
              <span data-ticket-project>{projectKey(task) || 'Not filed'}{#if task.alert_source} · {task.alert_source.resource_name}{/if}</span>
              <span>{ticketAssigneeLabel(task, userId, actorNames)}</span>
              <span>{ticketAge(task.ticket?.created_at || task.created_at)}</span>
            </span>
          </button>
        </li>
      {/each}
    </ul>
    {#if nextCursor}<button class="button quiet-button ticket-more" type="button" on:click={onMore}>Load more tickets</button>{/if}
  {/if}
</div>

<style>
  .ticket-list { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .ticket-row { width: 100%; display: grid; gap: 5px; padding: 11px 12px; border: 1px solid var(--border); border-radius: 10px; color: var(--ink); background: var(--surface); text-align: left; }
  .ticket-row:hover { background: var(--surface-hover); }
  .ticket-row.selected { border-color: var(--purple); box-shadow: inset 3px 0 0 var(--purple); }
  .ticket-row:focus-visible { outline: 2px solid var(--purple); outline-offset: 2px; }
  .ticket-row strong { font-size: var(--text-sm); line-height: 1.35; }
  .ticket-row-top { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .ticket-row-meta { display: flex; flex-wrap: wrap; gap: 4px 12px; color: var(--muted); font-size: var(--text-xs); }
  .ticket-status { padding: 2px 7px; border: 1px solid var(--border-strong); border-radius: 5px; font-size: var(--text-xs); font-weight: 600; }
  .ticket-status.status-needs_triage { color: var(--semantic-amber); background: var(--amber-soft); }
  .ticket-status.status-waiting { color: var(--semantic-red); background: var(--red-soft); }
  .ticket-status.status-in_progress { color: var(--purple); background: var(--purple-soft); }
  .ticket-status.status-completed { color: var(--semantic-green); background: var(--green-soft); }
  .ticket-repeat { padding: 2px 6px; border-radius: 5px; color: var(--ink-soft); background: var(--surface-muted); font-size: var(--text-xs); font-weight: 600; }
  .ticket-more { margin-top: 8px; width: 100%; }
</style>
