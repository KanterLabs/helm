<script lang="ts">
  import type { OfflineBoard } from '../offlineBoards';

  export let boards: OfflineBoard[] = [];
  export let activeBoard: OfflineBoard;
  export let visibleCount = 0;
  export let selectedSlug = '';
  export let search = '';

  function savedAtIso(value: number): string {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? '' : date.toISOString();
  }

  function savedAtLabel(value: number): string {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return 'Unknown time';
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium',
      timeStyle: 'short'
    }).format(date);
  }

  function selectBoard(event: Event): void {
    selectedSlug = (event.currentTarget as HTMLSelectElement).value;
  }
</script>

<section class="offline-board-controls" aria-label="Offline board controls">
  <div class="offline-project-picker">
    <label for="offline-project-picker">Saved project</label>
    <select id="offline-project-picker" data-testid="offline-project-picker" value={activeBoard.project.slug} aria-label="Choose a saved project" on:change={selectBoard}>
      {#each boards as board (board.project.slug)}
        <option value={board.project.slug}>{board.project.name} · {board.project.key}</option>
      {/each}
    </select>
  </div>
  <label class="offline-search" for="offline-task-search">
    <span aria-hidden="true">⌕</span>
    <span class="sr-only">Search saved tasks</span>
    <input id="offline-task-search" data-testid="offline-search" type="search" bind:value={search} placeholder="Search saved tasks…" autocomplete="off" />
  </label>
</section>

<section class="offline-snapshot-meta" aria-label="Snapshot details">
  <div class="offline-snapshot-copy">
    <strong>{activeBoard.project.name}</strong>
    <span>{visibleCount} of {activeBoard.tasks.length} saved {activeBoard.tasks.length === 1 ? 'task' : 'tasks'} shown</span>
  </div>
  <div class="offline-snapshot-time" data-testid="offline-saved-at">
    <span>Saved</span>
    <time datetime={savedAtIso(activeBoard.savedAt)}>{savedAtLabel(activeBoard.savedAt)}</time>
  </div>
</section>

<style>
  .offline-board-controls {
    display: grid;
    grid-template-columns: minmax(190px, 260px) minmax(220px, 1fr);
    align-items: end;
    gap: 12px;
    margin-bottom: 15px;
  }

  .offline-project-picker,
  .offline-search { display: grid; gap: 6px; }
  .offline-project-picker label { color: var(--offline-muted); font-size: var(--text-xs); font-weight: 600; letter-spacing: .08em; text-transform: uppercase; }
  .offline-project-picker select,
  .offline-search input {
    width: 100%;
    min-height: 44px;
    border: 1px solid var(--offline-border);
    border-radius: 9px;
    color: var(--offline-ink);
    background: var(--offline-surface);
    font-size: var(--text-md);
  }

  .offline-project-picker select { padding: 0 31px 0 11px; }
  .offline-search { position: relative; }
  .offline-search > span:first-child { position: absolute; top: 13px; left: 12px; color: var(--offline-muted); font-size: var(--text-md); line-height: 1; }
  .offline-search input { padding: 0 11px 0 35px; }
  .offline-project-picker select:hover,
  .offline-search input:hover { border-color: var(--offline-border-strong); }
  .offline-project-picker select:focus,
  .offline-search input:focus { outline: 3px solid color-mix(in srgb, var(--offline-purple), transparent 76%); outline-offset: 1px; border-color: var(--offline-purple); }

  .offline-snapshot-meta {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 15px;
    padding: 14px 15px;
    margin-bottom: 11px;
    border: 1px solid var(--offline-border);
    border-radius: 11px;
    background: var(--offline-surface);
    box-shadow: var(--offline-shadow);
  }

  .offline-snapshot-copy { display: grid; gap: 3px; min-width: 0; }
  .offline-snapshot-copy strong { overflow: hidden; color: var(--offline-ink); font-size: var(--text-base); text-overflow: ellipsis; white-space: nowrap; }
  .offline-snapshot-copy span,
  .offline-snapshot-time { color: var(--offline-muted); font-size: var(--text-xs); }
  .offline-snapshot-time { display: grid; gap: 3px; justify-items: end; white-space: nowrap; }
  .offline-snapshot-time time { color: var(--offline-ink-soft); font-weight: 600; }

  .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }

  @media (max-width: 680px) {
    .offline-board-controls { grid-template-columns: 1fr; gap: 8px; margin-bottom: 10px; }
    .offline-snapshot-meta { align-items: flex-start; flex-direction: column; gap: 7px; padding: 10px 12px; margin-bottom: 8px; }
    .offline-snapshot-time { justify-items: start; }
  }
</style>
