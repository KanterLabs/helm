<svelte:options runes={true} />

<script lang="ts">
  // New task dialog with Luna assistance.
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import { taskDeepLink } from '../../../lib/state';
  import { priorityLabels } from '../../constants';
  import { focusTrap } from '../../dialogs';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { cancelCodexLogin, connectCodex, copyCodexCode } from '../../settings';
  import { creation } from '../../state/creation.svelte';
  import { drawer } from '../../state/drawer.svelte';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { session } from '../../state/session.svelte';
  import { settings } from '../../state/settings.svelte';
  import { taskModal } from '../../state/taskModal.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { applyAllLunaFields, applyLunaField, assistTaskWithLuna, cancelTaskAssist, changeTaskModalProject, closeTaskModal, createGlobalTask } from '../../taskModal';
</script>

{#if taskModal.showTaskModal}
  <div class="modal-backdrop" role="presentation" on:click={closeTaskModal}></div>
  <div class="modal task-create-modal" role="dialog" aria-modal="true" aria-labelledby="task-modal-title" use:focusTrap><PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} />
    <div class="modal-header"><div><span class="eyebrow">Capture an idea</span><h2 id="task-modal-title">Create a task</h2></div><button class="icon-button" type="button" aria-label="Close" disabled={taskModal.taskModalCreating || Boolean(creation.taskCreation)} on:click={closeTaskModal}>×</button></div>
    {#if taskModal.taskModalError}<div class="inline-alert error" role="alert"><span>!</span>{taskModal.taskModalError}</div>{/if}
		<form aria-busy={taskModal.taskModalCreating} on:submit|preventDefault={createGlobalTask}><fieldset disabled={taskModal.taskModalCreating || Boolean(creation.taskCreation)} style="border: 0; padding: 0; margin: 0; min-width: 0;">
		  <div class="form-row task-destination-row"><label>Project<select bind:value={taskModal.taskModalProjectId} on:change={changeTaskModalProject}>{#each workspace.navProjects as project}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select></label><label>Column<select bind:value={taskModal.taskModalColumnId} disabled={taskModal.taskModalLoading || !taskModal.taskModalColumns.length}>{#each taskModal.taskModalColumns as column}<option value={column.id}>{column.name}</option>{/each}</select></label></div>
		  {#if taskModal.taskModalParentId && drawer.drawerTask?.id === taskModal.taskModalParentId}<p class="task-parent-note">This task will be created as a child of <strong>{drawer.drawerTask.key}</strong>.</p>{/if}
		  <section class="luna-assist-panel" aria-labelledby="luna-assist-heading" aria-describedby="luna-assist-description">
		    <div class="luna-assist-heading"><div><span class="eyebrow">Optional assist</span><h3 id="luna-assist-heading">Plan it with Luna</h3><p id="luna-assist-description">Describe the outcome and Luna will suggest the task details.</p></div><span class="luna-mark" aria-hidden="true">✦</span></div>
        <label>Rough idea<textarea data-dialog-initial-focus rows="2" bind:value={taskModal.taskModalIdea} placeholder="e.g. Let self-hosted users connect their own Codex subscription"></textarea></label>
        {#if taskModal.taskModalAssisting}<div class="luna-progress" aria-live="polite"><span class="button-spinner"></span><span>{taskModal.taskModalAssistStage}</span><a class="text-button" href="/settings" target="_blank" rel="noopener noreferrer">View debug steps ↗</a><button class="text-button" type="button" on:click={cancelTaskAssist}>Cancel</button></div>
        {:else}<button class="button luna-button" type="button" disabled={!taskModal.taskModalIdea.trim() && !taskModal.taskModalTitle.trim()} on:click={assistTaskWithLuna}>✦ {taskModal.taskModalSuggestion ? 'Try again with Luna' : 'Assist with Luna'}</button>{/if}
        {#if taskModal.taskModalNeedsCodex}
          <div class="luna-connect" role="status"><div><strong>Connect your Codex subscription</strong><p>This draft stays here while you finish device-code login.</p></div>{#if settings.codexLogin}<div class="codex-code-row"><code>{settings.codexLogin.user_code}</code><button class="button quiet-button compact-button" type="button" on:click={copyCodexCode}>Copy</button></div><div class="form-actions"><button class="text-button" type="button" on:click={cancelCodexLogin}>Cancel login</button><a class="button primary" href={settings.codexLogin.verification_url} target="_blank" rel="noreferrer">Open ChatGPT ↗</a></div>{:else}<button class="button primary" type="button" disabled={settings.codexLoading} on:click={connectCodex}>{#if settings.codexLoading}<span class="button-spinner"></span>{/if}Connect Codex</button>{/if}</div>
        {/if}
        {#if taskModal.taskModalSuggestion}
          <div class="luna-preview" class:collapsed={taskModal.taskModalSuggestionCollapsed}>
            <div class="luna-preview-header"><div><span class="eyebrow">Suggestion ready</span><h4>{taskModal.taskModalSuggestionCollapsed && taskModal.taskModalAllFieldsApplied ? 'Suggestion applied' : 'Review before applying'}</h4></div><div class="luna-preview-actions"><button class="button primary compact-button" type="button" disabled={taskModal.taskModalAllFieldsApplied} on:click={applyAllLunaFields}>{taskModal.taskModalAllFieldsApplied ? '✓ All applied' : taskModal.taskModalHasAppliedAll ? 'Reapply all' : 'Apply all'}</button><button class="text-button luna-review-toggle" type="button" aria-expanded={!taskModal.taskModalSuggestionCollapsed} aria-controls="luna-suggestion-details" on:click={() => taskModal.taskModalSuggestionCollapsed = !taskModal.taskModalSuggestionCollapsed}>{taskModal.taskModalSuggestionCollapsed ? 'Review suggestion' : 'Hide suggestion'}</button></div></div>
            {#if taskModal.taskModalApplyNotice}<div class="luna-feedback" class:luna-apply-success={taskModal.taskModalAllFieldsApplied} role="status" aria-live="polite"><span class="luna-feedback-icon" aria-hidden="true">{taskModal.taskModalAllFieldsApplied ? '✓' : 'i'}</span><span>{taskModal.taskModalHasAppliedAll && !taskModal.taskModalAllFieldsApplied ? 'Task details changed after applying. Review the suggestion or reapply all.' : taskModal.taskModalApplyNotice}</span></div>{/if}
            <div id="luna-suggestion-details" class="luna-suggestion-details" hidden={taskModal.taskModalSuggestionCollapsed}>
              <div class="luna-preview-field" data-luna-field="title"><span><strong>Title</strong>{taskModal.taskModalSuggestion.title}</span><button class="text-button luna-apply-button" class:applied={taskModal.taskModalTitleApplied} type="button" disabled={taskModal.taskModalTitleApplied} aria-label={taskModal.taskModalTitleApplied ? 'Title suggestion applied' : 'Apply title suggestion'} on:click={() => applyLunaField('title')}>{taskModal.taskModalTitleApplied ? '✓ Applied' : 'Apply'}</button></div>
              <div class="luna-preview-field" data-luna-field="description"><span><strong>Description &amp; acceptance criteria</strong>{taskModal.taskModalSuggestion.description}<small>{taskModal.taskModalSuggestion.acceptance_criteria.length} measurable criteria</small></span><button class="text-button luna-apply-button" class:applied={taskModal.taskModalDescriptionApplied} type="button" disabled={taskModal.taskModalDescriptionApplied} aria-label={taskModal.taskModalDescriptionApplied ? 'Description suggestion applied' : 'Apply description suggestion'} on:click={() => applyLunaField('description')}>{taskModal.taskModalDescriptionApplied ? '✓ Applied' : 'Apply'}</button></div>
              <div class="luna-preview-field" data-luna-field="priority"><span><strong>Priority</strong><span class={`priority-pill priority-${taskModal.taskModalSuggestion.priority}`}>{priorityLabels[taskModal.taskModalSuggestion.priority]}</span><small>{taskModal.taskModalSuggestion.rationale}</small></span><button class="text-button luna-apply-button" class:applied={taskModal.taskModalPriorityApplied} type="button" disabled={taskModal.taskModalPriorityApplied} aria-label={taskModal.taskModalPriorityApplied ? 'Priority suggestion applied' : 'Apply priority suggestion'} on:click={() => applyLunaField('priority')}>{taskModal.taskModalPriorityApplied ? '✓ Applied' : 'Apply'}</button></div>
            {#if taskModal.taskModalSuggestion.supporting_task_keys.length}<div class="luna-sources"><strong>Related tasks</strong>{#each taskModal.taskModalSuggestion.supporting_task_keys as key}<a href={taskDeepLink(taskModal.taskModalProject?.slug || workspace.activeProjectSlug, key)} on:click={closeTaskModal}>{key}</a>{/each}</div>{/if}
            </div>
          </div>
        {/if}
      </section>
      <section class="task-details-fields" aria-labelledby="task-details-heading"><div class="task-details-heading"><div><span class="eyebrow">Task details</span><h3 id="task-details-heading">Shape the work</h3></div><span>Review before creating</span></div>
        <label>Task title<input bind:value={taskModal.taskModalTitle} placeholder="What should move forward?" /></label>
        <label>Description <span class="optional">Optional · Markdown supported</span><textarea rows="3" bind:value={taskModal.taskModalDescription} placeholder="Add the context your future self will need."></textarea></label>
        <div class="form-row"><label>Priority<select bind:value={taskModal.taskModalPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label><label>Due date <span class="optional">Optional</span><input type="date" bind:value={taskModal.taskModalDueDate} /></label></div>
        <label>Assignee <span class="optional">Optional</span><input bind:value={taskModal.taskModalAssignee} placeholder="Actor ID" /></label>
        <label>Focus <span class="optional">Optional</span><select aria-label="Focus" bind:value={taskModal.taskModalReleaseId}><option value="">No focus</option>{#each (releasePlan.releasesByProject[taskModal.taskModalProjectId] || []).filter((release) => release.status === 'planned') as release}<option value={release.id}>{release.name}</option>{/each}</select></label>
      </section>
      </fieldset>{#if taskModal.taskModalCreating}<p role="status">Saving… Keep this form open while the result is confirmed.</p>{/if}<div class="modal-actions"><button class="text-button" type="button" disabled={taskModal.taskModalCreating || Boolean(creation.taskCreation)} on:click={closeTaskModal}>Cancel</button><button class="button primary" type="submit" disabled={taskModal.taskModalCreating || (!creation.taskCreation && (taskModal.taskModalLoading || !taskModal.taskModalTitle.trim() || !taskModal.taskModalColumns.some((column) => column.id === taskModal.taskModalColumnId)))}>{#if taskModal.taskModalCreating}<span class="button-spinner"></span>{/if}{taskModal.taskModalCreating ? 'Saving…' : creation.taskCreation ? 'Retry original submission' : 'Create task'}</button></div>
    </form>
  </div>
{/if}
