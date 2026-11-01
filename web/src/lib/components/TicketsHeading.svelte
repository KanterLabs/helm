<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../api';
  import type { Actor, PublicEndpointView } from '../types';
  import TicketIntegrations from './TicketIntegrations.svelte';
  import { defaultConnectionTab, type ConnectionTab } from './tickets/connections';

  export let user: Actor;
  /** Whether the New ticket form is open; the workspace renders the form. */
  export let creating = false;
  /** Connect apps holds unsaved drafts or a one-time URL (additive, bindable). */
  export let dirty = false;

  let connectOpen = false;
  let connectButton: HTMLButtonElement;
  // Remembered while the page is open, never stored.
  let connectTab: ConnectionTab | null = null;
  let openTab: ConnectionTab = 'email';
  let connectionsDirty = false;
  // Admins see at a glance whether this Helm is reachable from the internet.
  let publicAccess: PublicEndpointView | null = null;

  $: dirty = connectOpen && connectionsDirty;
  $: publicTitle = publicAccess?.active ? `Public URL https://${publicAccess.active.hostname} is ${publicAccess.connector.state === 'connected' ? 'live' : publicAccess.connector.state}` : '';

  async function loadPublicAccess() {
    if (!user.admin) return;
    try {
      publicAccess = await api.getPublicEndpoints();
    } catch {
      publicAccess = null;
    }
  }

  function openConnect(returningFromCloudflare = false) {
    openTab = returningFromCloudflare ? 'public' : connectTab || defaultConnectionTab(user.admin, publicAccess);
    connectOpen = true;
  }

  async function closeConnect() {
    connectTab = openTab;
    connectOpen = false;
    connectionsDirty = false;
    void loadPublicAccess();
    await tick();
    connectButton?.focus();
  }

  onMount(async () => {
    // ?connect=1 deep-links straight to Connect apps (e.g. from Admin, or
    // back from Cloudflare sign-in); only `connect` leaves the URL here.
    const url = new URL(window.location.href);
    const deepLink = url.searchParams.get('connect') === '1';
    const returning = url.searchParams.has('cloudflare') || url.searchParams.has('cloudflare_error');
    if (deepLink) {
      url.searchParams.delete('connect');
      window.history.replaceState({}, '', url.pathname + url.search);
    }
    await loadPublicAccess();
    if (deepLink) openConnect(returning);
  });
</script>

<header class="tickets-heading">
  <div class="tickets-heading-copy">
    <h1 id="tickets-heading">Tickets</h1>
    <p>Alerts and requests that need a person. New ones land in Needs triage.</p>
  </div>
  <div class="tickets-heading-actions">
    <button bind:this={connectButton} class="button quiet-button connections-entry" type="button" aria-haspopup="dialog" aria-expanded={connectOpen} on:click={() => openConnect()}>
      <span aria-hidden="true">⇄</span> Connect apps{#if publicAccess?.active}<span class={`public-pill pill-${publicAccess.connector.state}`} title={publicTitle} data-public-pill>● Public</span>{/if}
    </button>
    <button class="button primary" type="button" on:click={() => { creating = !creating; }} aria-expanded={creating}>＋ New ticket</button>
  </div>
</header>

{#if connectOpen}
  <TicketIntegrations {user} {publicAccess} bind:tab={openTab} onClose={closeConnect} onPublicAccessChanged={loadPublicAccess} bind:dirty={connectionsDirty} />
{/if}

<style>
  .tickets-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 10px 16px; }
  .tickets-heading-copy { min-width: 0; }
  .tickets-heading h1 { margin: 0; font: 600 var(--text-xl) var(--font-sans); letter-spacing: -.02em; }
  .tickets-heading p { margin: 4px 0 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.45; }
  .tickets-heading-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .public-pill { margin-left: 2px; padding: 1px 6px; border-radius: 999px; font-size: var(--text-2xs); font-weight: 600; color: var(--semantic-amber); background: var(--amber-soft); }
  .public-pill.pill-connected { color: var(--semantic-green); background: var(--green-soft); }
  .public-pill.pill-restarting, .public-pill.pill-error { color: var(--semantic-red); background: var(--red-soft); }
  @media (max-width: 520px) {
    .tickets-heading-actions { width: 100%; }
    .tickets-heading-actions .button { flex: 1 1 0; }
  }
</style>
