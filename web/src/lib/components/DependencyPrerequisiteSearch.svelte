<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import type { Task, TaskDependencies } from '../types';
  import { dependencyCandidates, nextDependencyOptionIndex } from './dependencyRelations';

  export let task: Task;
  export let relations: TaskDependencies | null = null;
  export let projectTasks: Task[] = [];
  export let pendingAdd = false;
  export let candidatesLoading = false;
  export let addError = '';
  /** Adds the chosen prerequisite; resolves true when the search should reset. */
  export let onSelect: (candidate: Task) => Promise<boolean> = async () => false;

  let query = '';
  let comboboxOpen = false;
  let activeIndex = -1;
  let searchInput: HTMLInputElement;
  let blurTimer: ReturnType<typeof setTimeout> | undefined;

  $: matches = dependencyCandidates(projectTasks, task.id, relations, query);
  $: if (activeIndex >= matches.length) activeIndex = matches.length ? matches.length - 1 : -1;
  $: activeOptionId = activeIndex >= 0 && matches[activeIndex]
    ? `dependency-option-${matches[activeIndex].id}`
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
    // The parent re-enables the control before resolving; disabled inputs
    // cannot receive focus in browsers or assistive-technology test environments.
    await tick();
    searchInput?.focus();
  }

  function handleComboboxKeydown(event: KeyboardEvent) {
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault();
      comboboxOpen = true;
      activeIndex = nextDependencyOptionIndex(activeIndex, event.key, matches.length);
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

<form class="dependency-add" on:submit|preventDefault={() => activeIndex >= 0 && matches[activeIndex] && choose(matches[activeIndex])}>
  <label for="dependency-search">Add prerequisite</label>
  <div class="combobox-wrap">
    <input
      bind:this={searchInput}
      id="dependency-search"
      role="combobox"
      aria-autocomplete="list"
      aria-expanded={comboboxOpen && Boolean(query.trim())}
      aria-controls="dependency-options"
      aria-activedescendant={activeOptionId}
      autocomplete="off"
      placeholder="Search this project by key or title"
      disabled={pendingAdd || candidatesLoading}
      bind:value={query}
      on:focus={() => comboboxOpen = true}
      on:input={() => { comboboxOpen = true; activeIndex = matches.length ? 0 : -1; addError = ''; }}
      on:keydown={handleComboboxKeydown}
      on:blur={deferComboboxClose}
    />
    {#if pendingAdd}<span class="add-spinner" aria-hidden="true"></span>{/if}
    {#if comboboxOpen && query.trim()}
      <div id="dependency-options" class="dependency-options" role="listbox" aria-label="Project tasks">
        {#if matches.length}
          {#each matches as candidate, index (candidate.id)}
            <button
              id={`dependency-option-${candidate.id}`}
              class:active={index === activeIndex}
              type="button"
              role="option"
              aria-selected={index === activeIndex}
              tabindex="-1"
              on:mouseenter={() => activeIndex = index}
              on:mousedown|preventDefault
              on:click={() => choose(candidate)}
            >
              <span>{candidate.key}</span>
              <strong>{candidate.title}</strong>
              {#if candidate.completed_at}<small>Done</small>{/if}
            </button>
          {/each}
        {:else}
          <p>No available tasks match “{query.trim()}”.</p>
        {/if}
      </div>
    {/if}
  </div>
  <small>Current task, existing links, and direct dependents are excluded.</small>
  {#if candidatesLoading}<p class="dependency-note" role="status">Loading project tasks…</p>{/if}
  {#if addError}<p class="relationship-error add-error" role="alert">{addError}</p>{/if}
</form>

<style>
  .dependency-add { display: grid; gap: 5px; padding-top: 12px; border-top: 1px solid var(--border); }
  .dependency-add > label { color: var(--ink-soft); font-size: var(--text-xs); font-weight: 600; }
  .dependency-add > small { color: var(--faint); font-size: var(--text-xs); }
  .combobox-wrap { position: relative; }
  .combobox-wrap > input { width: 100%; min-height: 44px; padding-right: 34px; }
  .dependency-options { position: absolute; top: calc(100% + 4px); right: 0; left: 0; z-index: 8; max-height: 225px; overflow: auto; padding: 5px; border: 1px solid var(--border-strong); border-radius: 9px; background: var(--surface-raised); box-shadow: var(--shadow-md); }
  .dependency-options button { width: 100%; min-height: 44px; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 8px; padding: 7px 8px; border: 0; border-radius: 6px; color: var(--ink); background: transparent; text-align: left; }
  .dependency-options button:hover, .dependency-options button.active { background: var(--purple-soft); }
  .dependency-options button span { color: var(--purple); font: 600 var(--text-xs) var(--font-mono); }
  .dependency-options button strong { min-width: 0; overflow: hidden; font-size: var(--text-xs); text-overflow: ellipsis; white-space: nowrap; }
  .dependency-options button small { color: var(--green); font-size: var(--text-xs); font-weight: 600; }
  .dependency-options > p { margin: 0; padding: 10px; color: var(--muted); font-size: var(--text-xs); }
  .add-spinner { position: absolute; top: 15px; right: 11px; width: 13px; height: 13px; border: 2px solid var(--border); border-top-color: var(--purple); border-radius: 50%; animation: spin .7s linear infinite; }
  .dependency-note { margin: 0; color: var(--muted); font-size: var(--text-xs); }
  .relationship-error { grid-column: 1 / -1; margin: 0; color: var(--red); font-size: var(--text-xs); line-height: 1.4; }
  .add-error { margin-top: 2px; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (hover: none) {
    .dependency-options button { min-height: 46px; }
  }
  @media (prefers-reduced-motion: reduce) {
    .add-spinner { animation: none; }
  }
</style>
