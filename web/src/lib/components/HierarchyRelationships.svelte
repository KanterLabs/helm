<script lang="ts">
  import type { TaskHierarchy, TaskHierarchyReference } from '../types';

  export let hierarchy: TaskHierarchy | null = null;
  export let mutationBusy = false;
  export let parentRemoving = false;
  export let onNavigate: (reference: TaskHierarchyReference) => void | Promise<void> = () => undefined;
  export let onRemoveParent: () => void | Promise<void> = () => undefined;
  export let onRemoveChild: (reference: TaskHierarchyReference) => void | Promise<void> = () => undefined;
  export let onCreateChild: () => void | Promise<void> = () => undefined;

  let childrenHeading: HTMLHeadingElement;

  /** Restores focus after a child is removed from the list. */
  export function focusChildren() {
    childrenHeading?.focus();
  }

  function referenceLabel(reference: TaskHierarchyReference | null | undefined): string {
    return reference ? `${reference.key}: ${reference.title}` : 'parent task';
  }
</script>

<div class="hierarchy-sections">
  <section aria-labelledby="task-parent-heading">
    <h3 id="task-parent-heading">Parent</h3>
    {#if hierarchy?.parent}
      <div class="hierarchy-row">
        <button class="hierarchy-link" type="button" aria-label={`Open ${referenceLabel(hierarchy?.parent)}`} on:click={() => hierarchy?.parent && onNavigate(hierarchy.parent)}>
          <span class="hierarchy-key">{hierarchy?.parent?.key}</span>
          <span class="hierarchy-title">{hierarchy?.parent?.title}</span>
        </button>
        <button class="remove-hierarchy" type="button" disabled={mutationBusy} aria-label="Remove parent relationship" on:click={() => onRemoveParent()}>{parentRemoving ? '…' : '×'}</button>
      </div>
    {:else}
      <p class="hierarchy-empty">No parent linked.</p>
    {/if}
  </section>

  <section aria-labelledby="task-children-heading">
    <div class="hierarchy-subheading">
      <h3 bind:this={childrenHeading} id="task-children-heading" tabindex="-1">Children <span>{hierarchy?.children.length || 0}</span></h3>
      <button class="create-child" type="button" on:click={() => onCreateChild()}>Create child</button>
    </div>
    {#if hierarchy?.children.length}
      <ul class="hierarchy-list">
        {#each hierarchy.children as reference (reference.id)}
          <li>
            <button class="hierarchy-link" type="button" aria-label={`Open ${referenceLabel(reference)}`} on:click={() => onNavigate(reference)}>
              <span class="hierarchy-key">{reference.key}</span>
              <span class="hierarchy-title">{reference.title}</span>
              <span class:done={reference.semantic_state === 'completed'} class="hierarchy-state">{reference.semantic_state}</span>
            </button>
            <button class="remove-hierarchy" type="button" disabled={mutationBusy} aria-label={`Remove ${reference.key} as a child`} on:click={() => onRemoveChild(reference)}>{parentRemoving ? '…' : '×'}</button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="hierarchy-empty">No children yet.</p>
    {/if}
  </section>

  {#if hierarchy?.ancestors.length}
    <section aria-labelledby="task-ancestors-heading">
      <h3 id="task-ancestors-heading">Ancestors <span>{hierarchy.ancestors.length}</span></h3>
      <ul class="hierarchy-list compact-list">
        {#each hierarchy.ancestors as reference (reference.id)}
          <li><button class="hierarchy-link" type="button" aria-label={`Open ${referenceLabel(reference)}`} on:click={() => onNavigate(reference)}><span class="hierarchy-key">{reference.key}</span><span class="hierarchy-title">{reference.title}</span></button></li>
        {/each}
      </ul>
    </section>
  {/if}
</div>

<style>
  .hierarchy-subheading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .hierarchy-sections { display: grid; gap: 12px; }
  .hierarchy-sections section { min-width: 0; }
  .hierarchy-sections h3 { display: flex; align-items: center; gap: 5px; margin: 0 0 7px; color: var(--ink-soft); font-size: var(--text-2xs); }
  .hierarchy-sections h3 span { min-width: 18px; padding: 2px 5px; border-radius: 999px; color: var(--muted); background: var(--surface-muted); font-size: var(--text-2xs); text-align: center; }
  .hierarchy-list { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .hierarchy-list li, .hierarchy-row { display: grid; grid-template-columns: minmax(0, 1fr) 34px; gap: 4px; min-width: 0; }
  .compact-list li { grid-template-columns: minmax(0, 1fr); }
  .hierarchy-link { min-width: 0; min-height: 44px; display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 7px; padding: 7px 8px; border: 1px solid var(--border); border-radius: 8px; color: var(--ink); background: var(--surface); text-align: left; }
  .hierarchy-link:hover, .hierarchy-link:focus-visible { border-color: var(--purple); outline: none; }
  .hierarchy-key { color: var(--purple); font: 600 var(--text-2xs) var(--font-mono); }
  .hierarchy-title { min-width: 0; overflow: hidden; font-size: var(--text-2xs); font-weight: 600; text-overflow: ellipsis; white-space: nowrap; }
  .hierarchy-state { padding: 2px 5px; border-radius: 5px; color: var(--amber); background: var(--amber-soft); font-size: var(--text-2xs); font-weight: 600; }
  .hierarchy-state.done { color: var(--green); background: var(--green-soft); }
  .remove-hierarchy { min-width: 34px; min-height: 44px; border: 1px solid var(--border); border-radius: 8px; color: var(--muted); background: var(--surface); font-size: var(--text-md); }
  .remove-hierarchy:hover:not(:disabled), .remove-hierarchy:focus-visible { color: var(--red); border-color: var(--red); outline: none; }
  .remove-hierarchy:disabled { cursor: wait; opacity: .55; }
  .create-child { min-height: 32px; padding: 0 9px; border: 1px solid var(--border); border-radius: 7px; color: var(--purple); background: var(--surface); font-size: var(--text-2xs); font-weight: 600; }
  .create-child:hover, .create-child:focus-visible { border-color: var(--purple); outline: none; }
  .hierarchy-empty { min-height: 44px; display: flex; align-items: center; margin: 0; padding: 8px; border: 1px dashed var(--border); border-radius: 8px; color: var(--faint); font-size: var(--text-2xs); }
  @media (hover: none) { .hierarchy-link, .remove-hierarchy { min-height: 46px; } }
</style>
