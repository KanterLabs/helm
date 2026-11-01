<svelte:options runes={true} />

<script lang="ts">
  // Confirmations, token reveal, announcements and toasts.
  import ConfirmDialog from '../../../lib/components/ConfirmDialog.svelte';
  import { cancelAdminConfirmation, runAdminConfirmation } from '../../admin';
  import { focusTrap, settleConfirm } from '../../dialogs';
  import { runToastAction } from '../../feedback';
  import TokenDialogs from '../../../lib/components/tokens/TokenDialogs.svelte';
  import { admin } from '../../state/admin.svelte';
  import { dialogs } from '../../state/dialogs.svelte';
  import { live } from '../../state/live.svelte';
</script>

{#if dialogs.confirmRequest}
  <ConfirmDialog
    title={dialogs.confirmRequest.title}
    message={dialogs.confirmRequest.message}
    confirmLabel={dialogs.confirmRequest.confirmLabel}
    cancelLabel={dialogs.confirmRequest.cancelLabel || 'Cancel'}
    onConfirm={() => settleConfirm(true)}
    onCancel={() => settleConfirm(false)}
  />
{/if}

{#if admin.adminConfirmation}
  <div class="modal-backdrop" role="presentation" on:click={cancelAdminConfirmation}></div>
  <div class="modal admin-confirm-modal" role="alertdialog" aria-modal="true" aria-labelledby="admin-confirm-title" aria-describedby="admin-confirm-message" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">Confirm administration change</span><h2 id="admin-confirm-title">{admin.adminConfirmation.title}</h2></div><button class="icon-button" type="button" aria-label="Close confirmation" on:click={cancelAdminConfirmation}>×</button></div>
    <p id="admin-confirm-message" class="admin-confirm-message">{admin.adminConfirmation.message}</p>
    <div class="modal-actions"><button class="text-button" type="button" disabled={admin.adminConfirmationBusy} on:click={cancelAdminConfirmation}>Cancel</button><button class="button primary" data-dialog-initial-focus type="button" disabled={admin.adminConfirmationBusy} on:click={runAdminConfirmation}>{#if admin.adminConfirmationBusy}<span class="button-spinner"></span>{/if}{admin.adminConfirmation.confirmLabel}</button></div>
  </div>
{/if}

<TokenDialogs />

<div class="sr-only" aria-live="polite" aria-atomic="true">{live.liveAnnouncement}</div>
<div class="toast-stack" aria-label="Notifications">{#each live.toasts as item (item.id)}<div class={`toast ${item.kind}`} role={item.role} aria-live={item.live} aria-atomic="true"><span aria-hidden="true">{item.kind === 'success' ? '✓' : item.kind === 'error' ? '!' : 'i'}</span><span class="toast-message">{item.message}</span>{#if item.action}<button class="toast-action" type="button" disabled={item.action.pending} on:click={() => void runToastAction(item)}>{item.action.pending ? 'Working…' : item.action.label}</button>{/if}<button class="icon-button tiny" type="button" aria-label="Dismiss notification" on:click={() => live.toasts = live.toasts.filter((toastItem) => toastItem.id !== item.id)}>×</button></div>{/each}</div>
