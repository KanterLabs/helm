<svelte:options runes={true} />

<script lang="ts">
  // Bug report dialog.
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import { changeBugModalProject, closeBugModal, reportBug } from '../../bugModal';
  import { severityLabels } from '../../constants';
  import { focusTrap } from '../../dialogs';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { bugModal } from '../../state/bugModal.svelte';
  import { creation } from '../../state/creation.svelte';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { session } from '../../state/session.svelte';
  import { workspace } from '../../state/workspace.svelte';
</script>

{#if bugModal.showBugModal}
  <div class="modal-backdrop" role="presentation" on:click={closeBugModal}></div>
  <div class="modal bug-create-modal" role="dialog" aria-modal="true" aria-labelledby="bug-modal-title" use:focusTrap><PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} />
    <div class="modal-header"><div><span class="eyebrow">Capture a regression</span><h2 id="bug-modal-title">Report a bug</h2></div><button class="icon-button" type="button" aria-label="Close" disabled={bugModal.bugModalCreating || Boolean(creation.bugCreation)} on:click={closeBugModal}>×</button></div>
    {#if bugModal.bugModalError}<div class="inline-alert error" role="alert"><span>!</span>{bugModal.bugModalError}</div>{/if}
    <form aria-busy={bugModal.bugModalCreating} on:submit|preventDefault={reportBug}><fieldset disabled={bugModal.bugModalCreating || Boolean(creation.bugCreation)} style="border: 0; padding: 0; margin: 0; min-width: 0;">
      <div class="form-row task-destination-row"><label>Project<select bind:value={bugModal.bugModalProjectId} on:change={() => void changeBugModalProject()}>{#each workspace.navProjects as project}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select></label><label>Column<select bind:value={bugModal.bugModalColumnId} disabled={bugModal.bugModalLoading || !bugModal.bugModalColumns.length}>{#each bugModal.bugModalColumns as column}<option value={column.id}>{column.name}</option>{/each}</select></label></div>
      <label>Bug title<input data-dialog-initial-focus bind:value={bugModal.bugModalTitle} placeholder="What went wrong?" /></label>
      <label>Actual behavior<textarea rows="3" bind:value={bugModal.bugModalActual} placeholder="What happened?" required></textarea></label>
      <label>Expected behavior<textarea rows="2" bind:value={bugModal.bugModalExpected} placeholder="What should have happened?"></textarea></label>
      <label>Reproduction steps <span class="optional">Optional</span><textarea rows="2" bind:value={bugModal.bugModalReproduction} placeholder="1. Open…&#10;2. Click…"></textarea></label>
      <div class="form-row"><label>Environment <span class="optional">Optional</span><input bind:value={bugModal.bugModalEnvironment} placeholder="Browser, OS, device" /></label><label>Affected version <span class="optional">Optional · customer version</span><input bind:value={bugModal.bugModalVersion} placeholder="e.g. 1.4.0" /></label></div>
      <label>Focus <span class="optional">Optional · distinct from affected version</span><select aria-label="Focus" bind:value={bugModal.bugModalReleaseId}><option value="">No focus</option>{#each (releasePlan.releasesByProject[bugModal.bugModalProjectId] || []).filter((release) => release.status === 'planned') as release}<option value={release.id}>{release.name}</option>{/each}</select></label>
      <div class="form-row"><label>Severity <span class="optional">Optional · triage later</span><select aria-label="Initial bug severity" bind:value={bugModal.bugModalSeverity}><option value="">Untriaged</option><option value="s1">{severityLabels.s1}</option><option value="s2">{severityLabels.s2}</option><option value="s3">{severityLabels.s3}</option><option value="s4">{severityLabels.s4}</option></select></label><label>Priority<select bind:value={bugModal.bugModalPriority}><option value="urgent">Urgent</option><option value="high">High</option><option value="normal">Normal</option><option value="low">Low</option></select></label></div>
      <label>Description <span class="optional">Optional · Markdown supported</span><textarea rows="2" bind:value={bugModal.bugModalDescription} placeholder="Add context beyond the reproduction details."></textarea></label>
      <label>Labels <span class="optional">Optional · comma separated</span><input bind:value={bugModal.bugModalLabels} placeholder="frontend, regression" /></label>
      </fieldset>{#if bugModal.bugModalCreating}<p role="status">Saving… Keep this form open while the result is confirmed.</p>{/if}<div class="modal-actions"><button class="text-button" type="button" disabled={bugModal.bugModalCreating || Boolean(creation.bugCreation)} on:click={closeBugModal}>Cancel</button><button class="button primary" type="submit" disabled={bugModal.bugModalCreating || (!creation.bugCreation && (bugModal.bugModalLoading || !bugModal.bugModalTitle.trim() || !bugModal.bugModalActual.trim() || !bugModal.bugModalColumns.some((column) => column.id === bugModal.bugModalColumnId)))}>{#if bugModal.bugModalCreating}<span class="button-spinner"></span>{/if}{bugModal.bugModalCreating ? 'Saving…' : creation.bugCreation ? 'Retry original submission' : 'Report bug'}</button></div>
    </form>
  </div>
{/if}
