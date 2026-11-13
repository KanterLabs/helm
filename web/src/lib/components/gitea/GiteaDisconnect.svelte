<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../api';
  import type { GiteaStatus } from '../../types';
  import { clearDraft, errorCode, messageFor, type ProductLabel } from './giteaConnection';

  /** Disconnect, after a confirmation that links to the forge's revoke page. */
  export let product: ProductLabel = 'Gitea';
  export let revokeUrl = '';
  export let onDisconnected: (status: GiteaStatus) => void = () => undefined;
  export let announce: (text: string) => void = () => undefined;

  let confirming = false;
  let busy = false;
  let failure = '';
  let trigger: HTMLButtonElement;
  let cancelButton: HTMLButtonElement;

  async function ask() {
    confirming = true;
    failure = '';
    await tick();
    cancelButton?.focus();
  }

  async function cancel() {
    confirming = false;
    await tick();
    trigger?.focus();
  }

  async function confirm() {
    if (busy) return;
    busy = true;
    failure = '';
    try {
      const status = await api.disconnectGitea();
      clearDraft();
      announce(`Disconnected from ${product}.`);
      onDisconnected(status);
    } catch (cause) {
      failure = messageFor(errorCode(cause).code, product).text;
      announce(failure);
    } finally {
      busy = false;
    }
  }
</script>

{#if !confirming}
  <button class="text-button danger-text-button gitea-disconnect" type="button" bind:this={trigger} on:click={ask}>Disconnect</button>
{:else}
  <div class="gitea-confirm" role="group" aria-labelledby="gitea-confirm-title" data-gitea-disconnect-confirm>
    <p id="gitea-confirm-title"><strong>Disconnect Helm from {product}?</strong> Helm deletes its sign-in and stops reading repositories.</p>
    <p>To remove Helm's authorization on {product} as well, delete it under {#if revokeUrl}<a href={revokeUrl} target="_blank" rel="noopener noreferrer">Authorized OAuth2 Applications</a>{:else}Authorized OAuth2 Applications{/if}.</p>
    {#if failure}<div class="inline-alert error"><span aria-hidden="true">!</span><span>{failure}</span></div>{/if}
    <div class="gitea-confirm-actions">
      <button class="button quiet-button compact-button danger-button" type="button" disabled={busy} on:click={confirm}>{#if busy}<span class="button-spinner gitea-dark-spinner" aria-hidden="true"></span>{/if}Yes, disconnect</button>
      <button class="button quiet-button compact-button" type="button" bind:this={cancelButton} disabled={busy} on:click={cancel}>Cancel</button>
    </div>
  </div>
{/if}

<style>
  .gitea-disconnect { justify-self: start; min-height: 30px; }
  .gitea-confirm { display: grid; gap: 8px; padding: 12px; border: 1px solid color-mix(in srgb, var(--red), var(--border) 70%); border-radius: 10px; background: var(--red-soft); font-size: var(--text-sm); }
  .gitea-confirm p { margin: 0; color: var(--ink-soft); line-height: 1.5; overflow-wrap: anywhere; }
  .gitea-confirm a { color: var(--purple); font-weight: 600; }
  .gitea-confirm-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .gitea-dark-spinner { border-color: color-mix(in srgb, var(--ink), transparent 70%); border-top-color: var(--ink); }
  @media (max-width: 640px) {
    .gitea-disconnect { min-height: 44px; }
    .gitea-confirm-actions .button { flex: 1 1 100%; min-height: 44px; }
  }
</style>
