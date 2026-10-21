<script lang="ts">
  import type { OfflineBoard } from '../offlineBoards';

  export let task: OfflineBoard['tasks'][number];
  // Cards in a known column also list their column and priority details.
  export let columnName = '';

  function priorityLabel(priority: OfflineBoard['tasks'][number]['priority']): string {
    const labels: Record<string, string> = {
      urgent: 'Urgent',
      high: 'High',
      normal: 'Normal',
      low: 'Low'
    };
    return labels[String(priority)] || 'Priority not recorded';
  }

  function priorityClass(priority: OfflineBoard['tasks'][number]['priority']): string {
    return ['urgent', 'high', 'normal', 'low'].includes(String(priority)) ? String(priority) : 'unknown';
  }
</script>

<details class="offline-task-card task-card" data-testid="offline-task-card" data-task-id={task.id}>
  <summary class="offline-task-summary">
    <span class="offline-task-top">
      <span class="offline-task-key">{task.key}</span>
      <span class={`offline-priority offline-priority-${priorityClass(task.priority)}`}>{priorityLabel(task.priority)}</span>
    </span>
    <strong>{task.title}</strong>
  </summary>
  <div class="offline-task-details">
    {#if task.description}
      <p class="offline-task-description">{task.description}</p>
    {:else}
      <p class="offline-task-description offline-task-description-empty">No description saved.</p>
    {/if}
    {#if columnName}
      <dl>
        <div><dt>Column</dt><dd>{columnName}</dd></div>
        <div><dt>Priority</dt><dd>{priorityLabel(task.priority)}</dd></div>
      </dl>
    {/if}
    <span class="offline-card-readonly">Read-only saved detail</span>
  </div>
</details>

<style>
  .offline-priority,
  .offline-card-readonly {
    display: inline-flex;
    align-items: center;
    min-height: 24px;
    padding: 3px 8px;
    border-radius: 999px;
    color: var(--offline-muted);
    background: var(--offline-surface-muted);
    font-size: var(--text-xs);
    font-weight: 600;
    letter-spacing: .04em;
    white-space: nowrap;
  }

  .offline-task-card { overflow: hidden; flex: 0 0 auto; border: 1px solid var(--offline-border); border-radius: 10px; background: var(--offline-surface); box-shadow: var(--offline-shadow); transition: border-color 140ms ease, box-shadow 140ms ease; }
  .offline-task-card:hover { border-color: var(--offline-border-strong); box-shadow: var(--offline-shadow-raised); }
  .offline-task-card[open] { border-color: color-mix(in srgb, var(--offline-purple), var(--offline-border) 56%); }
  .offline-task-summary { min-height: 74px; display: grid; align-content: center; gap: 7px; padding: 11px 12px; color: var(--offline-ink); cursor: pointer; list-style-position: inside; }
  .offline-task-summary::-webkit-details-marker { color: var(--offline-purple); }
  .offline-task-summary:focus-visible { outline: 3px solid color-mix(in srgb, var(--offline-purple), transparent 76%); outline-offset: -3px; }
  .offline-task-top { display: flex; align-items: center; min-width: 0; gap: 7px; }
  .offline-task-key { min-width: 0; overflow: hidden; color: var(--offline-muted); font: 600 var(--text-xs) var(--font-mono); letter-spacing: .05em; text-overflow: ellipsis; white-space: nowrap; }
  .offline-priority { min-height: 20px; margin-left: auto; padding: 2px 6px; color: var(--offline-muted); font-size: var(--text-xs); letter-spacing: 0; }
  .offline-priority-urgent { color: var(--offline-red); background: var(--offline-red-soft); }
  .offline-priority-high { color: var(--offline-amber); background: var(--offline-amber-soft); }
  .offline-priority-normal { color: var(--offline-purple); background: var(--offline-purple-soft); }
  .offline-priority-low { color: var(--offline-muted); background: var(--offline-surface-muted); }
  .offline-task-summary > strong { overflow: hidden; font-size: var(--text-md); line-height: 1.35; text-overflow: ellipsis; }
  .offline-task-details { padding: 0 12px 12px 30px; }
  .offline-task-description { margin: 0 0 12px; color: var(--offline-ink-soft); font-size: var(--text-base); line-height: 1.5; white-space: pre-wrap; overflow-wrap: anywhere; }
  .offline-task-description-empty { color: var(--offline-muted); font-style: italic; }
  .offline-task-details dl { display: grid; gap: 6px; margin: 0 0 11px; }
  .offline-task-details dl > div { display: flex; justify-content: space-between; gap: 10px; font-size: var(--text-xs); }
  .offline-task-details dt { color: var(--offline-muted); }
  .offline-task-details dd { margin: 0; color: var(--offline-ink-soft); font-weight: 600; text-align: right; }
  .offline-card-readonly { min-height: 20px; padding: 2px 6px; color: var(--offline-muted); font-size: var(--text-xs); letter-spacing: 0; }

  @media (max-width: 680px) {
    .offline-task-summary { min-height: 78px; padding-top: 12px; padding-bottom: 12px; }
  }

  @media (prefers-reduced-motion: reduce) {
    .offline-task-card { transition: none; }
  }
</style>
