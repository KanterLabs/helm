<svelte:options runes={true} />

<script lang="ts">
  // Task drawer details panel.
  import AgentNotes from '../../../lib/components/AgentNotes.svelte';
  import AgentWorkPanel from '../../../lib/components/AgentWorkPanel.svelte';
  import AlertSourcePanel from '../../../lib/components/AlertSourcePanel.svelte';
  import TaskChecklist from '../../../lib/components/TaskChecklist.svelte';
  import TaskDependencies from '../../../lib/components/TaskDependencies.svelte';
  import TaskDependencyStatus from '../../../lib/components/TaskDependencyStatus.svelte';
  import TaskHierarchy from '../../../lib/components/TaskHierarchy.svelte';
  import TaskPullRequests from '../../../lib/components/github/TaskPullRequests.svelte';
  import { dependencyActionExplanation, dependencyBlocked } from '../../../lib/dependencies';
  import { actorId, actorName, formatRelative } from '../../../lib/state';
  import { resolutionLabels, resolutionOptions, severityLabels } from '../../constants';
  import { openBlockReason, reopenBug, resolveBug, runTaskAction, triageBug } from '../../drawer/actions';
  import { createChildTask, handleChecklistTaskUpdated, handleDependencyTaskUpdated, handleHierarchyTaskUpdated, openDependencyTask, openHierarchyTask, refreshDependencyTask, refreshHierarchyTask } from '../../drawer/open';
  import { deleteProjectLabel, saveTask } from '../../drawer/save';
  import { board } from '../../state/board.svelte';
  import { drawer } from '../../state/drawer.svelte';
  import { live } from '../../state/live.svelte';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { session } from '../../state/session.svelte';
  import { agentLabelForTask, claimAction, claimConflict, claimCountdown, claimExpiresSoon, claimExpiryExact, claimIsActive, claimOwnerLabel, projectForTask, showAgentPulse } from '../../taskQueries';

  // Rendered only while drawer.drawerTask is set (guarded by the parent).
  const drawerTask = $derived(drawer.drawerTask!);
</script>

