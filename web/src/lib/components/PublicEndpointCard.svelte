<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { ApiError, type PublicEndpointView } from '../types';

  export let onChanged: () => void = () => undefined;

  const stateLabels: Record<string, string> = {
    stopped: 'Stopped',
    starting: 'Connecting…',
    connected: 'Connected',
    restarting: 'Reconnecting',
    error: 'Error'
  };

  let view: PublicEndpointView | null = null;
  let unavailable = '';
  let error = '';
  let notice = '';
  let hostname = '';
  let token = '';
  let saving = false;
  let disableOpen = false;
  let disableToken = '';
  let poll: ReturnType<typeof setInterval> | undefined;

  function message(value: unknown, fallback: string): string {
    return value instanceof Error && value.message ? value.message : fallback;
  }

  async function load() {
    try {
      view = await api.getPublicEndpoints();
      unavailable = '';
    } catch (cause) {
      if (cause instanceof ApiError && cause.code === 'public_endpoints_disabled') unavailable = cause.message;
      else error = message(cause, 'Public endpoint status could not be loaded.');
    }
  }

  async function create() {
    if (!hostname.trim() || !token.trim() || saving) return;
    saving = true;
    error = '';
    notice = '';
    try {
      view = await api.createPublicEndpoint({ hostname: hostname.trim(), api_token: token.trim() });
      notice = `Public URL ready: https://${view.active?.hostname}. New webhook URLs use it.`;
      hostname = '';
      onChanged();
    } catch (cause) {
      error = message(cause, 'The public endpoint could not be created.');
    } finally {
      token = '';
      saving = false;
    }
  }

  async function disable() {
    if (!view?.active || saving) return;
    saving = true;
    error = '';
    try {
      const result = await api.disablePublicEndpoint(view.active.id, disableToken.trim());
      notice = result.warning || `Public URL https://${result.endpoint.hostname} removed.`;
      disableOpen = false;
      onChanged();
      await load();
    } catch (cause) {
      error = message(cause, 'The public endpoint could not be disabled.');
    } finally {
      disableToken = '';
      saving = false;
    }
  }

  onMount(() => {
    void load();
    poll = setInterval(() => {
      if (view?.active && view.connector.state !== 'connected') void load();
    }, 5000);
  });
  onDestroy(() => clearInterval(poll));
</script>

<section class="public-endpoint" aria-labelledby="public-endpoint-heading">
  <h3 id="public-endpoint-heading">Public URL</h3>
  {#if unavailable}
    <p class="optional">{unavailable}</p>
  {:else if view?.active}
    <p>Outside apps reach this Helm at <strong data-public-hostname>https://{view.active.hostname}</strong>. Only webhook paths are public; everything else returns 404.</p>
    <p class="connector">Connector: <span class={`connector-state state-${view.connector.state}`} data-connector-state>{stateLabels[view.connector.state] || view.connector.state}</span>{#if view.connector.message}<span class="optional"> · {view.connector.message}</span>{/if}</p>
    {#if disableOpen}
      <form class="public-form" on:submit|preventDefault={disable}>
        <label>Cloudflare API token <span class="optional">Optional — also deletes the DNS record and tunnel</span><input type="password" autocomplete="off" bind:value={disableToken} /></label>
        <div class="public-actions"><button class="text-button" type="button" on:click={() => { disableOpen = false; disableToken = ''; }}>Cancel</button><button class="button danger-button" type="submit" disabled={saving}>{saving ? 'Removing…' : 'Remove public URL'}</button></div>
      </form>
    {:else}
      <button class="button quiet-button" type="button" on:click={() => { disableOpen = true; }}>Remove public URL…</button>
    {/if}
  {:else if view}
    <p>Self-hosting behind NAT or a private network? Paste a Cloudflare API token and pick an unused hostname. Helm creates a Cloudflare Tunnel that publishes <strong>only</strong> webhook paths, with no open ports or certificates. The token is used once and never stored.</p>
    {#if !view.connector_available}<div class="inline-alert warning" role="note"><span>!</span>cloudflared is not installed on this server, so a public URL cannot run here. The Docker image includes it; other installs need it on PATH or HELM_CLOUDFLARED_BINARY.</div>{/if}
    <form class="public-form" on:submit|preventDefault={create}>
      <label>Hostname<input aria-label="Public hostname" bind:value={hostname} placeholder="hooks.example.com" autocomplete="off" required /></label>
      <label>Cloudflare API token<input aria-label="Cloudflare API token" type="password" bind:value={token} autocomplete="off" required /></label>
      <button class="button primary" type="submit" disabled={!hostname.trim() || !token.trim() || saving}>{saving ? 'Creating tunnel…' : 'Create public URL'}</button>
    </form>
    <details>
      <summary>Token permissions</summary>
      <ul>{#each view.required_permissions as permission}<li>{permission}</li>{/each}</ul>
      <p class="optional">Exposed paths: <code>{view.exposed_paths}</code>. The server needs outbound access to Cloudflare (port 7844) and the <code>cloudflared</code> binary.</p>
    </details>
    {#if view.history.some((item) => item.cleanup_pending)}
      <p class="optional">Earlier endpoints still have Cloudflare resources to delete: {view.history.filter((item) => item.cleanup_pending).map((item) => item.hostname).join(', ')}.</p>
    {/if}
  {/if}
  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class="inline-alert" role="status"><span>✓</span>{notice}</div>{/if}
</section>

<style>
  .public-endpoint { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .public-endpoint h3 { margin: 0; font-size: 14px; }
  .public-endpoint p { margin: 0; font-size: 13px; line-height: 1.5; }
  .public-form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 10px; align-items: end; }
  .public-form label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .public-actions { display: flex; gap: 10px; align-items: center; }
  .connector-state { font-weight: 800; }
  .state-connected { color: var(--semantic-green); }
  .state-restarting, .state-error { color: var(--semantic-red); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  details { font-size: 12px; }
  details ul { margin: 6px 0; padding-left: 18px; }
  @media (max-width: 760px) { .public-form { grid-template-columns: minmax(0, 1fr); } }
</style>
