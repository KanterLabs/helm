<svelte:options runes={true} />

<script lang="ts">
  // New project dialog.
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import { projectInitials } from '../../../lib/state';
  import { closeProjectModal, createProject } from '../../creation';
  import { focusTrap } from '../../dialogs';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { creation } from '../../state/creation.svelte';
  import { session } from '../../state/session.svelte';
</script>

{#if creation.showProjectModal}
  <div class="modal-backdrop" role="presentation" on:click={closeProjectModal}></div>
  <div class="modal project-modal" role="dialog" aria-modal="true" aria-labelledby="project-modal-title" use:focusTrap><PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} /><div class="modal-header"><div><span class="eyebrow">New workspace</span><h2 id="project-modal-title">Create a project</h2></div><button class="icon-button" type="button" aria-label="Close" disabled={creation.projectCreating || Boolean(creation.projectCreation)} on:click={closeProjectModal}>×</button></div>{#if creation.projectFormError}<div class="inline-alert error" role="alert"><span>!</span>{creation.projectFormError}</div>{/if}<form aria-busy={creation.projectCreating} on:submit|preventDefault={createProject}><fieldset disabled={creation.projectCreating || Boolean(creation.projectCreation)} style="border: 0; padding: 0; margin: 0; min-width: 0;"><div class="project-form-title"><span class="project-dot huge" style={`--project-color: ${creation.projectColorDraft}`}>{projectInitials({ name: creation.projectNameDraft || 'New project', key: creation.projectKeyDraft || 'NP' })}</span><div><label>Project name<input data-dialog-initial-focus bind:value={creation.projectNameDraft} placeholder="Product launch" /></label></div></div><div class="form-row"><label>Project key<input maxlength="16" bind:value={creation.projectKeyDraft} placeholder="PROD" /></label><label>Accent color<input class="color-input" type="color" bind:value={creation.projectColorDraft} /></label></div><label>Description <span class="optional">Optional</span><textarea rows="3" bind:value={creation.projectDescriptionDraft} placeholder="A short note about what this project is for."></textarea><span class="field-hint">Helm will add Backlog, Ready, In progress, Blocked, and Done columns automatically.</span></label></fieldset>{#if creation.projectCreating}<p role="status">Saving… Keep this form open while the result is confirmed.</p>{/if}<div class="modal-actions"><button class="text-button" type="button" disabled={creation.projectCreating || Boolean(creation.projectCreation)} on:click={closeProjectModal}>Cancel</button><button class="button primary" type="submit" disabled={creation.projectCreating || (!creation.projectCreation && (!creation.projectNameDraft.trim() || !creation.projectKeyDraft.trim()))}>{#if creation.projectCreating}<span class="button-spinner"></span>{/if}{creation.projectCreating ? 'Saving…' : creation.projectCreation ? 'Retry original submission' : 'Create project'}</button></div></form></div>
{/if}
