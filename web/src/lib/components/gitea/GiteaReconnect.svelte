<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../api';
  import type { GiteaStatus } from '../../types';
  import GiteaDisconnect from './GiteaDisconnect.svelte';
  import GiteaMessage from './GiteaMessage.svelte';
  import { authorizeAllowed, displayAddress, errorCode, messageFor, productLabel, type Message } from './giteaConnection';

  /** `reconnect_required`: one button that reuses the stored server and client ID. */
  export let status: GiteaStatus;
  export let result: Message | null = null;
  export let onChange: (status: GiteaStatus) => void = () => undefined;
  export let onShowConnection: () => void = () => undefined;
  export let announce: (text: string) => void = () => undefined;

  let busy = false;
  let root: HTMLElement;

  $: product = productLabel(status.server?.product);
  $: base = status.server?.base_url || '';

  async function fail(message: Message) {
    result = message;
    busy = false;
    announce(message.text);
    await tick();
    root?.querySelector<HTMLElement>('[data-gitea-result]')?.focus();
  }

  async function signIn() {
    if (busy) return;
    busy = true;
    result = null;
    try {
      const { authorize_url } = await api.startGiteaSignIn({ base_url: '', client_id: '', allow_insecure_http: status.server?.insecure_http ?? false });
      if (!authorizeAllowed(authorize_url, base)) return void fail(messageFor('authorize_outside', product));
      window.location.assign(authorize_url);
    } catch (cause) {
      const { code, details } = errorCode(cause);
      void fail(messageFor(code, product, details, status.unavailable_reason));
    }
  }
</script>

<svelte:window on:pageshow={(event) => { if (event.persisted) busy = false; }} />

<div class="gitea-reconnect" bind:this={root}>
  <div class="inline-alert warning"><span aria-hidden="true">!</span><span>Helm's sign-in to {product} has ended.</span></div>
  <p>Helm was connected to <strong>{displayAddress(base)}</strong>{#if status.account?.login}{' '}as <strong>@{status.account.login}</strong>{/if}. Sign in again to keep reading its repositories; nothing needs to change on {product}.</p>
  <button class="button primary gitea-primary" type="button" disabled={busy} aria-busy={busy} on:click={signIn}>{#if busy}<span class="button-spinner" aria-hidden="true"></span>{/if}Sign in again</button>
  {#if result}
    <GiteaMessage message={result} {product} result revokeUrl={status.revoke_url} registerUrl={status.register_url} onRetry={signIn} {onShowConnection} />
  {/if}
  <GiteaDisconnect {product} revokeUrl={status.revoke_url || status.register_url} {announce} onDisconnected={onChange} />
</div>

<style>
  .gitea-reconnect { display: grid; gap: 12px; justify-items: start; }
  .gitea-reconnect > * { max-width: 100%; }
  .gitea-reconnect > .inline-alert, .gitea-reconnect > :global(.gitea-message), .gitea-reconnect > :global(.gitea-confirm) { justify-self: stretch; }
  p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; overflow-wrap: anywhere; }
  .gitea-primary { min-height: 40px; padding: 0 18px; font-size: var(--text-sm); }
  @media (max-width: 640px) { .gitea-primary { justify-self: stretch; min-height: 44px; } }
</style>
