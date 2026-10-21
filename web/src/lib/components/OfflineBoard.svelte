<script lang="ts">
  import type { OfflineBoard } from '../offlineBoards';
  import OfflineBoardColumns from './OfflineBoardColumns.svelte';
  import OfflineBoardControls from './OfflineBoardControls.svelte';

  export let boards: OfflineBoard[] = [];
  export let reconnect: () => void = () => undefined;
  export let reconnecting = false;
  export let error = '';
  export let clear: () => void = () => undefined;

  let selectedSlug = '';
  let search = '';
  let activeBoard: OfflineBoard | null = null;
  let sortedColumns: OfflineBoard['columns'] = [];
  let visibleTasks: OfflineBoard['tasks'] = [];
  let unassignedTasks: OfflineBoard['tasks'] = [];

  $: activeBoard = boards.find((board) => board.project.slug === selectedSlug) || boards[0] || null;
  $: sortedColumns = activeBoard
    ? [...activeBoard.columns].sort((left, right) => left.position - right.position)
    : [];
  $: visibleTasks = activeBoard ? activeBoard.tasks.filter((task) => matchesSearch(task, search)) : [];
  $: unassignedTasks = visibleTasks.filter((task) => !sortedColumns.some((column) => column.id === task.column_id));

  function matchesSearch(task: OfflineBoard['tasks'][number], value: string): boolean {
    const needle = value.trim().toLocaleLowerCase();
    if (!needle) return true;
    return [task.key, task.title, task.description].some((part) => part.toLocaleLowerCase().includes(needle));
  }

  function confirmClear(): void {
    if (typeof window === 'undefined') return;
    if (window.confirm('Clear all saved offline boards from this device? This cannot be undone.')) clear();
  }

  function reconnectLabel(): string {
    return reconnecting ? 'Reconnecting…' : 'Reconnect';
  }
</script>

