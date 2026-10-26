<svelte:options runes={true} />

<script lang="ts">
  // Task drawer shell with details and activity tabs.
  import AgentWorkPanel from '../../../lib/components/AgentWorkPanel.svelte';
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import TaskActivityTimeline from '../../../lib/components/TaskActivityTimeline.svelte';
  import TaskShareActions from '../../../lib/components/TaskShareActions.svelte';
  import TaskWatchToggle from '../../../lib/components/TaskWatchToggle.svelte';
  import { projectInitials } from '../../../lib/state';
  import { priorityLabels, severityLabels } from '../../constants';
  import { focusTrap } from '../../dialogs';
  import { confirmDrawerCommentDelete, deleteDrawerComment, editDrawerComment, postComment } from '../../drawer/actions';
  import { closeDrawer } from '../../drawer/drafts';
  import { drawerTaskShareUrl } from '../../drawer/open';
  import { deleteDrawerTask } from '../../drawer/save';
  import { drawerTabKeydown, loadDrawerTimeline, loadOlderDrawerActivity, setDrawerTimelineFilter, setDrawerView } from '../../drawer/timeline';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { drawer } from '../../state/drawer.svelte';
  import { live } from '../../state/live.svelte';
  import { session } from '../../state/session.svelte';
  import { agentLabelForTask, showAgentPulse, taskReleaseName } from '../../taskQueries';
  import DrawerDetails from './DrawerDetails.svelte';

  // Rendered only while drawer.drawerTask is set (guarded by the parent).
  const drawerTask = $derived(drawer.drawerTask!);
  // Rendered only while session.user is set (guarded by the parent).
  const signedInUser = $derived(session.user!);
</script>

{#if drawerTask}
  <div class="drawer-backdrop" role="presentation" on:click={() => closeDrawer()}></div>
  <div class="task-drawer" role="dialog" aria-modal="true" aria-label={`${drawerTask.key}: ${drawerTask.title}`} use:focusTrap>
    <PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} /><div class="drawer-focus-target sr-only" tabindex="-1" data-dialog-initial-focus aria-label="Task details"></div>
    <div class="drawer-header"><div><span class="drawer-key">{drawerTask.key}</span><span class="issue-kind-badge" class:task-kind={drawerTask.kind !== 'bug'}>{drawerTask.kind === 'bug' ? 'Bug' : 'Task'}</span>{#if drawerTask.kind === 'bug'}<span class:untriaged={!drawerTask.bug?.severity} class="severity-badge">{drawerTask.bug?.severity ? severityLabels[drawerTask.bug.severity] : 'Untriaged'}</span>{/if}<span class={`priority-pill priority-${drawerTask.priority}`}>{priorityLabels[drawerTask.priority]}</span>{#if taskReleaseName(drawerTask)}<span class="release-chip">{taskReleaseName(drawerTask)}</span>{/if}</div><TaskWatchToggle task={drawerTask} sessionKey={`${signedInUser.id}:${session.sessionGeneration}`} disabled={drawer.drawerSaving || drawer.drawerLoading} /><button class="icon-button" type="button" aria-label="Close task details" on:click={() => closeDrawer()}>×</button></div>
    {#if drawer.drawerLoading}<div class="drawer-loading"><span class="spinner"></span><span>Loading task details…</span></div>{/if}
    {#if drawer.drawerError}<div class="inline-alert error drawer-alert" role="alert"><span>!</span>{drawer.drawerError}</div>{/if}
    <div class="drawer-tabs" role="tablist" aria-label="Task views">
      <button class:active={drawer.drawerView === 'details'} id="drawer-details-tab" class="drawer-tab" type="button" role="tab" aria-selected={drawer.drawerView === 'details'} aria-controls="drawer-details-panel" tabindex={drawer.drawerView === 'details' ? 0 : -1} on:click={() => setDrawerView('details')} on:keydown={drawerTabKeydown}>Details</button>
      <button class:active={drawer.drawerView === 'activity'} id="drawer-activity-tab" class="drawer-tab" type="button" role="tab" aria-selected={drawer.drawerView === 'activity'} aria-controls="drawer-activity-panel" tabindex={drawer.drawerView === 'activity' ? 0 : -1} on:click={() => setDrawerView('activity')} on:keydown={drawerTabKeydown}>Activity</button>
    </div>
    <TaskShareActions taskKey={drawerTask.key} taskUrl={drawerTaskShareUrl(drawerTask)} />
    {#if drawer.drawerView === 'details'}
    <DrawerDetails />
    {:else}
      <div id="drawer-activity-panel" class="drawer-scroll drawer-activity-scroll" role="tabpanel" aria-labelledby="drawer-activity-tab">
        {#if showAgentPulse(drawerTask)}<AgentWorkPanel task={drawerTask} now={live.pulseClock} actorLabel={agentLabelForTask(drawerTask)} />{/if}
        <section class="drawer-section activity-section drawer-timeline-section">
          <form class="comment-form" on:submit|preventDefault={postComment}>
            <span class="avatar mini-user-avatar">{projectInitials({ name: signedInUser.name, key: signedInUser.name })}</span>
            <textarea rows="2" bind:value={drawer.commentBody} placeholder="Leave a note for your team…" aria-label="Comment"></textarea>
            <button class="icon-button comment-send" type="submit" disabled={!drawer.commentBody.trim() || drawer.commentSending} aria-label="Add comment">↑</button>
          </form>
          <TaskActivityTimeline
            bind:dirty={drawer.editedCommentDirty}
            bind:pendingWrites={drawer.editedCommentPending}
            items={drawer.drawerTimelineItems}
            filter={drawer.drawerTimelineFilter}
            loading={drawer.drawerTimelineLoading}
            loadingOlder={drawer.drawerTimelineLoadingOlder}
            error={drawer.drawerTimelineError}
            hasOlder={Boolean(drawer.drawerTimelineNextCursor)}
            onFilterChange={setDrawerTimelineFilter}
            onLoadOlder={loadOlderDrawerActivity}
            onRetry={() => { void loadDrawerTimeline(drawerTask?.id); }}
            currentActorId={signedInUser?.id || ''}
            canManageComments={Boolean(signedInUser?.admin)}
            taskId={drawerTask?.id || ''}
            onEditComment={editDrawerComment}
            onConfirmDelete={confirmDrawerCommentDelete}
            onDeleteComment={deleteDrawerComment}
          />
        </section>
      </div>
    {/if}
    <div class="drawer-delete-wrap"><button class="button danger-button" type="button" disabled={drawer.drawerSaving || drawer.taskActionLoading === drawerTask?.id} on:click={deleteDrawerTask}>Delete task</button></div>
  </div>
{/if}
