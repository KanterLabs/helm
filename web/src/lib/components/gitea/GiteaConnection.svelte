<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../../api';
  import type { GiteaStatus } from '../../types';
  import GiteaConnected from './GiteaConnected.svelte';
  import GiteaMessage from './GiteaMessage.svelte';
  import GiteaReconnect from './GiteaReconnect.svelte';
  import GiteaSetup from './GiteaSetup.svelte';
  import { clearDraft, displayAddress, loadDraft, messageFor, productLabel, takeReturnParams, unavailableText, type Draft, type Message, type ProductLabel } from './giteaConnection';

  // Settings › Gitea (administrators only). Connects Helm to a self-hosted
  // Gitea or Forgejo with OAuth. Standard: docs/GITEA_OAUTH_UX.md; API:
  // contract C5 in docs/GITEA_OAUTH_PLAN.md; failure list: ./failures.md.

  let section: HTMLElement | undefined;
  let status: GiteaStatus | null = null;
  let loading = true;
  let loadFailed = false;
  let live = '';
  let probedProduct: ProductLabel | null = null;
  let restore: { draft: Draft | null; code: string } | null = null;
  let returned: Message | null = null;
  let setupKey = 0;

  // A saved connection Helm cannot trust arrives as reconnect_required with no
  // server: there is nothing to sign in to again, so start over at step 1.
  $: lost = !!status && status.available && status.state === 'reconnect_required' && !status.server?.base_url;
  $: state = status ? (!status.available ? 'unavailable' : lost ? 'disconnected' : status.state) : '';
  $: product = state === 'disconnected' && probedProduct ? probedProduct : productLabel(status?.server?.product);
  $: badge = !status ? '' : state === 'unavailable' ? 'Unavailable' : state === 'connected' ? 'Connected' : state === 'reconnect_required' ? 'Signed out' : 'Not connected';

  async function announce(text: string) {
    live = '';
    await tick();
    live = text;
  }

  async function load(): Promise<boolean> {
    loading = true;
    loadFailed = false;
    try {
      status = await api.getGiteaConnection();
      return true;
    } catch {
      loadFailed = true;
      return false;
    } finally {
      loading = false;
    }
  }

  function changed(next: GiteaStatus) {
    if (next.state !== 'connected' || status?.state !== 'connected') {
      restore = null;
      returned = null;
      setupKey += 1;
    }
    status = next;
  }

  async function showConnection() {
    restore = null;
    returned = null;
    if (await load()) {
      await tick();
      section?.querySelector<HTMLElement>('[data-gitea-connected-heading]')?.focus();
    }
  }

  async function focusResult(selector: string) {
    await tick();
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    section?.scrollIntoView({ block: 'start', behavior: reduce ? 'auto' : 'smooth' });
    section?.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true });
  }

  onMount(async () => {
    const back = takeReturnParams();
    if (back.connected) clearDraft();
    // Set before the first status arrives: GiteaSetup reads it when it mounts.
    if (back.error) restore = { draft: loadDraft(), code: back.error };
    if (!(await load()) || !status) return;
    if (back.connected && status.state === 'connected') {
      void announce(`Connected to ${displayAddress(status.server?.base_url || '')} as @${status.account?.login || 'unknown'}.`);
      await focusResult('[data-gitea-connected-heading]');
    } else if (back.error) {
      if (state === 'disconnected') {
        // GiteaSetup restores the values, re-checks the server and focuses the result.
        section?.scrollIntoView({ block: 'start' });
      } else {
        restore = null;
        returned = messageFor(back.error, product, {}, status.unavailable_reason);
        void announce(returned.text);
        await focusResult('[data-gitea-result]');
      }
    }
  });
</script>

<section class="settings-section gitea-settings" bind:this={section} data-gitea-settings aria-labelledby="gitea-settings-heading" aria-busy={loading}>
  <div class="settings-section-heading">
    <div>
      <span class="eyebrow">Code hosting</span>
      <h2 id="gitea-settings-heading">{product}</h2>
      <p>{#if product === 'Forgejo'}Connect Helm to your self-hosted Forgejo{:else}Connect Helm to your self-hosted Gitea or Forgejo{/if} so it can read your repositories. Helm signs in with read-only access and never sees a password.</p>
    </div>
    {#if badge}<span class="gitea-state" data-state={state}>{badge}</span>{/if}
  </div>

  <div class="sr-only" role="status" aria-live="polite" data-gitea-live>{live}</div>

  {#if loading && !status}
    <div class="list-skeleton gitea-skeleton" data-gitea-loading><div></div></div>
  {:else if loadFailed && !status}
    <div class="inline-alert error" data-gitea-load-error><span aria-hidden="true">!</span><span>Helm could not load the Gitea connection.</span><button class="text-button" type="button" on:click={load}>Retry</button></div>
  {:else if status}
    {#if returned && state !== 'reconnect_required'}
      <GiteaMessage message={returned} {product} result revokeUrl={status.revoke_url} registerUrl={status.register_url} onRetry={() => { returned = null; }} onShowConnection={showConnection} />
    {/if}
    {#if state === 'unavailable'}
      <p class="gitea-unavailable" data-gitea-unavailable={status.unavailable_reason}>{#if status.unavailable_reason === 'public_origin_missing'}Sign-in needs Helm's public address. An operator sets <code>HELM_PUBLIC_ORIGIN</code>.{:else}{unavailableText(status.unavailable_reason, product)}{/if}</p>
    {:else if state === 'connected'}
      <GiteaConnected {status} onChange={changed} {announce} />
    {:else if state === 'reconnect_required'}
      <GiteaReconnect {status} result={returned} onChange={changed} onShowConnection={showConnection} {announce} />
    {:else}
      {#if lost}<p class="gitea-unavailable gitea-lost" data-gitea-lost>Helm's saved connection could not be read, so it needs connecting again.</p>{/if}
      {#key setupKey}
        <GiteaSetup {status} {restore} {announce} onProduct={(next) => { probedProduct = next; }} onShowConnection={showConnection} />
      {/key}
    {/if}
  {/if}
</section>

<style>
  .gitea-settings { display: grid; gap: 4px; min-width: 0; scroll-margin-top: 16px; }
  .gitea-settings .settings-section-heading { margin-bottom: 12px; }
  .gitea-settings .settings-section-heading p { overflow-wrap: anywhere; }
  .gitea-state { flex-shrink: 0; padding: 3px 10px; border: 1px solid var(--border-strong); border-radius: 999px; color: var(--ink-soft); font-size: var(--text-xs); font-weight: 600; white-space: nowrap; }
  .gitea-state[data-state='connected'] { border-color: var(--green); color: var(--semantic-green); }
  .gitea-state[data-state='reconnect_required'] { border-color: var(--amber); color: var(--semantic-amber); }
  .gitea-state[data-state='disconnected'], .gitea-state[data-state='unavailable'] { border-style: dashed; }
  .gitea-skeleton > div { height: 120px; }
  .gitea-unavailable { margin: 0; padding: 10px 12px; border: 1px dashed var(--border-strong); border-radius: 9px; color: var(--ink-soft); background: var(--surface-muted); font-size: var(--text-sm); line-height: 1.5; overflow-wrap: anywhere; }
  .gitea-lost { margin-bottom: 10px; }
  .gitea-unavailable code { font-family: var(--font-mono); font-size: var(--text-xs); }
  .gitea-settings > :global(.gitea-message) { margin-bottom: 10px; }
  @media (prefers-reduced-motion: reduce) { .gitea-skeleton > div { animation: none; } }
</style>