<main class="offline-shell offline-page" data-testid="offline-board-view" aria-label="Helm offline read-only viewer">
  <header class="offline-header">
    <div class="offline-brand-lockup">
      <span class="offline-brand-mark" aria-hidden="true">H</span>
      <div class="offline-brand-copy">
        <strong>Helm</strong>
        <span>Offline</span>
      </div>
    </div>
    <div class="offline-header-actions">
      <span class="offline-readonly-badge">Read-only</span>
      <button
        class="offline-button offline-reconnect"
        data-testid="offline-reconnect"
        type="button"
        disabled={reconnecting}
        aria-busy={reconnecting}
        on:click={reconnect}
      >
        {#if reconnecting}<span class="offline-spinner" aria-hidden="true"></span>{/if}
        <span>{reconnectLabel()}</span>
      </button>
    </div>
  </header>

  <div class="offline-content">
    {#if error}
      <div class="offline-alert offline-alert-error" data-testid="offline-error" role="alert" aria-live="assertive">
        <span class="offline-alert-icon" aria-hidden="true">!</span>
        <p>{error}</p>
      </div>
    {/if}

    <section class="offline-intro" aria-labelledby="offline-heading">
      <div>
        <span class="offline-eyebrow">Workspace snapshot</span>
        <h1 id="offline-heading">Your boards, available offline</h1>
        <p>Browse a read-only copy of the pages you loaded before the connection dropped.</p>
      </div>
      {#if boards.length}
        <button class="offline-button offline-clear" data-testid="offline-clear" type="button" on:click={confirmClear}>Clear saved boards</button>
      {/if}
    </section>

    <aside class="offline-privacy" role="note">
      <span class="offline-privacy-icon" aria-hidden="true">⌂</span>
      <p><strong>Private content stays on this device.</strong> This viewer is read-only and does not send saved board data anywhere while you are offline.</p>
    </aside>

    {#if !boards.length}
      <section class="offline-empty" data-testid="offline-empty" aria-labelledby="offline-empty-heading">
        <span class="offline-empty-icon" aria-hidden="true">◌</span>
        <h2 id="offline-empty-heading">No saved boards yet</h2>
        <p>There is no board snapshot on this device. Reconnect to Helm to load your workspace; boards you open while online can be available here the next time you are offline.</p>
        <button class="offline-button offline-empty-reconnect" type="button" disabled={reconnecting} aria-busy={reconnecting} on:click={reconnect}>
          {#if reconnecting}<span class="offline-spinner" aria-hidden="true"></span>{/if}
          <span>{reconnecting ? 'Reconnecting…' : 'Reconnect to Helm'}</span>
        </button>
      </section>
    {:else if activeBoard}
      <OfflineBoardControls {boards} {activeBoard} visibleCount={visibleTasks.length} bind:selectedSlug bind:search />

      {#if activeBoard.partial}
        <div class="offline-alert offline-alert-warning" data-testid="offline-partial-warning" role="status">
          <span class="offline-alert-icon" aria-hidden="true">!</span>
          <p><strong>Partial snapshot.</strong> Only previously loaded or filtered tasks were saved; some tasks/details may be missing.</p>
        </div>
      {/if}

      <p class="offline-scope-note">Offline data is limited to previously loaded pages, not the full project. Search and project switching happen locally on this device.</p>

      {#if sortedColumns.length}
        <OfflineBoardColumns projectName={activeBoard.project.name} columns={sortedColumns} {visibleTasks} {unassignedTasks} {search} />
      {:else}
        <section class="offline-no-columns" aria-label="No saved columns">
          <h2>No columns in this snapshot</h2>
          <p>This project has no previously loaded column pages available offline.</p>
        </section>
      {/if}
    {/if}
  </div>
</main>

<style>
  .offline-shell {
    --offline-bg: var(--bg, #f7f8fb);
    --offline-surface: var(--surface, #ffffff);
    --offline-surface-raised: var(--surface-raised, #ffffff);
    --offline-surface-muted: var(--surface-muted, #f0f2f7);
    --offline-surface-hover: var(--surface-hover, #f4f5fa);
    --offline-ink: var(--ink, #1d2433);
    --offline-ink-soft: var(--ink-soft, #4e586a);
    --offline-muted: var(--muted, #596579);
    --offline-faint: var(--faint, #626b7d);
    --offline-border: var(--border, #e5e8ef);
    --offline-border-strong: var(--border-strong, #d9dde7);
    --offline-purple: var(--purple, #6d5efc);
    --offline-purple-soft: var(--purple-soft, #efedff);
    --offline-green: var(--green, #2ea879);
    --offline-green-soft: var(--green-soft, #e7f7f0);
    --offline-red: var(--red, #dc626f);
    --offline-red-soft: var(--red-soft, #fff0f1);
    --offline-amber: var(--amber, #d49534);
    --offline-amber-soft: var(--amber-soft, #fff7e8);
    --offline-shadow: var(--shadow-sm, 0 2px 8px rgba(35, 42, 61, .05));
    --offline-shadow-raised: var(--shadow-md, 0 14px 40px rgba(29, 36, 51, .12));
    width: 100%;
    min-width: 320px;
    min-height: 100vh;
    min-height: 100dvh;
    padding: max(18px, env(safe-area-inset-top, 0px)) max(18px, env(safe-area-inset-right, 0px)) max(26px, env(safe-area-inset-bottom, 0px)) max(18px, env(safe-area-inset-left, 0px));
    overflow-x: hidden;
    color: var(--offline-ink);
    background: radial-gradient(circle at 93% 0, color-mix(in srgb, var(--offline-purple-soft) 55%, transparent), transparent 34%), var(--offline-bg);
    font-family: var(--font-sans);
    text-rendering: optimizeLegibility;
  }

  .offline-header,
  .offline-content {
    width: min(1180px, 100%);
    margin: 0 auto;
  }

  .offline-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    min-height: 64px;
    padding: 6px 0 20px;
    border-bottom: 1px solid var(--offline-border);
  }

  .offline-brand-lockup,
  .offline-header-actions {
    display: flex;
    align-items: center;
  }

  .offline-brand-lockup { gap: 11px; }
  .offline-brand-mark {
    width: 38px;
    height: 38px;
    display: inline-grid;
    place-items: center;
    border-radius: 12px;
    color: #fff;
    background: linear-gradient(145deg, #8276ff, #5c4ced);
    box-shadow: 0 6px 15px rgba(109, 94, 252, .24);
    font-size: var(--text-lg);
    font-weight: 600;
    letter-spacing: -.02em;
  }

  .offline-brand-copy {
    display: flex;
    flex-direction: column;
    gap: 2px;
    line-height: 1.1;
  }

  .offline-brand-copy strong { font-size: var(--text-lg); letter-spacing: -.02em; }
  .offline-brand-copy span { color: var(--offline-muted); font-size: var(--text-xs); font-weight: 600; letter-spacing: .04em; }
  .offline-header-actions { justify-content: flex-end; gap: 9px; }

  .offline-readonly-badge {
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

  .offline-readonly-badge { color: var(--offline-purple); background: var(--offline-purple-soft); }

  .offline-button {
    min-height: 44px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    padding: 0 14px;
    border: 1px solid var(--offline-border-strong);
    border-radius: 10px;
    color: var(--offline-ink-soft);
    background: var(--offline-surface);
    font-size: var(--text-sm);
    font-weight: 600;
    transition: border-color 140ms ease, background 140ms ease, color 140ms ease, transform 140ms ease;
  }

  .offline-button:hover:not(:disabled),
  .offline-button:focus-visible { border-color: var(--offline-purple); color: var(--offline-purple); background: var(--offline-surface-hover); }
  .offline-button:active:not(:disabled) { transform: scale(.98); }
  .offline-button:disabled { cursor: wait; opacity: .68; }
  .offline-reconnect { border-color: var(--offline-purple); color: var(--offline-purple); background: var(--offline-purple-soft); }
  .offline-clear { color: var(--offline-red); }

  .offline-content { padding-top: 22px; }
  .offline-alert {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 11px 13px;
    margin-bottom: 18px;
    border: 1px solid var(--offline-border);
    border-radius: 11px;
    color: var(--offline-ink-soft);
    background: var(--offline-surface);
    font-size: var(--text-sm);
    line-height: 1.45;
  }

  .offline-alert p { margin: 1px 0 0; }
  .offline-alert strong { color: currentColor; }
  .offline-alert-icon {
    width: 19px;
    height: 19px;
    display: inline-grid;
    place-items: center;
    flex: 0 0 auto;
    border-radius: 50%;
    color: #fff;
    background: var(--offline-purple);
    font-size: var(--text-xs);
    font-weight: 600;
  }

  .offline-alert-error { border-color: color-mix(in srgb, var(--offline-red), var(--offline-border) 65%); color: var(--offline-red); background: var(--offline-red-soft); }
  .offline-alert-warning { border-color: color-mix(in srgb, var(--offline-amber), var(--offline-border) 65%); color: var(--offline-ink-soft); background: var(--offline-amber-soft); }
  .offline-alert-warning .offline-alert-icon { background: var(--offline-amber); }

  .offline-intro {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 22px;
    margin-bottom: 18px;
  }

  .offline-eyebrow {
    display: block;
    margin-bottom: 8px;
    color: var(--offline-purple);
    font-size: var(--text-xs);
    font-weight: 600;
    letter-spacing: .12em;
    text-transform: uppercase;
  }

  .offline-intro h1 {
    margin: 0;
    font-size: clamp(var(--text-xl), 4vw, var(--text-2xl));
    line-height: 1.08;
    letter-spacing: -.02em;
  }

  .offline-intro p {
    max-width: 620px;
    margin: 7px 0 0;
    color: var(--offline-muted);
    font-size: var(--text-sm);
    line-height: 1.55;
  }

  .offline-privacy {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    padding: 11px 13px;
    margin-bottom: 15px;
    border: 1px solid color-mix(in srgb, var(--offline-purple), var(--offline-border) 68%);
    border-radius: 11px;
    color: var(--offline-ink-soft);
    background: color-mix(in srgb, var(--offline-purple-soft) 42%, var(--offline-surface));
    font-size: var(--text-xs);
    line-height: 1.45;
  }

  .offline-privacy p { margin: 1px 0 0; }
  .offline-privacy strong { color: var(--offline-ink); }
  .offline-privacy-icon { color: var(--offline-purple); font-size: var(--text-md); line-height: 1; }

  .offline-empty,
  .offline-no-columns {
    display: grid;
    justify-items: center;
    padding: clamp(34px, 9vw, 72px) 22px;
    border: 1px dashed var(--offline-border-strong);
    border-radius: 15px;
    color: var(--offline-ink-soft);
    background: color-mix(in srgb, var(--offline-surface) 75%, transparent);
    text-align: center;
  }

  .offline-empty-icon { color: var(--offline-purple); font-size: var(--text-xl); line-height: 1; }
  .offline-empty h2,
  .offline-no-columns h2 { margin: 13px 0 0; color: var(--offline-ink); font-size: var(--text-lg); letter-spacing: -.02em; }
  .offline-empty p,
  .offline-no-columns p { max-width: 490px; margin: 9px 0 20px; color: var(--offline-muted); font-size: var(--text-xs); line-height: 1.55; }
  .offline-empty-reconnect { color: var(--offline-purple); border-color: var(--offline-purple); }

  .offline-scope-note { margin: 8px 0 12px; color: var(--offline-muted); font-size: var(--text-xs); line-height: 1.45; }

  .offline-no-columns { padding: 36px 20px; margin-top: 4px; }
  .offline-no-columns h2 { margin-top: 0; font-size: var(--text-md); }
  .offline-no-columns p { margin-bottom: 0; }

  .offline-spinner { width: 14px; height: 14px; display: inline-block; border: 2px solid color-mix(in srgb, var(--offline-purple), transparent 65%); border-top-color: var(--offline-purple); border-radius: 50%; animation: offline-spin .75s linear infinite; }
  @keyframes offline-spin { to { transform: rotate(360deg); } }

  :global(html[data-theme='dark']) .offline-shell {
    --offline-bg: #111219;
    --offline-surface: #191b25;
    --offline-surface-raised: #20222e;
    --offline-surface-muted: #252835;
    --offline-surface-hover: #2a2d3a;
    --offline-ink: #f0f1f6;
    --offline-ink-soft: #c0c4d0;
    --offline-muted: #aab1c0;
    --offline-faint: #969eaf;
    --offline-border: #2c2f3c;
    --offline-border-strong: #3a3e4c;
    --offline-purple: #958aff;
    --offline-purple-soft: #292641;
    --offline-green: #4bc494;
    --offline-green-soft: #1e342e;
    --offline-red: #f0828d;
    --offline-red-soft: #3d242b;
    --offline-amber: #e4ad53;
    --offline-amber-soft: #392f20;
  }

  @media (max-width: 680px) {
    .offline-shell { padding-right: max(13px, env(safe-area-inset-right, 0px)); padding-left: max(13px, env(safe-area-inset-left, 0px)); }
    .offline-header { min-height: 52px; gap: 8px; padding-top: 2px; padding-bottom: 11px; }
    .offline-brand-lockup { min-width: 0; flex: 1 1 auto; }
    .offline-brand-copy { min-width: 0; }
    .offline-brand-copy strong { font-size: var(--text-md); }
    .offline-header-actions { flex: 0 0 auto; gap: 6px; }
    .offline-header-actions .offline-reconnect { flex: 0 0 auto; padding-right: 10px; padding-left: 10px; }
    .offline-content { padding-top: 17px; }
    .offline-alert { padding: 9px 11px; margin-bottom: 12px; font-size: var(--text-xs); }
    .offline-intro { align-items: stretch; flex-direction: column; gap: 8px; margin-bottom: 11px; }
    .offline-eyebrow { margin-bottom: 5px; }
    .offline-intro h1 { font-size: var(--text-xl); }
    .offline-intro p { margin-top: 5px; font-size: var(--text-xs); }
    .offline-clear { align-self: flex-start; min-height: 40px; padding-right: 11px; padding-left: 11px; }
    .offline-privacy { padding: 8px 10px; margin-bottom: 12px; }
    .offline-scope-note { margin: 6px 0 10px; }
  }

  @media (prefers-reduced-motion: reduce) {
    .offline-button { transition: none; }
    .offline-spinner { animation: none; }
  }
</style>
