<script lang="ts">
  import type { Task } from '../../../lib/types';
  import { live } from '../../state/live.svelte';
  import { activeTaskOwner } from '../../taskQueries';

  let { task, onCard = false }: { task: Task; onCard?: boolean } = $props();
  const owner = $derived(activeTaskOwner(task, live.pulseClock));
  const nextAction = $derived(task.next_action?.trim() || task.agent_work?.next_action?.trim());
  const checklist = $derived(task.checklist_summary);
</script>

<section class:on-card={onCard} class="active-work-summary" aria-label="Active task details">
  <dl>
    <div><dt>Owner</dt><dd>{owner}</dd></div>
    <div><dt>Next action</dt><dd>{nextAction || 'Not recorded'}</dd></div>
    <div>
      <dt>Finish condition</dt>
      <dd>{task.finish_condition?.trim() || (checklist?.total ? `Complete the checklist (${checklist.completed} of ${checklist.total} done)` : 'Not recorded')}</dd>
    </div>
  </dl>
</section>

<style>
  .active-work-summary {
    margin: 12px 0;
    padding: 9px 12px;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--surface-muted);
    color: var(--ink);
    font-size: var(--text-xs);
  }
  .on-card { margin: 4px 10px 8px; }
  dl { margin: 0; display: grid; gap: 6px; }
  dl > div { display: grid; grid-template-columns: minmax(84px, 1fr) minmax(0, 3fr); gap: 6px; }
  dt { color: var(--muted); font-weight: 600; }
  dd { margin: 0; overflow-wrap: anywhere; white-space: pre-line; }
</style>
