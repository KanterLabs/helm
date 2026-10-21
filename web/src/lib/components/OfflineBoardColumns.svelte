<script lang="ts">
  import { boardCardHeight } from '../boardLayout';
  import type { OfflineBoard } from '../offlineBoards';
  import OfflineTaskCard from './OfflineTaskCard.svelte';

  export let projectName = '';
  export let columns: OfflineBoard['columns'] = [];
  export let visibleTasks: OfflineBoard['tasks'] = [];
  export let unassignedTasks: OfflineBoard['tasks'] = [];
  export let search = '';

  const safeBoardCardHeight = (node: HTMLElement) => {
    // jsdom and older embedded web views may not expose ResizeObserver. The
    // CSS fallback still gives those environments natural-height columns.
    if (typeof ResizeObserver === 'undefined') return {};
    return boardCardHeight(node);
  };

  function tasksForColumn(columnId: string): OfflineBoard['tasks'] {
    return visibleTasks.filter((task) => task.column_id === columnId);
  }

  function columnHeadingId(index: number): string {
    return `offline-column-heading-${index}`;
  }
</script>

<section class="offline-board-columns" use:safeBoardCardHeight aria-label={`${projectName} offline board`}>
  {#each columns as column, index (column.id)}
    {@const columnTasks = tasksForColumn(column.id)}
    <article class="offline-column board-column" aria-labelledby={columnHeadingId(index)}>
      <header class="offline-column-header">
        <div class="offline-column-title">
          <span class="offline-column-dot" aria-hidden="true"></span>
          <h2 id={columnHeadingId(index)}>{column.name}</h2>
        </div>
        <span class="offline-column-count" aria-label={`${columnTasks.length} tasks`}>{columnTasks.length}</span>
      </header>
      <div class="offline-column-cards column-cards">
        {#if columnTasks.length}
          {#each columnTasks as task (task.id)}
            <OfflineTaskCard {task} columnName={column.name} />
          {/each}
        {:else}
          <p class="offline-column-empty">{search.trim() ? 'No saved tasks match this search.' : 'No saved tasks in this column.'}</p>
        {/if}
      </div>
    </article>
  {/each}

  {#if unassignedTasks.length}
    <article class="offline-column board-column" aria-labelledby="offline-unassigned-heading">
      <header class="offline-column-header">
        <div class="offline-column-title">
          <span class="offline-column-dot offline-column-dot-muted" aria-hidden="true"></span>
          <h2 id="offline-unassigned-heading">Other saved tasks</h2>
        </div>
        <span class="offline-column-count" aria-label={`${unassignedTasks.length} tasks`}>{unassignedTasks.length}</span>
      </header>
      <div class="offline-column-cards column-cards">
        {#each unassignedTasks as task (task.id)}
          <OfflineTaskCard {task} />
        {/each}
      </div>
    </article>
  {/if}
</section>

<style>
  .offline-board-columns {
    min-width: 0;
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: minmax(275px, 1fr);
    align-items: start;
    gap: 14px;
    overflow-x: auto;
    overscroll-behavior-x: contain;
    padding: 2px 2px 18px;
    scroll-snap-type: x proximity;
    scrollbar-width: thin;
  }

  .offline-column {
    min-width: 0;
    min-height: 290px;
    height: var(--board-column-height, auto);
    display: flex;
    flex-direction: column;
    overflow: hidden;
    border: 1px solid var(--offline-border);
    border-radius: 13px;
    background: color-mix(in srgb, var(--offline-surface-muted) 68%, transparent);
    scroll-snap-align: start;
  }

  .offline-column-header {
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    justify-content: space-between;
    gap: 9px;
    min-height: 53px;
    padding: 0 13px;
    background: color-mix(in srgb, var(--offline-surface-muted) 86%, transparent);
  }

  .offline-column-title { display: flex; align-items: center; min-width: 0; gap: 9px; }
  .offline-column-title h2 { min-width: 0; margin: 0; overflow: hidden; font-size: var(--text-base); letter-spacing: -.02em; text-overflow: ellipsis; white-space: nowrap; }
  .offline-column-dot { width: 8px; height: 8px; flex: 0 0 auto; border-radius: 50%; background: var(--offline-purple); box-shadow: 0 0 0 3px color-mix(in srgb, var(--offline-purple), transparent 82%); }
  .offline-column-dot-muted { background: var(--offline-muted); box-shadow: 0 0 0 3px color-mix(in srgb, var(--offline-muted), transparent 82%); }
  .offline-column-count { min-width: 23px; padding: 3px 6px; border-radius: 8px; color: var(--offline-muted); background: var(--offline-surface); font-size: var(--text-xs); font-weight: 600; text-align: center; }

  .offline-column-cards {
    min-height: 0;
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 8px;
    overflow-y: auto;
    overscroll-behavior-y: contain;
    padding: 12px 9px 9px;
    scrollbar-gutter: stable;
  }

  .offline-column-empty { min-height: 105px; display: grid; place-items: center; margin: 0; padding: 16px 10px; color: var(--offline-muted); font-size: var(--text-xs); line-height: 1.45; text-align: center; }

  @media (max-width: 680px) {
    .offline-board-columns { grid-auto-columns: minmax(82vw, 1fr); gap: 11px; margin-right: -13px; padding-right: 13px; scroll-snap-type: x mandatory; }
    .offline-column { min-height: 260px; }
  }
</style>
