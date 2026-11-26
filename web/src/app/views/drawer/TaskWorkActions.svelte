<script lang="ts">
  import { dependencyActionExplanation, dependencyBlocked } from '../../../lib/dependencies';
  import { actorId } from '../../../lib/state';
  import type { Task } from '../../../lib/types';
  import { runTaskAction } from '../../drawer/actions';
  import { drawer } from '../../state/drawer.svelte';
  import { live } from '../../state/live.svelte';
  import { session } from '../../state/session.svelte';
  import { claimAction, claimConflict, claimCountdown, claimExpiresSoon, claimExpiryExact, claimIsActive, claimOwnerLabel } from '../../taskQueries';

  let { task }: { task: Task } = $props();
  const blocked = $derived(dependencyBlocked(task));
  const busy = $derived(drawer.drawerSaving || drawer.taskActionLoading === task.id);
  const active = $derived(claimIsActive(task, live.pulseClock));
  const conflict = $derived(claimConflict(task, live.pulseClock));
  const expiring = $derived(claimExpiresSoon(task, live.pulseClock));
  const action = $derived(claimAction(task, live.pulseClock));
  const claimant = $derived(claimOwnerLabel(task));
  const ownsClaim = $derived(active && actorId(task.claimed_by) === session.user?.id);
</script>

{#if task.claimed_by}
  <div class="claim-lease" class:expired={!active} class:expiring-soon={expiring} class:conflict role={conflict || expiring ? 'alert' : undefined}>
    <span class="claim-lease-icon" aria-hidden="true">⚑</span>
    <span>
      <strong>{conflict ? `Claim conflict · ${claimant}` : active ? `Claimed by ${claimant}` : `Previous claim by ${claimant}`}</strong>
      {#if task.claim_expires_at}
        <small><time datetime={task.claim_expires_at}>{active ? 'Expires' : 'Expired'} {claimExpiryExact(task.claim_expires_at)}</time> · {claimCountdown(task, live.pulseClock)} · Task version v{task.version}</small>
      {:else}<small>No expiry reported · Task version v{task.version}</small>{/if}
      {#if expiring}<small class="claim-lease-warning">Expiring soon — renew this claim before it expires.</small>{/if}
    </span>
  </div>
{/if}

<div class="drawer-actions">
  <button class="button quiet-button" type="button"
    title={blocked ? dependencyActionExplanation(task, action === 'renew' ? 'renew this claim' : 'claim this task') : conflict ? `Claim held by ${claimant} · task version v${task.version}` : undefined}
    disabled={busy || blocked || conflict} onclick={() => runTaskAction(action)}>
    {ownsClaim ? '↻ Renew claim' : conflict ? `Claimed by ${claimant}` : '⚑ Claim task'}
  </button>
  {#if ownsClaim}
    <button class="button quiet-button" type="button" disabled={busy} onclick={() => runTaskAction('release')}>Release claim</button>
  {/if}
  <button class="button complete-button" type="button" title={blocked ? dependencyActionExplanation(task, 'complete this task') : undefined}
    disabled={busy || Boolean(task.completed_at) || blocked} onclick={() => runTaskAction('complete')}>
    {task.completed_at ? '✓ Completed' : '✓ Complete'}
  </button>
</div>
