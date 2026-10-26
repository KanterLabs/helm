<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import type { Actor, PublicEndpointView } from '../types';
  import TicketIntegrations from './TicketIntegrations.svelte';

  export let user: Actor;
  /** Whether the New ticket form is open; the workspace renders the form. */
  export let creating = false;

  let connectOpen = false;
  // Admins see at a glance whether this Helm is reachable from the internet.
  let publicAccess: PublicEndpointView | null = null;

  async function loadPublicAccess() {
    if (!user.admin) return;
    try {
      publicAccess = await api.getPublicEndpoints();
    } catch {
      publicAccess = null;
    }
  }

  function toggleConnect() {
    connectOpen = !connectOpen;
    if (!connectOpen) void loadPublicAccess();
  }

  onMount(() => {
    // ?connect=1 deep-links straight to Connect apps (e.g. from Admin).
    const url = new URL(window.location.href);
    if (url.searchParams.get('connect') === '1') {
      connectOpen = true;
      url.searchParams.delete('connect');
      window.history.replaceState({}, '', url.pathname + url.search);
    }
    void loadPublicAccess();
  });
</script>

<header class="tickets-heading">
  <div>
    <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Triage</span></div>
    <h1 id="tickets-heading">Tickets</h1>
    <p>Alerts and requests that need a person. Everything new lands in Needs triage; file a ticket into a project when you know where it belongs.</p>
  </div>
  <div class="tickets-heading-actions">
    <button class="button quiet-button" type="button" aria-expanded={connectOpen} on:click={toggleConnect}>⇄ Connect apps{#if publicAccess?.active}<span class={`public-pill pill-${publicAccess.connector.state}`} title={`Public URL https://${publicAccess.active.hostname} is ${publicAccess.connector.state === 'connected' ? 'live' : publicAccess.connector.state}`} data-public-pill>● Public</span>{/if}</button>
    <button class="button primary" type="button" on:click={() => { creating = !creating; }} aria-expanded={creating}>＋ New ticket</button>
  </div>
</header>

{#if connectOpen}<TicketIntegrations {user} onPublicAccessChanged={loadPublicAccess} />{/if}

<style>
  .tickets-heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; }
  .tickets-heading h1 { margin: 0; font: 600 var(--text-xl) var(--font-sans); letter-spacing: -.02em; }
  .tickets-heading p { max-width: 650px; margin: 8px 0 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.5; }
  .tickets-heading-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .public-pill { margin-left: 6px; padding: 1px 6px; border-radius: 999px; font-size: var(--text-2xs); font-weight: 600; color: var(--semantic-amber); background: var(--amber-soft); }
  .public-pill.pill-connected { color: var(--semantic-green); background: var(--green-soft); }
  .public-pill.pill-restarting, .public-pill.pill-error { color: var(--semantic-red); background: var(--red-soft); }
  .breadcrumbs { display: flex; gap: 6px; color: var(--muted); font-size: var(--text-xs); }
  @media (max-width: 760px) {
    .tickets-heading { align-items: flex-start; flex-direction: column; }
  }
</style>
