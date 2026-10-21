<script lang="ts">
  // Read-only summary for agent progress and task change entries in the activity timeline.
  import type { TaskTimelineItem } from '../types';
  import { changeContext, changeLabel } from './taskActivityTimeline';

  export let item: TaskTimelineItem;
</script>

{#if item.kind === 'agent_progress' && item.progress}
  <p class="task-timeline-summary">{item.progress.summary}</p>
  <dl class="task-timeline-details">
    {#if item.progress.phase}<div><dt>Phase</dt><dd>{item.progress.phase}</dd></div>{/if}
    {#if item.progress.next_action}<div><dt>Next</dt><dd>{item.progress.next_action}</dd></div>{/if}
    {#if item.progress.checkpoint_total !== null && item.progress.checkpoint_total !== undefined}<div><dt>Checkpoints</dt><dd>{item.progress.checkpoint_completed ?? 0} of {item.progress.checkpoint_total}</dd></div>{/if}
  </dl>
{:else if item.kind === 'task_change' && item.change}
  <p class="task-timeline-summary">{changeLabel(item)}{#if changeContext(item)}<span> · {changeContext(item)}</span>{/if}</p>
{/if}