<div id="drawer-details-panel" class="drawer-details-panel" role="tabpanel" aria-labelledby="drawer-details-tab">
  <div class="drawer-scroll" data-drawer-scroll>
{#if drawerTask.kind === 'bug'}
  <div class="drawer-bug-controls">
    <section class="drawer-section bug-details-section" aria-labelledby="bug-details-heading"><div class="section-heading-inline"><h2 id="bug-details-heading">Bug report</h2><span class="optional">Reporter: {drawerTask.bug?.reporter_id || 'Unknown'}</span></div><label>Actual behavior<textarea rows="3" bind:value={drawer.draftBugActual} placeholder="What happened?"></textarea></label><label>Expected behavior<textarea rows="3" bind:value={drawer.draftBugExpected} placeholder="What should have happened?"></textarea></label><label>Reproduction steps<textarea rows="3" bind:value={drawer.draftBugReproduction} placeholder="1. Open…&#10;2. Click…"></textarea></label><div class="drawer-field-grid"><label>Environment<input bind:value={drawer.draftBugEnvironment} placeholder="Browser, OS, device" /></label><label>Affected version<input bind:value={drawer.draftBugVersion} placeholder="e.g. 1.4.0" /></label></div></section>
    <section class="drawer-section bug-triage-section" aria-labelledby="bug-triage-heading"><div class="section-heading-inline"><h2 id="bug-triage-heading">Triage</h2><span class="optional">Set severity and ownership</span></div><div class="drawer-field-grid"><label>Severity<select aria-label="Bug severity" bind:value={drawer.triageSeverityDraft}><option value="s1">{severityLabels.s1}</option><option value="s2">{severityLabels.s2}</option><option value="s3">{severityLabels.s3}</option><option value="s4">{severityLabels.s4}</option></select></label><label>Priority<select aria-label="Triage priority" bind:value={drawer.draftPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label></div><label>Assignee<input aria-label="Triage assignee" bind:value={drawer.draftAssignee} placeholder="Actor ID (optional)" /></label><button class="button primary" type="button" title={dependencyBlocked(drawerTask) ? dependencyActionExplanation(drawerTask, 'start triage') : undefined} disabled={dependencyBlocked(drawerTask) || drawer.drawerSaving || drawer.taskActionLoading === drawerTask.id} on:click={triageBug}>{#if drawer.taskActionLoading === drawerTask.id}<span class="button-spinner"></span>{/if}{board.columns.some(column => column.id === drawerTask.column_id && column.semantic_state === 'backlog') ? 'Triage & move to Ready' : drawerTask.bug?.severity ? 'Update triage' : 'Triage issue'}</button></section>
    {#if drawerTask.bug?.resolution}
      <section class="drawer-section bug-resolution-section" aria-labelledby="bug-resolution-heading"><div class="section-heading-inline"><h2 id="bug-resolution-heading">Resolved as {resolutionLabels[drawerTask.bug.resolution] || drawerTask.bug.resolution}</h2><span class="optional">Reopen if the issue persists</span></div><label>Reopen reason<textarea rows="2" bind:value={drawer.reopenReasonDraft} placeholder="Why does this need another look?"></textarea></label><button class="button quiet-button" type="button" disabled={drawer.drawerSaving || drawer.taskActionLoading === drawerTask.id || !drawer.reopenReasonDraft.trim()} on:click={reopenBug}>Reopen issue</button></section>
    {:else}
      <section class="drawer-section bug-resolution-section" aria-labelledby="bug-resolution-heading"><div class="section-heading-inline"><h2 id="bug-resolution-heading">Resolve</h2><span class="optional">Close the loop for reporters</span></div><label>Resolution<select aria-label="Bug resolution" bind:value={drawer.resolutionDraft}>{#each resolutionOptions as resolution}<option value={resolution}>{resolutionLabels[resolution]}</option>{/each}</select></label>{#if drawer.resolutionDraft === 'duplicate'}<label>Duplicate of<input aria-label="Duplicate issue" bind:value={drawer.duplicateOfDraft} placeholder="Task key or ID" /></label>{/if}<label>Resolution note <span class="optional">Optional</span><textarea rows="2" bind:value={drawer.resolutionNoteDraft} placeholder="What changed or why was this closed?"></textarea></label><button class="button complete-button" type="button" title={dependencyBlocked(drawerTask) ? dependencyActionExplanation(drawerTask, 'resolve this issue') : undefined} disabled={dependencyBlocked(drawerTask) || drawer.drawerSaving || drawer.taskActionLoading === drawerTask.id} on:click={resolveBug}>Resolve issue</button></section>
    {/if}
  </div>
{/if}
{#if drawerTask.claimed_by}
  <div class="claim-lease" class:expired={!claimIsActive(drawerTask, live.pulseClock)} class:expiring-soon={claimExpiresSoon(drawerTask, live.pulseClock)} class:conflict={claimConflict(drawerTask, live.pulseClock)} role={claimConflict(drawerTask, live.pulseClock) || claimExpiresSoon(drawerTask, live.pulseClock) ? 'alert' : undefined}>
    <span class="claim-lease-icon" aria-hidden="true">⚑</span>
    <span><strong>{claimConflict(drawerTask, live.pulseClock) ? `Claim conflict · ${claimOwnerLabel(drawerTask)}` : `Claimed by ${claimOwnerLabel(drawerTask)}`}</strong>{#if drawerTask.claim_expires_at}<small><time datetime={drawerTask.claim_expires_at}>{claimIsActive(drawerTask, live.pulseClock) ? `Expires ${claimExpiryExact(drawerTask.claim_expires_at)}` : `Expired ${claimExpiryExact(drawerTask.claim_expires_at)}`}</time> · {claimCountdown(drawerTask, live.pulseClock)} · Task version v{drawerTask.version}</small>{:else}<small>No expiry reported · Task version v{drawerTask.version}</small>{/if}{#if claimExpiresSoon(drawerTask, live.pulseClock)}<small class="claim-lease-warning">Expiring soon — renew this claim before it expires.</small>{/if}</span>
  </div>
{/if}
<button class="button quiet-button block-button" type="button" disabled={Boolean(drawerTask.completed_at) || drawer.drawerSaving || drawer.taskActionLoading === drawerTask.id} on:click={openBlockReason}>■ Block</button>
{#if drawer.blockReasonOpen}
  <section class="block-reason-form" aria-labelledby="block-reason-heading"><label id="block-reason-heading">Why is this task blocked?<textarea rows="3" bind:value={drawer.blockReasonDraft} placeholder="Describe the dependency or decision needed." required></textarea></label><div class="form-actions"><button class="text-button" type="button" on:click={() => { drawer.blockReasonOpen = false; drawer.blockReasonDraft = ''; }}>Cancel</button><button class="button danger-button" type="button" disabled={!drawer.blockReasonDraft.trim() || drawer.drawerSaving || drawer.taskActionLoading === drawerTask.id} on:click={() => runTaskAction('block', drawer.blockReasonDraft)}>Block task</button></div></section>
{/if}
<TaskDependencyStatus task={drawerTask} mode="notice" />
      <label class="drawer-title-label"><span class="sr-only">Task title</span><input id="drawer-title" class="drawer-title-input" bind:value={drawer.draftTitle} /></label><div class="drawer-meta"><span class="task-project-marker" style={`--project-color: ${projectForTask(drawerTask)?.color || '#6d5efc'}`}></span><span>{projectForTask(drawerTask)?.name || 'Project'}</span><span>·</span><span>Updated {formatRelative(drawerTask?.updated_at)}</span></div><div class="drawer-actions"><button class="button quiet-button" type="button" title={dependencyBlocked(drawerTask) ? dependencyActionExplanation(drawerTask, claimAction(drawerTask) === 'renew' ? 'renew this claim' : 'claim this task') : claimConflict(drawerTask, live.pulseClock) ? `Claim held by ${claimOwnerLabel(drawerTask)} · task version v${drawerTask.version}` : undefined} disabled={drawer.drawerSaving || drawer.taskActionLoading === drawerTask?.id || dependencyBlocked(drawerTask) || claimConflict(drawerTask, live.pulseClock)} on:click={() => runTaskAction(claimAction(drawerTask))}>{drawerTask.claimed_by && actorId(drawerTask.claimed_by) === session.user?.id && claimIsActive(drawerTask, live.pulseClock) ? '↻ Renew claim' : drawerTask.claimed_by && claimConflict(drawerTask, live.pulseClock) ? `Claimed by ${actorName(drawerTask.claimed_by) || 'agent'}` : '⚑ Claim task'}</button>{#if drawerTask.claimed_by && actorId(drawerTask.claimed_by) === session.user?.id && claimIsActive(drawerTask, live.pulseClock)}<button class="button quiet-button" type="button" disabled={drawer.drawerSaving || drawer.taskActionLoading === drawerTask?.id} on:click={() => runTaskAction('release')}>Release claim</button>{/if}<button class="button complete-button" type="button" title={dependencyBlocked(drawerTask) ? dependencyActionExplanation(drawerTask, 'complete this task') : undefined} disabled={drawer.drawerSaving || Boolean(drawerTask.completed_at) || dependencyBlocked(drawerTask) || drawer.taskActionLoading === drawerTask.id} on:click={() => runTaskAction('complete')}>{drawerTask.completed_at ? '✓ Completed' : '✓ Complete'}</button></div>{#if showAgentPulse(drawerTask)}<AgentWorkPanel task={drawerTask} now={live.pulseClock} actorLabel={agentLabelForTask(drawerTask)} />{/if}<section class="drawer-section"><div class="drawer-field-grid"><label>Priority<select bind:value={drawer.draftPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label><label>Due date<input type="date" bind:value={drawer.draftDueDate} /></label></div><label>Assignee<input bind:value={drawer.draftAssignee} placeholder="Actor ID (optional)" /></label><label>Focus <span class="optional">Optional</span><select aria-label="Focus" bind:value={drawer.draftReleaseId}><option value="">No focus</option>{#each releasePlan.releasesByProject[drawerTask.project_id] || [] as release}<option value={release.id} disabled={release.status === 'released' && release.id !== drawer.draftReleaseId}>{release.name}{release.status === 'released' ? ' · Completed' : ''}</option>{/each}</select></label><label>Labels <span class="optional">Comma separated</span><input bind:value={drawer.draftLabels} placeholder="frontend, design" /></label>{#if board.labels.filter((label) => label.project_id === drawerTask?.project_id).length}<div class="drawer-label-picker"><span class="optional">Project labels</span><div class="drawer-label-options">{#each board.labels.filter((label) => label.project_id === drawerTask?.project_id) as label (label.id)}<span class="drawer-label-option" style={`--label-color: ${label.color || '#8b7cf6'}`}><span>{label.name}</span><button class="icon-button tiny danger-button" type="button" aria-label={`Delete label ${label.name}`} disabled={drawer.labelDeleting === label.id} on:click|stopPropagation={() => deleteProjectLabel(label)}>×</button></span>{/each}</div></div>{/if}</section>
        {#if drawerTask.alert_source}<AlertSourcePanel source={drawerTask.alert_source} onOpenPrevious={(id) => openDependencyTask({ id, key: '', title: '', completed_at: null, satisfied: true })} />{/if}
        <TaskPullRequests task={drawerTask} columnName={board.columns.find((column) => column.id === drawerTask.column_id)?.name || ''} onTaskUpdated={handleDependencyTaskUpdated} onRefreshTask={refreshDependencyTask} />
        <TaskDependencies
          bind:this={drawer.drawerDependencyPanel}
          task={drawerTask}
          refreshToken={drawer.drawerDependencyRefresh}
          onTaskUpdated={handleDependencyTaskUpdated}
          onNavigate={openDependencyTask}
          onRefreshTask={refreshDependencyTask}
        />
	                <TaskChecklist
          bind:dirty={drawer.checklistDirty}
          bind:pendingWrites={drawer.checklistPending}
	                  task={drawerTask}
	                  onTaskUpdated={handleChecklistTaskUpdated}
	                  onRefreshTask={refreshDependencyTask}
	                />
	                <TaskHierarchy
	                  bind:this={drawer.drawerHierarchyPanel}
	                  task={drawerTask}
	                  refreshToken={drawer.drawerHierarchyRefresh}
	                  onTaskUpdated={handleHierarchyTaskUpdated}
	                  onNavigate={openHierarchyTask}
	                  onRefreshTask={refreshHierarchyTask}
	                  onCreateChild={createChildTask}
	                />
	                <section class="drawer-section description-section"><div class="section-heading-inline"><h2>Description</h2><span class="markdown-hint">Markdown supported</span></div><textarea class="description-input" rows="7" bind:value={drawer.draftDescription} placeholder="What does success look like?"></textarea></section>
          <AgentNotes task={drawerTask} currentActorId={session.user?.id || ''} canManage={Boolean(session.user?.admin)} />
          <div class="drawer-save-bar" class:dirty={drawer.drawerDraftDirty} aria-live="polite"><span class="drawer-save-status">{#if drawer.drawerSaving}<span class="button-spinner"></span>Saving changes…{:else if drawer.drawerTaskDraftDirty}<span class="drawer-status-dot"></span>Unsaved changes{:else if drawer.drawerDraftDirty}<span class="drawer-status-dot"></span>Action draft pending{:else}<span class="drawer-status-check">✓</span>All changes saved{/if}</span><button class="button primary save-task-button" type="button" disabled={drawer.drawerSaving || !drawer.drawerTaskDraftDirty || !drawer.draftTitle.trim()} on:click={() => void saveTask()}>{#if drawer.drawerSaving}<span class="button-spinner"></span>{/if}{drawer.drawerTaskDraftDirty ? 'Save changes' : 'Saved'}</button></div>
	          </div>
</div>
