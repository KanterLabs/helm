<script lang="ts">
  import { projectCapacity, projectCapacityAction } from '../../../lib/capacity';
  import type { Project } from '../../../lib/types';
  import { reviewActiveTasks } from '../../board/capacity';

  let { project }: { project: Project } = $props();
  const capacity = $derived(projectCapacity(project));
</script>

{#if capacity}
  <section class="project-capacity" class:full={capacity.count >= capacity.limit} aria-label="In Progress capacity">
    <div role="status">
      <strong>In Progress</strong>
      <span>{capacity.count} of {capacity.limit}</span>
      {#if capacity.count >= capacity.limit}
        <strong class="capacity-state">{capacity.count > capacity.limit ? 'Over limit' : 'Full'}</strong>
        <p>{projectCapacityAction(capacity.count, capacity.limit)}</p>
      {/if}
    </div>
    {#if capacity.count >= capacity.limit}
      <button class="button quiet-button" type="button" onclick={reviewActiveTasks}>Review In Progress tasks</button>
    {/if}
  </section>
{/if}

<style>
  .project-capacity {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px 16px;
    margin-bottom: 12px;
    color: var(--ink-soft);
    font-size: var(--text-xs);
  }
  .project-capacity.full {
    justify-content: space-between;
    padding: 9px 12px;
    border: 1px solid var(--border-strong);
    border-left: 3px solid var(--semantic-amber);
    border-radius: 8px;
    background: var(--surface);
  }
  strong, span { display: inline-block; margin-right: 6px; }
  .capacity-state {
    padding: 2px 7px;
    border-radius: 6px;
    color: var(--ink);
    background: var(--amber-soft);
  }
  p { margin: 3px 0 0; }
  button { min-height: 44px; }
</style>
