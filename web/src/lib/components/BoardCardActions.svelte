<script context="module" lang="ts">
  export type BoardCardAction = {
    id: string;
    label: string;
    disabled?: boolean;
    reason?: string;
    run: () => void | Promise<void>;
  };
</script>

<script lang="ts">
  import { tick } from 'svelte';

  export let taskKey: string;
  export let actions: BoardCardAction[] = [];

  let trigger: HTMLButtonElement;
  let menu: HTMLDivElement;
  let open = false;
  let activeIndex = 0;
  let pending = false;

  function items(): HTMLButtonElement[] {
    return Array.from(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') || []);
  }

  function positionMenu() {
    if (!open || !trigger || !menu) return;
    const anchor = trigger.getBoundingClientRect();
    const bounds = menu.getBoundingClientRect();
    const left = Math.min(Math.max(8, anchor.right - bounds.width), window.innerWidth - bounds.width - 8);
    const below = anchor.bottom + 6;
    const top = below + bounds.height <= window.innerHeight - 8
      ? below : Math.max(8, anchor.top - bounds.height - 6);
    menu.style.left = `${left}px`;
    menu.style.top = `${top}px`;
  }

  function restoreFocus() {
    // A column move can destroy this component. Find the new instance by its
    // stable task key, then return to the same control after reconciliation.
    const current = document.querySelector<HTMLButtonElement>(`[data-card-actions="${CSS.escape(taskKey)}"]`);
    if (current) {
      current.focus({ preventScroll: true });
      if (!trigger?.isConnected) current.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    }
  }

  async function show(index = 0) {
    if (pending) return;
    activeIndex = index;
    menu.showPopover();
    open = true;
    await tick();
    positionMenu();
    items()[activeIndex]?.focus({ preventScroll: true });
  }

  function close(restore = true) {
    if (menu?.matches(':popover-open')) menu.hidePopover();
    open = false;
    if (restore) restoreFocus();
  }

  function toggled(event: Event) {
    const nextOpen = (event as ToggleEvent).newState === 'open';
    const wasOpen = open;
    open = nextOpen;
    if (!nextOpen && wasOpen) restoreFocus();
  }

  async function activate(action: BoardCardAction) {
    if (action.disabled || pending) return;
    pending = true;
    close();
    try {
      await action.run();
    } finally {
      pending = false;
      await tick();
      restoreFocus();
    }
  }

  function keydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      close();
    } else if (event.key === 'Tab') {
      // Return to the trigger before the browser performs ordinary tabbing.
      close();
    } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      event.stopPropagation();
      const count = actions.length;
      if (!count) return;
      activeIndex = event.key === 'Home' ? 0 : event.key === 'End' ? count - 1
        : (activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + count) % count;
      items()[activeIndex]?.focus();
    }
  }
</script>

<svelte:window on:resize={positionMenu} on:scroll|capture={positionMenu} />

<button
  bind:this={trigger}
  type="button"
  class="card-actions-trigger"
  data-card-actions={taskKey}
  aria-label={`Actions for ${taskKey}`}
  aria-haspopup="menu"
  aria-expanded={open}
  aria-busy={pending}
  on:click|stopPropagation={() => open ? close() : void show()}
  on:keydown={(event) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      event.stopPropagation();
      void show(event.key === 'ArrowUp' ? actions.length - 1 : 0);
    }
  }}
>Actions <span aria-hidden="true">⋯</span></button>

<div
  bind:this={menu}
  class="card-actions-menu"
  popover="auto"
  role="menu"
  aria-label={`Actions for ${taskKey}`}
  tabindex="-1"
  on:toggle={toggled}
  on:keydown={keydown}
  on:click|stopPropagation
>
  {#each actions as action, index (action.id)}
    <button
      type="button"
      role="menuitem"
      class="card-action-item"
      data-card-action={action.id}
      aria-disabled={Boolean(action.disabled || pending)}
      tabindex={activeIndex === index ? 0 : -1}
      on:focus={() => activeIndex = index}
      on:click|stopPropagation={() => void activate(action)}
    >
      <span>{action.label}</span>
      {#if action.disabled && action.reason}<small>{action.reason}</small>{/if}
    </button>
  {/each}
</div>

<style>
  .card-actions-trigger {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 7px;
    flex-shrink: 0;
    min-width: 80px;
    height: 44px;
    padding: 0 9px;
    border: 1px solid var(--border, #e5e8ef);
    border-radius: 7px;
    color: var(--ink-soft, #4e586a);
    background: var(--surface, #fff);
    font-size: var(--text-xs);
    font-weight: 600;
    white-space: nowrap;
  }
  .card-actions-trigger:hover, .card-actions-trigger[aria-expanded="true"] { background: var(--purple-soft, #efedff); color: var(--purple, #6d5efc); }
  .card-actions-menu {
    position: fixed;
    inset: auto;
    width: min(340px, calc(100vw - 16px));
    max-height: calc(100dvh - 16px);
    overflow-y: auto;
    overscroll-behavior: contain;
    margin: 0;
    padding: 6px;
    border: 1px solid var(--border-strong, #d9dde7);
    border-radius: 10px;
    color: var(--ink, #1d2433);
    background: var(--surface-raised, #fff);
    box-shadow: var(--shadow-md, 0 14px 40px #1d24331f);
  }
  .card-action-item {
    display: flex;
    flex-direction: column;
    justify-content: center;
    width: 100%;
    min-height: 44px;
    padding: 10px;
    border: 0;
    border-radius: 6px;
    color: inherit;
    background: transparent;
    text-align: left;
    overflow-wrap: anywhere;
    font-size: var(--text-sm);
    line-height: 1.4;
  }
  .card-action-item:hover, .card-action-item:focus-visible { background: var(--surface-hover, #f4f5fa); }
  .card-action-item[aria-disabled="true"] { color: var(--muted, #596579); cursor: default; }
  .card-action-item small { display: block; margin-top: 3px; font-size: var(--text-xs); line-height: 1.4; }
  .card-actions-trigger:focus-visible, .card-action-item:focus-visible { outline: 2px solid var(--purple, #6d5efc); outline-offset: -2px; }
</style>
