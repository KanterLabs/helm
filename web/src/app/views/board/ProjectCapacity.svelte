<script lang="ts">
  import { projectCapacity, projectCapacityAction } from '../../../lib/capacity';
  import type { Project } from '../../../lib/types';
  import { reviewActiveTasks } from '../../board/capacity';

  let { project, compact = false }: { project: Project; compact?: boolean } = $props();
  const capacity = $derived(projectCapacity(project));
</script>

{#if capacity && compact && capacity.count < capacity.limit}
  <section aria-label="In Progress capacity">
    <div role="status">In Progress · {capacity.count} of {capacity.limit}</div>
  </section>
{:else if capacity && !compact && capacity.count >= capacity.limit}
  <section class="project-capacity" aria-label="In Progress capacity">
    <div role="status">
      <strong>In Progress</strong>
      <span>{capacity.count} of {capacity.limit}</span>
      <strong class="capacity-state">{capacity.count > capacity.limit ? 'Over limit' : 'Full'}</strong>
      <p>{projectCapacityAction(capacity.count, capacity.limit)}</p>
    </div>
    <button class="button quiet-button" type="button" onclick={reviewActiveTasks}>Review In Progress tasks</button>
  </section>
{/if}

<style>
  .project-capacity {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: 8px 16px;
    margin-bottom: 12px;
    color: var(--ink-soft);
    font-size: var(--text-xs);
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
