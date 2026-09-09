<script lang="ts">
  import {
    boardOverflowNavigation,
    defaultBoardOverflowState,
    scrollBoardColumn,
    type BoardOverflowDirection,
    type BoardOverflowState
  } from '../boardOverflowNavigation';

  export let label = 'Board columns';

  let state: BoardOverflowState = defaultBoardOverflowState;
  let navigationRoot: HTMLElement;
  const overflowOptions = { onChange: updateState };

  function updateState(next: BoardOverflowState): void {
    state = next;
  }

  function move(direction: BoardOverflowDirection): void {
    scrollBoardColumn(navigationRoot, direction);
  }
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
  {#if state.hasOverflow}
    <nav class="board-overflow-controls" aria-label="Board column navigation">
      <button
        class="board-overflow-button"
        data-testid="board-overflow-previous"
        type="button"
        aria-label="Show previous columns"
        title="Show previous columns"
        disabled={!state.canPrevious}
        on:click={() => move('previous')}
      >
        <span aria-hidden="true">←</span>
      </button>
      <button
        class="board-overflow-button"
        data-testid="board-overflow-next"
        type="button"
        aria-label="Show next columns"
        title="Show next columns"
        disabled={!state.canNext}
        on:click={() => move('next')}
      >
        <span aria-hidden="true">→</span>
      </button>
    </nav>
  {/if}

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

  .board-overflow-controls {
    min-height: 38px;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 3px;
    padding: 0 9px 4px;
  }

  .board-overflow-button {
    width: 30px;
    height: 30px;
    display: inline-grid;
    place-items: center;
    padding: 0;
    border: 1px solid transparent;
    border-radius: 6px;
    color: var(--ink-soft);
    background: var(--surface);
    font-size: 15px;
    line-height: 1;
    box-shadow: var(--shadow-sm);
  }

  .board-overflow-button:hover:not(:disabled),
  .board-overflow-button:focus-visible {
    color: var(--purple);
    border-color: var(--border-strong);
    background: var(--purple-soft);
  }

  .board-overflow-button:disabled {
    color: var(--faint);
    cursor: default;
    opacity: .48;
  }

  .board-overflow-edge {
    position: absolute;
    top: 42px;
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

  @media (max-width: 600px) {
    .board-overflow-controls {
      min-height: 46px;
      padding-right: 8px;
      padding-left: 8px;
    }

    .board-overflow-button {
      width: 38px;
      height: 38px;
    }

    .board-overflow-edge {
      top: 50px;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .board-overflow-edge {
      transition: none;
    }
  }
</style>
