<script lang="ts">
  import { onDestroy } from 'svelte';
  import {
    boardOverflowNavigation,
    defaultBoardOverflowState,
    scrollBoardColumn,
    type BoardOverflowDirection,
    type BoardOverflowState
  } from '../boardOverflowNavigation';

  export let label = 'Board columns';
  /** Reports overflow so the toolbar can render the column arrows beside its controls. */
  export let onChange: ((state: BoardOverflowState, move: ((direction: BoardOverflowDirection) => void) | null) => void) | undefined = undefined;

  let state: BoardOverflowState = defaultBoardOverflowState;
  let navigationRoot: HTMLElement;
  const overflowOptions = { onChange: updateState };

  function updateState(next: BoardOverflowState): void {
    state = next;
    onChange?.(next, move);
  }

  function move(direction: BoardOverflowDirection): void {
    if (navigationRoot) scrollBoardColumn(navigationRoot, direction);
  }

  onDestroy(() => onChange?.(defaultBoardOverflowState, null));
</script>

<div
  class="board-overflow-navigation"
  class:has-overflow={state.hasOverflow}
  class:at-start={state.atStart}
  class:at-end={state.atEnd}
  aria-label={label}
  data-board-overflow={state.hasOverflow ? 'true' : 'false'}
  data-board-overflow-at-start={state.atStart ? 'true' : 'false'}
  data-board-overflow-at-end={state.atEnd ? 'true' : 'false'}
  use:boardOverflowNavigation={overflowOptions}
  bind:this={navigationRoot}
>
  <slot />

  {#if state.hasOverflow}
    <span class="board-overflow-edge board-overflow-edge-start" class:active={state.canPrevious} aria-hidden="true"></span>
    <span class="board-overflow-edge board-overflow-edge-end" class:active={state.canNext} aria-hidden="true"></span>
  {/if}
</div>

<style>
  .board-overflow-navigation {
    position: relative;
    min-width: 0;
  }

  /* The edges cover the columns, not the board's scrollbar padding beneath them. */
  .board-overflow-edge {
    position: absolute;
    top: 2px;
    bottom: 18px;
    z-index: 3;
    width: 24px;
    pointer-events: none;
    opacity: 0;
    transition: opacity var(--dur-fast) var(--ease-out);
  }

  .board-overflow-edge.active {
    opacity: 1;
  }

  .board-overflow-edge-start {
    left: 0;
    background: linear-gradient(90deg, color-mix(in srgb, var(--purple-soft) 86%, var(--surface)), transparent);
  }

  .board-overflow-edge-end {
    right: 0;
    background: linear-gradient(270deg, color-mix(in srgb, var(--purple-soft) 86%, var(--surface)), transparent);
  }

  @media (prefers-reduced-motion: reduce) {
    .board-overflow-edge {
      transition: none;
    }
  }
</style>
