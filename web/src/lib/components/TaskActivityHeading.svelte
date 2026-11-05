<script lang="ts">
  import type { TaskTimelineFilter } from '../types';
  import { filterOptions } from './taskActivityTimeline';
  export let filter: TaskTimelineFilter = 'all';
  export let onFilterChange: (next: TaskTimelineFilter) => void;
</script>

  <div class="task-timeline-heading">
    <div>
      <span class="eyebrow">Durable history</span>
      <h2 id="task-activity-heading">Activity</h2>
      <p>Agent updates, comments, and task changes in newest-first order.</p>
    </div>
    <div class="task-timeline-filters" role="group" aria-label="Filter task activity">
      {#each filterOptions as option}
        <button
          class:active={filter === option.value}
          class="task-timeline-filter"
          type="button"
          aria-pressed={filter === option.value}
          on:click={() => onFilterChange(option.value)}
        >{option.label}</button>
      {/each}
    </div>
  </div>
