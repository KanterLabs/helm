<svelte:options runes={true} />

<script lang="ts">
  // Ordinary New task dialog: title first, then destination and optional
  // details. Task drafting with Luna is retired from this form (TC-318); its
  // dormant exports remain until TC-325 retires the shared runtime consumers.
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { focusTrap } from '../../dialogs';
  import { creation } from '../../state/creation.svelte';
  import { drawer } from '../../state/drawer.svelte';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { session } from '../../state/session.svelte';
  import { taskModal } from '../../state/taskModal.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { changeTaskModalProject, closeTaskModal, createGlobalTask } from '../../taskModal';

  const locked = $derived(taskModal.taskModalCreating || Boolean(creation.taskCreation));
  const parentTask = $derived(taskModal.taskModalParentId && drawer.drawerTask?.id === taskModal.taskModalParentId ? drawer.drawerTask : null);
  const plannedFocus = $derived((releasePlan.releasesByProject[taskModal.taskModalProjectId] || []).filter((release) => release.status === 'planned'));
  const canSubmit = $derived(!taskModal.taskModalCreating && (Boolean(creation.taskCreation) || (!taskModal.taskModalLoading && Boolean(taskModal.taskModalTitle.trim()) && taskModal.taskModalColumns.some((column) => column.id === taskModal.taskModalColumnId))));
</script>

{#if taskModal.showTaskModal}
  <div class="modal-backdrop" role="presentation" on:click={closeTaskModal}></div>
  <div class="modal task-create-modal" role="dialog" aria-modal="true" aria-labelledby="task-modal-title" use:focusTrap><PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} />
    <div class="modal-header"><div><span class="eyebrow">{parentTask ? 'Child task' : 'New task'}</span><h2 id="task-modal-title">Create a task</h2></div><button class="icon-button" type="button" aria-label="Close" disabled={locked} on:click={closeTaskModal}>×</button></div>
    {#if taskModal.taskModalError}<div class="inline-alert error" role="alert"><span>!</span>{taskModal.taskModalError}</div>{/if}
    <form aria-busy={taskModal.taskModalCreating} on:submit|preventDefault={createGlobalTask}><fieldset class="task-create-fields" disabled={locked}>
      <label class="task-title-field">Task title<input data-dialog-initial-focus autocomplete="off" bind:value={taskModal.taskModalTitle} placeholder="What needs doing?" /></label>
      <label>Description <span class="optional">Optional · Markdown supported</span><textarea rows="3" bind:value={taskModal.taskModalDescription} placeholder="Context, links or acceptance criteria."></textarea></label>
      {#if parentTask}<p class="task-parent-note">This task will be created as a child of <strong>{parentTask.key}</strong>.</p>{/if}
      <fieldset class="task-create-group"><legend>Destination</legend>
        <div class="task-create-grid"><label>Project<select bind:value={taskModal.taskModalProjectId} on:change={changeTaskModalProject}>{#each workspace.navProjects as project}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select></label><label>Column<select bind:value={taskModal.taskModalColumnId} disabled={taskModal.taskModalLoading || !taskModal.taskModalColumns.length}>{#each taskModal.taskModalColumns as column}<option value={column.id}>{column.name}</option>{/each}</select></label></div>
      </fieldset>
      <fieldset class="task-create-group"><legend>Details <span class="optional">Optional</span></legend>
        <div class="task-create-grid"><label>Priority<select bind:value={taskModal.taskModalPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label><label>Due date<input type="date" bind:value={taskModal.taskModalDueDate} /></label>
          <label>Assignee<input bind:value={taskModal.taskModalAssignee} placeholder="Actor ID" /></label><label>Focus<select aria-label="Focus" bind:value={taskModal.taskModalReleaseId}><option value="">No focus</option>{#each plannedFocus as release}<option value={release.id}>{release.name}</option>{/each}</select></label></div>
      </fieldset>
    </fieldset>{#if taskModal.taskModalCreating}<p role="status">Saving… Keep this form open while the result is confirmed.</p>{/if}<div class="modal-actions"><button class="text-button" type="button" disabled={locked} on:click={closeTaskModal}>Cancel</button><button class="button primary" type="submit" disabled={!canSubmit}>{#if taskModal.taskModalCreating}<span class="button-spinner"></span>{/if}{taskModal.taskModalCreating ? 'Saving…' : creation.taskCreation ? 'Retry original submission' : 'Create task'}</button></div>
    </form>
  </div>
{/if}
