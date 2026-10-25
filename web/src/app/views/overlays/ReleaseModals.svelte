<svelte:options runes={true} />

<script lang="ts">
  // Focus area edit and reopen dialogs.
  import PwaStatus from '../../../lib/components/PwaStatus.svelte';
  import { focusTrap } from '../../dialogs';
  import { beforePwaUpdate } from '../../pwaGuard';
  import { closeReleaseModal, closeReleaseReopen, reopenRelease, saveRelease } from '../../releases/edit';
  import { releaseWritesDisabled } from '../../releases/load';
  import { releasePlan } from '../../state/releasePlan.svelte';
  import { session } from '../../state/session.svelte';
</script>

{#if releasePlan.showReleaseModal}
  <div class="modal-backdrop" role="presentation" on:click={closeReleaseModal}></div>
  <div class="modal release-modal" role="dialog" aria-modal="true" aria-labelledby="release-modal-title" use:focusTrap><PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} />
    <div class="modal-header"><div><span class="eyebrow">Focused work</span><h2 id="release-modal-title">{releasePlan.releaseEditingId ? 'Edit a focus' : 'Create a focus'}</h2></div><button class="icon-button" type="button" aria-label="Close focus dialog" on:click={closeReleaseModal}>×</button></div>
    {#if releasePlan.releaseModalError}<div class="inline-alert error" role="alert"><span>!</span>{releasePlan.releaseModalError}</div>{/if}
    <form class="release-form" on:submit|preventDefault={saveRelease}>
      <label><span class="release-field-label">Focus name</span><input data-dialog-initial-focus bind:value={releasePlan.releaseModalName} maxlength="200" placeholder="Launch the new onboarding" required /></label>
      <label><span class="release-field-label">Description <span class="optional">Optional</span></span><textarea rows="4" bind:value={releasePlan.releaseModalDescription} maxlength="20000" placeholder="What belongs in this focus?"></textarea></label>
      <label><span class="release-field-label">Target date <span class="optional">Optional</span></span><input type="date" bind:value={releasePlan.releaseModalTargetDate} /></label>
      <p class="field-hint release-form-hint">Assign tasks to this focus to keep the work that matters now together. An issue’s affected version remains separate context.</p>
      <div class="modal-actions"><button class="text-button" type="button" on:click={closeReleaseModal}>Cancel</button><button class="button primary" type="submit" disabled={releasePlan.releaseModalSaving || releaseWritesDisabled() || !releasePlan.releaseModalName.trim()}>{#if releasePlan.releaseModalSaving}<span class="button-spinner"></span>{/if}{releasePlan.releaseEditingId ? 'Save focus' : 'Create focus'}</button></div>
    </form>
  </div>
{/if}

{#if releasePlan.releaseReopenTarget}
  <div class="modal-backdrop" role="presentation" on:click={closeReleaseReopen}></div>
  <div class="modal release-modal" role="dialog" aria-modal="true" aria-labelledby="release-reopen-title" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">Focus history</span><h2 id="release-reopen-title">Reopen {releasePlan.releaseReopenTarget.name}</h2></div><button class="icon-button" type="button" aria-label="Close reopen dialog" on:click={closeReleaseReopen}>×</button></div>
    <p class="release-reopen-copy">Reopening makes this focus active again. Give your team a reason so the change stays auditable.</p>
    <label>Reason<textarea data-dialog-initial-focus rows="3" bind:value={releasePlan.releaseReopenReason} placeholder="We found one more required task…" required></textarea></label>
    <div class="modal-actions"><button class="text-button" type="button" on:click={closeReleaseReopen}>Cancel</button><button class="button primary" type="button" disabled={releasePlan.releaseReopening || releaseWritesDisabled() || !releasePlan.releaseReopenReason.trim()} on:click={() => void reopenRelease()}>{#if releasePlan.releaseReopening}<span class="button-spinner"></span>{/if}Reopen focus</button></div>
  </div>
{/if}
