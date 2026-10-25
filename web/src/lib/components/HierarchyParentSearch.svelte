<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import type { Task, TaskHierarchy } from '../types';
  import { hierarchyCandidates, nextHierarchyOptionIndex } from './hierarchyRelations';

  export let task: Task;
  export let hierarchy: TaskHierarchy | null = null;
  export let projectTasks: Task[] = [];
  export let mutationBusy = false;
  export let candidatesLoading = false;
  export let addError = '';
  /** Links the chosen parent; resolves true when the search should reset. */
  export let onSelect: (candidate: Task) => Promise<boolean> = async () => false;

  let query = '';
  let comboboxOpen = false;
  let activeIndex = -1;
  let searchInput: HTMLInputElement;
  let blurTimer: ReturnType<typeof setTimeout> | undefined;

  $: matches = hierarchyCandidates(projectTasks, task.id, hierarchy, query);
  $: if (activeIndex >= matches.length) activeIndex = matches.length ? matches.length - 1 : -1;
  $: activeOptionId = activeIndex >= 0 && matches[activeIndex]
    ? `hierarchy-option-${matches[activeIndex].id}`
    : undefined;

  onDestroy(() => {
    if (blurTimer) clearTimeout(blurTimer);
  });

  export function focus() {
    searchInput?.focus();
  }

  async function choose(candidate: Task): Promise<void> {
    if (!(await onSelect(candidate))) return;
    query = '';
    activeIndex = -1;
    comboboxOpen = false;
    await tick();
    searchInput?.focus();
  }

  function handleComboboxKeydown(event: KeyboardEvent) {
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      comboboxOpen = true;
      activeIndex = nextHierarchyOptionIndex(activeIndex, event.key, matches.length);
      return;
    }
    if (event.key === 'Enter' && activeIndex >= 0 && matches[activeIndex]) {
      event.preventDefault();
      void choose(matches[activeIndex]);
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      comboboxOpen = false;
      activeIndex = -1;
    }
  }

  function deferComboboxClose() {
    if (blurTimer) clearTimeout(blurTimer);
    blurTimer = setTimeout(() => {
      comboboxOpen = false;
      activeIndex = -1;
    }, 120);
  }
</script>

<form class="hierarchy-add" on:submit|preventDefault={() => activeIndex >= 0 && matches[activeIndex] && choose(matches[activeIndex])}>
  <label for="hierarchy-search">Link parent</label>
  <div class="combobox-wrap">
    <input
      bind:this={searchInput}
      id="hierarchy-search"
      role="combobox"
      aria-autocomplete="list"
      aria-expanded={comboboxOpen && Boolean(query.trim())}
      aria-controls="hierarchy-options"
      aria-activedescendant={activeOptionId}
      autocomplete="off"
      placeholder="Search this project by key or title"
      disabled={mutationBusy || candidatesLoading}
      bind:value={query}
      on:focus={() => comboboxOpen = true}
      on:input={() => { comboboxOpen = true; activeIndex = matches.length ? 0 : -1; addError = ''; }}
      on:keydown={handleComboboxKeydown}
      on:blur={deferComboboxClose}
    />
    {#if comboboxOpen && query.trim()}
      <div id="hierarchy-options" class="hierarchy-options" role="listbox" aria-label="Project tasks">
        {#if matches.length}
          {#each matches as candidate, index (candidate.id)}
            <button id={`hierarchy-option-${candidate.id}`} class:active={index === activeIndex} type="button" role="option" aria-selected={index === activeIndex} tabindex="-1" on:mouseenter={() => activeIndex = index} on:mousedown|preventDefault on:click={() => choose(candidate)}>
              <span>{candidate.key}</span><strong>{candidate.title}</strong>
            </button>
          {/each}
        {:else}
          <p>No available tasks match “{query.trim()}”.</p>
        {/if}
      </div>
    {/if}
  </div>
  <small>{candidatesLoading ? 'Loading project tasks…' : 'Current task and descendants are excluded.'}</small>
  {#if addError}<p class="relationship-error" role="alert">{addError}</p>{/if}
</form>

<style>
  .hierarchy-add { display: grid; gap: 5px; padding-top: 12px; border-top: 1px solid var(--border); }
  .hierarchy-add > label { color: var(--ink-soft); font-size: var(--text-2xs); font-weight: 600; }
  .hierarchy-add > small { color: var(--faint); font-size: var(--text-2xs); }
  .combobox-wrap { position: relative; }
  .combobox-wrap > input { width: 100%; min-height: 44px; padding-right: 34px; }
  .hierarchy-options { position: absolute; top: calc(100% + 4px); right: 0; left: 0; z-index: 8; max-height: 225px; overflow: auto; padding: 5px; border: 1px solid var(--border-strong); border-radius: 9px; background: var(--surface-raised); box-shadow: var(--shadow-md); }
  .hierarchy-options button { width: 100%; min-height: 44px; display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center; gap: 8px; padding: 7px 8px; border: 0; border-radius: 6px; color: var(--ink); background: transparent; text-align: left; }
  .hierarchy-options button:hover, .hierarchy-options button.active { background: var(--purple-soft); }
  .hierarchy-options button span { color: var(--purple); font: 600 var(--text-2xs) var(--font-mono); }
  .hierarchy-options button strong { min-width: 0; overflow: hidden; font-size: var(--text-2xs); text-overflow: ellipsis; white-space: nowrap; }
  .hierarchy-options > p { margin: 0; padding: 10px; color: var(--muted); font-size: var(--text-2xs); }
  .relationship-error { margin: 0; color: var(--red); font-size: var(--text-2xs); line-height: 1.4; }
  @media (hover: none) { .hierarchy-options button { min-height: 46px; } }
</style>
