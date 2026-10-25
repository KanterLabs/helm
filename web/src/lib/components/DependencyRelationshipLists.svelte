<script lang="ts">
  import type { TaskDependencies, TaskReference } from '../types';
  import { dependencyStateLabel } from './dependencyRelations';

  export let relations: TaskDependencies | null = null;
  export let mutationBusy = false;
  export let removing: Record<string, boolean> = {};
  export let removeErrors: Record<string, string> = {};
  export let onNavigate: (reference: TaskReference) => void | Promise<void> = () => undefined;
  export let onRemove: (reference: TaskReference) => void | Promise<void> = () => undefined;
</script>

<div class="relationship-grid">
  <section aria-labelledby="waiting-on-heading">
    <h3 id="waiting-on-heading">Waiting on <span>{relations?.prerequisites.length || 0}</span></h3>
    {#if relations?.prerequisites.length}
      <ul class="relationship-list">
        {#each relations.prerequisites as reference (reference.id)}
          <li>
            <button class="relationship-link" type="button" aria-label={`Open ${reference.key}: ${reference.title}`} on:click={() => onNavigate(reference)}>
              <span class="relationship-key">{reference.key}</span>
              <span class="relationship-title">{reference.title}</span>
              <span class:done={reference.satisfied} class="relationship-state">{dependencyStateLabel(reference)}</span>
            </button>
            <button
              class="remove-relationship"
              type="button"
              aria-label={`Remove ${reference.key} as a prerequisite`}
              disabled={mutationBusy}
              on:click={() => onRemove(reference)}
            >{removing[reference.id] ? '…' : '×'}</button>
            {#if removeErrors[reference.id]}<p class="relationship-error" role="alert">{removeErrors[reference.id]}</p>{/if}
          </li>
        {/each}
      </ul>
    {:else}
      <p class="relationship-empty">No prerequisites yet.</p>
    {/if}
  </section>

  <section aria-labelledby="blocking-heading">
    <h3 id="blocking-heading">Blocking <span>{relations?.dependents.length || 0}</span></h3>
    {#if relations?.dependents.length}
      <ul class="relationship-list dependent-list">
        {#each relations.dependents as reference (reference.id)}
          <li>
            <button class="relationship-link" type="button" aria-label={`Open ${reference.key}: ${reference.title}`} on:click={() => onNavigate(reference)}>
              <span class="relationship-key">{reference.key}</span>
              <span class="relationship-title">{reference.title}</span>
              <span class:done={reference.satisfied} class="relationship-state">{dependencyStateLabel(reference)}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="relationship-empty">No tasks depend on this one.</p>
    {/if}
  </section>
</div>

<style>
  .relationship-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
  .relationship-grid section { min-width: 0; }
  .relationship-grid h3 { display: flex; align-items: center; gap: 5px; margin: 0 0 7px; color: var(--ink-soft); font-size: var(--text-xs); }
  .relationship-grid h3 span { min-width: 18px; padding: 2px 5px; border-radius: 999px; color: var(--muted); background: var(--surface-muted); font-size: var(--text-xs); text-align: center; }
  .relationship-list { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .relationship-list li { position: relative; display: grid; grid-template-columns: minmax(0, 1fr) 34px; gap: 4px; min-width: 0; }
  .dependent-list li { grid-template-columns: minmax(0, 1fr); }
  .relationship-link { min-width: 0; min-height: 44px; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 7px; padding: 7px 8px; border: 1px solid var(--border); border-radius: 8px; color: var(--ink); background: var(--surface); text-align: left; }
  .relationship-link:hover, .relationship-link:focus-visible { border-color: var(--purple); outline: none; }
  .relationship-key { color: var(--purple); font: 600 var(--text-xs) var(--font-mono); }
  .relationship-title { min-width: 0; overflow: hidden; font-size: var(--text-xs); font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
  .relationship-state { padding: 2px 5px; border-radius: 5px; color: var(--amber); background: var(--amber-soft); font-size: var(--text-xs); font-weight: 600; }
  .relationship-state.done { color: var(--green); background: var(--green-soft); }
  .remove-relationship { min-width: 34px; min-height: 44px; border: 1px solid var(--border); border-radius: 8px; color: var(--muted); background: var(--surface); font-size: var(--text-md); }
  .remove-relationship:hover:not(:disabled), .remove-relationship:focus-visible { color: var(--red); border-color: var(--red); outline: none; }
  .remove-relationship:disabled { cursor: wait; opacity: .55; }
  .relationship-empty { min-height: 44px; display: flex; align-items: center; margin: 0; padding: 8px; border: 1px dashed var(--border); border-radius: 8px; color: var(--faint); font-size: var(--text-xs); }
  .relationship-error { grid-column: 1 / -1; margin: 0; color: var(--red); font-size: var(--text-xs); line-height: 1.4; }
  @media (max-width: 520px) {
    .relationship-grid { grid-template-columns: 1fr; }
  }
  @media (hover: none) {
    .relationship-link, .remove-relationship { min-height: 46px; }
  }
</style>
