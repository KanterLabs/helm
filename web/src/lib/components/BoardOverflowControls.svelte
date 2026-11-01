<script lang="ts">
  import type { BoardOverflowDirection, BoardOverflowState } from '../boardOverflowNavigation';

  // Previous/next column buttons. They live in the board toolbar row so the
  // columns do not need a row of their own above them.
  export let state: BoardOverflowState;
  export let onMove: (direction: BoardOverflowDirection) => void;
</script>

<nav class="board-overflow-controls" aria-label="Board column navigation">
  <button
    class="board-overflow-button"
    data-testid="board-overflow-previous"
    type="button"
    aria-label="Show previous columns"
    title="Show previous columns"
    disabled={!state.canPrevious}
    on:click={() => onMove('previous')}
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
    on:click={() => onMove('next')}
  >
    <span aria-hidden="true">→</span>
  </button>
</nav>

<style>
  /* One segmented control at the toolbar's 44px height. */
  .board-overflow-controls {
    height: 44px;
    display: inline-flex;
    flex: 0 0 auto;
    overflow: hidden;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
  }

  .board-overflow-button {
    width: 42px;
    height: 100%;
    display: inline-grid;
    place-items: center;
    padding: 0;
    border: 0;
    color: var(--ink-soft);
    background: transparent;
    font-size: var(--text-md);
    line-height: 1;
  }

  .board-overflow-button + .board-overflow-button {
    border-left: 1px solid var(--border);
  }

  .board-overflow-button:hover:not(:disabled),
  .board-overflow-button:focus-visible {
    color: var(--purple);
    background: var(--purple-soft);
  }

  .board-overflow-button:focus-visible {
    outline: 2px solid var(--purple);
    outline-offset: -2px;
  }

  .board-overflow-button:disabled {
    color: var(--faint);
    cursor: default;
    opacity: .48;
  }
</style>
