<script lang="ts">
  import { tick } from 'svelte';

  export let activeCount = 0;
  export let onClear: () => void;
  export let heading = 'Board filters';
  export let triggerLabel = 'Filters';

  let trigger: HTMLButtonElement;
  let panel: HTMLDialogElement;
  let open = false;

  function positionPanel() {
    if (!open || !trigger || !panel) return;
    const anchor = trigger.getBoundingClientRect();
    panel.style.setProperty('--panel-left', `${Math.max(8, Math.min(anchor.left, window.innerWidth - 568))}px`);
    panel.style.setProperty('--panel-top', `${Math.min(anchor.bottom + 8, Math.max(8, window.innerHeight - 360))}px`);
  }

  async function show() {
    panel.showModal();
    open = true;
    positionPanel();
    await tick();
    panel.querySelector<HTMLElement>('.filter-panel-fields select, .filter-panel-fields input, .filter-panel-fields button')?.focus();
  }

  function close() {
    panel?.close();
    open = false;
    trigger?.focus({ preventScroll: true });
  }

  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Tab') return;
    const controls = Array.from(panel.querySelectorAll<HTMLElement>('button:not(:disabled), select:not(:disabled), input:not(:disabled), [href], [tabindex="0"]'))
      .filter((element) => element.getClientRects().length > 0);
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  }
</script>

<svelte:window on:resize={positionPanel} />

<button bind:this={trigger} class="filter-panel-trigger" type="button" data-board-filter-trigger aria-haspopup="dialog" aria-expanded={open} aria-label={`${triggerLabel}${activeCount ? `, ${activeCount} active` : ''}`} on:click={() => void show()}>
  <span aria-hidden="true">☷</span> {triggerLabel}
  {#if activeCount}<span class="filter-count" aria-hidden="true">{activeCount}</span>{/if}
</button>

<dialog
  bind:this={panel}
  class="board-filter-panel"
  aria-label={heading}
  on:cancel={(event) => { event.preventDefault(); close(); }}
  on:close={() => { open = false; }}
  on:keydown={keydown}
  on:click={(event) => {
    if (event.target !== panel) return;
    const bounds = panel.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) close();
  }}
>
  <header><h2>{heading}</h2><button class="filter-panel-close" type="button" aria-label="Close board filters" on:click={close}>×</button></header>
  <div class="filter-panel-fields"><slot /></div>
  <footer>
    <button class="filter-panel-clear" type="button" aria-disabled={activeCount === 0} on:click={() => { if (activeCount) onClear(); }}>Clear all</button>
    <button class="filter-panel-done" type="button" on:click={close}>Show results</button>
  </footer>
</dialog>

<style>
  .filter-panel-trigger, .board-filter-panel button {
    min-width: 44px;
    min-height: 44px;
    border: 1px solid var(--border-strong, #d9dde7);
    border-radius: 7px;
    color: var(--ink-soft, #4e586a);
    background: var(--surface, #fff);
    font-size: var(--text-sm);
    font-weight: 600;
  }
  .filter-panel-trigger { display: inline-flex; align-items: center; justify-content: center; gap: 7px; flex-shrink: 0; padding: 0 12px; }
  .filter-panel-trigger:hover, .filter-panel-trigger[aria-expanded="true"] { color: var(--purple, #6d5efc); background: var(--purple-soft, #efedff); }
  .filter-count { display: inline-grid; place-items: center; min-width: 20px; height: 20px; padding: 0 5px; border-radius: 10px; background: var(--purple-soft, #efedff); color: var(--purple, #6d5efc); font-size: var(--text-xs); }
  .board-filter-panel {
    position: fixed;
    inset: var(--panel-top, 80px) auto auto var(--panel-left, 8px);
    display: none;
    flex-direction: column;
    width: min(560px, calc(100vw - 16px));
    max-width: none;
    max-height: calc(100dvh - var(--panel-top, 80px) - 8px);
    margin: 0;
    padding: 0;
    border: 1px solid var(--border-strong, #d9dde7);
    border-radius: 12px;
    color: var(--ink, #1d2433);
    background: var(--surface-raised, #fff);
    box-shadow: var(--shadow-md, 0 14px 40px #1d24331f);
    overflow: hidden;
  }
  .board-filter-panel[open] { display: flex; }
  .board-filter-panel::backdrop { background: rgb(20 25 40 / 24%); }
  header, footer { display: flex; align-items: center; justify-content: space-between; flex-shrink: 0; gap: 12px; padding: 12px 16px; }
  header { border-bottom: 1px solid var(--border, #e5e8ef); }
  h2 { margin: 0; font: 600 var(--text-md) var(--font-sans); }
  .filter-panel-close { font-size: var(--text-xl) !important; line-height: 1; }
  .filter-panel-fields { min-height: 0; overflow-y: auto; overscroll-behavior: contain; padding: 16px; }
  .filter-panel-fields :global(.filter-group) { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; width: 100%; min-width: 0; overflow: visible; }
  .filter-panel-fields :global(select), .filter-panel-fields :global(input), .filter-panel-fields :global(button) { min-width: 0; width: 100%; min-height: 44px; max-width: 100%; }
  .filter-panel-fields :global(label) { display: grid; gap: 6px; min-width: 0; color: var(--ink-soft, #4e586a); font-size: var(--text-xs); }
  .filter-panel-fields :global(select) { padding: 10px 8px; border: 1px solid var(--border-strong, #d9dde7); border-radius: 6px; color: var(--ink, #1d2433); background: var(--surface, #fff); }
  footer { border-top: 1px solid var(--border, #e5e8ef); }
  footer button { padding: 0 14px; }
  .filter-panel-clear[aria-disabled="true"] { color: var(--muted, #596579); cursor: default; }
  .board-filter-panel .filter-panel-done { background: var(--purple, #6d5efc); border-color: transparent; color: #fff; }
  .filter-panel-trigger:focus-visible, .board-filter-panel button:focus-visible { outline: 2px solid var(--purple, #6d5efc); outline-offset: 2px; }
  @media (max-width: 600px) {
    .board-filter-panel { inset: auto 0 0; width: 100%; max-height: min(90dvh, 760px); border-radius: 16px 16px 0 0; border-bottom: 0; padding-bottom: env(safe-area-inset-bottom, 0px); }
    .filter-panel-fields :global(.filter-group) { grid-template-columns: minmax(0, 1fr); }
  }
</style>
