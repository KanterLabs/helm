<svelte:options runes={true} />

<script lang="ts">
  // Explicit project grants for an ordinary member. An empty selection
  // means no projects at all, never "all projects".
  import type { Project } from '../../types';
  import { projectLabel } from './access';

  let { projects, selected = $bindable([]), hiddenGrantCount = 0, disabled = false }: {
    projects: Project[];
    selected: string[];
    /** Saved grants for projects this list cannot show; they are preserved. */
    hiddenGrantCount?: number;
    disabled?: boolean;
  } = $props();

  function toggle(id: string, checked: boolean) {
    selected = checked ? [...selected.filter((item) => item !== id), id] : selected.filter((item) => item !== id);
  }
</script>

<fieldset {disabled}>
  <legend>Project access</legend>
  {#if projects.length}
    <div class="scope-grid">
      {#each projects as project (project.id)}
        <label class="check-label"><input type="checkbox" checked={selected.includes(project.id)} onchange={(event) => toggle(project.id, event.currentTarget.checked)} /><span>{projectLabel(project)}</span></label>
      {/each}
    </div>
  {:else}
    <p class="mship-hint">No projects exist yet. Grant access after creating one.</p>
  {/if}
  {#if !selected.length && !hiddenGrantCount}
    <p class="mship-hint mship-empty-grants" role="note">No projects selected. This member will not see any project until you grant one.</p>
  {/if}
  {#if hiddenGrantCount}
    <p class="mship-hint">{hiddenGrantCount === 1 ? '1 saved grant is' : `${hiddenGrantCount} saved grants are`} for projects not listed here and will be kept.</p>
  {/if}
</fieldset>
