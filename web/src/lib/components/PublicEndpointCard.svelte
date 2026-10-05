<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import { ApiError, type PublicEndpoint, type PublicEndpointView } from '../types';

  export let onChanged: () => void = () => undefined;

  const stateLabels: Record<string, string> = {
    stopped: 'Stopped',
    starting: 'Connecting…',
    connected: 'Connected',
    restarting: 'Reconnecting',
    error: 'Error'
  };
  const badgeLabels: Record<string, string> = {
    stopped: 'Stopped',
    starting: 'Connecting',
    connected: 'Live',
    restarting: 'Reconnecting',
    error: 'Error'
  };

  let view: PublicEndpointView | null = null;
  let unavailable = '';
  let error = '';
  let notice = '';
  let noticeKind: 'success' | 'warning' = 'success';
  let hostname = '';
  let token = '';
  let saving = false;
  let testing = false;
  let disableOpen = false;
  let disableToken = '';
  let cleanupId = '';
  let cleanupToken = '';
  let copied = '';
  let hostnameSuggestion = '';
  let poll: ReturnType<typeof setTimeout> | undefined;
  let destroyed = false;

  $: active = view?.active;
  $: connector = view?.connector;
  $: lastTest = view?.last_test;
  $: earlier = (view?.history || []).filter((item) => item.status !== 'active');
  $: pendingCleanup = earlier.filter((item) => item.cleanup_pending);

  function message(value: unknown, fallback: string): string {
    return value instanceof Error && value.message ? value.message : fallback;
  }

  // Poll quickly while the connector settles, slowly once it is live, and
  // not at all without an active URL or while the tab is hidden.
  function schedule() {
    clearTimeout(poll);
    if (destroyed || !view?.active) return;
    poll = setTimeout(async () => {
      if (document.visibilityState === 'visible') await load();
      schedule();
    }, view.connector.state === 'connected' ? 20_000 : 5_000);
  }

  async function load() {
    try {
      const previous = view?.active ? `${view.active.id}:${view.connector.state}` : '';
      view = await api.getPublicEndpoints();
      unavailable = '';
      // Keep the Connect apps indicator in step with connector changes.
      if (previous && previous !== (view.active ? `${view.active.id}:${view.connector.state}` : '')) onChanged();
    } catch (cause) {
      if (cause instanceof ApiError && cause.code === 'public_endpoints_disabled') unavailable = cause.message;
      else error = message(cause, 'Public endpoint status could not be loaded.');
    }
  }

  async function refresh() {
    await load();
    schedule();
  }

  async function create() {
    if (!hostname.trim() || !token.trim() || saving) return;
    saving = true;
    error = '';
    notice = '';
    try {
      view = await api.createPublicEndpoint({ hostname: hostname.trim(), api_token: token.trim() });
      notice = `Public URL ready: https://${view.active?.hostname}. New webhook URLs use it.`;
      noticeKind = 'success';
      hostname = '';
      onChanged();
      schedule();
    } catch (cause) {
      error = message(cause, 'The public endpoint could not be created.');
      // A bare domain is the most common mistake: offer the subdomain.
      hostnameSuggestion = /^[a-z0-9-]+\.[a-z0-9-]+$/i.test(hostname.trim()) ? `hooks.${hostname.trim().toLowerCase()}` : '';
    } finally {
      token = '';
      saving = false;
    }
  }

  async function runTest() {
    if (!active || testing) return;
    testing = true;
    error = '';
    try {
      const result = await api.testPublicEndpoint(active.id);
      if (view) view = { ...view, last_test: result };
    } catch (cause) {
      error = message(cause, 'The public URL could not be tested.');
    } finally {
      testing = false;
    }
  }

  async function disable() {
    if (!active || saving) return;
    saving = true;
    error = '';
    try {
      const result = await api.disablePublicEndpoint(active.id, disableToken.trim());
      notice = result.warning || `Public URL https://${result.endpoint.hostname} removed.`;
      noticeKind = result.warning ? 'warning' : 'success';
      disableOpen = false;
      onChanged();
      await refresh();
    } catch (cause) {
      error = message(cause, 'The public endpoint could not be disabled.');
    } finally {
      disableToken = '';
      saving = false;
    }
  }

  async function finishCleanup(endpoint: PublicEndpoint) {
    if (!cleanupToken.trim() || saving) return;
    saving = true;
    error = '';
    try {
      const result = await api.disablePublicEndpoint(endpoint.id, cleanupToken.trim());
      notice = result.warning || `Deleted the tunnel and DNS record for ${endpoint.hostname} in Cloudflare.`;
      noticeKind = result.warning ? 'warning' : 'success';
      cleanupId = '';
      onChanged();
      await refresh();
    } catch (cause) {
      error = message(cause, 'Cloudflare cleanup failed.');
    } finally {
      cleanupToken = '';
      saving = false;
    }
  }

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      copied = label;
      setTimeout(() => { if (copied === label) copied = ''; }, 2000);
    } catch {
      copied = '';
    }
  }

  function exposedPaths(pattern: string): string[] {
    const match = pattern.match(/\(([^)]+)\)/);
    return match ? match[1].split('|').map((path) => `/api/v1/${path}/…`) : [pattern];
  }

  function historyMeta(item: PublicEndpoint): string {
    const created = `Created ${formatRelative(item.created_at)}${item.created_by_name ? ` by ${item.created_by_name}` : ''}`;
    return item.disabled_at ? `${created} · removed ${formatRelative(item.disabled_at)}` : created;
  }

  function connectionSummary(connector: PublicEndpointView['connector'] | undefined): string {
    if (!connector) return '';
    if (connector.state !== 'connected') return connector.restarts ? `${connector.restarts} restart${connector.restarts === 1 ? '' : 's'}` : '';
    const edges = `${connector.connections} edge connection${connector.connections === 1 ? '' : 's'}`;
    return connector.locations?.length ? `${edges} · ${connector.locations.join(', ')}` : edges;
  }

  onMount(refresh);
  onDestroy(() => {
    destroyed = true;
    clearTimeout(poll);
  });
</script>

<section class="public-endpoint" aria-labelledby="public-endpoint-heading">
  <div class="public-heading">
    <h3 id="public-endpoint-heading">Public URL</h3>
    {#if active && connector}
      <span class={`status-badge badge-${connector.state}`} data-public-badge>{badgeLabels[connector.state] || connector.state}</span>
    {:else if view && !unavailable}
      <span class="status-badge badge-off">Off</span>
    {/if}
    <button class="text-button learn-more" type="button" on:click={() => openHelp('public-access', 'public-url-cloudflare-tunnel')}>Learn more</button>
  </div>

  {#if unavailable}
    <p class="optional">{unavailable}</p>
  {:else if active && connector}
    <div class="public-url-row">
      <a data-public-hostname href={`https://${active.hostname}`} target="_blank" rel="noreferrer">https://{active.hostname}</a>
      <button class="text-button" type="button" on:click={() => copy('host', `https://${active?.hostname}`)}>{copied === 'host' ? 'Copied' : 'Copy'}</button>
    </div>
    <p class="optional">Outside apps reach this Helm here. Only webhook paths are public; everything else returns 404.</p>

    <dl class="public-facts">
      <div>
        <dt>Connector</dt>
        <dd><span class={`connector-state state-${connector.state}`} data-connector-state>{stateLabels[connector.state] || connector.state}</span>{#if connector.state === 'connected' && connector.connected_at}{' '}<span class="optional">· since {formatRelative(connector.connected_at)}</span>{/if}</dd>
        {#if connectionSummary(connector)}<dd class="optional" data-connector-edges>{connectionSummary(connector)}</dd>{/if}
        {#if connector.message}<dd class="connector-error" data-connector-error>{connector.message}{#if connector.last_error_at}{' '}<span class="optional">· {formatRelative(connector.last_error_at)}</span>{/if}</dd>{/if}
      </div>
      <div>
        <dt>Last test</dt>
        {#if lastTest}
          <dd class={lastTest.ok ? 'test-ok' : 'test-failed'} data-test-result={lastTest.ok ? 'ok' : 'failed'}>{lastTest.ok ? '✓ Working' : '✕ Failed'}{' '}<span class="optional">· {formatRelative(lastTest.checked_at)}</span></dd>
          <dd class="optional" data-test-detail>{lastTest.ok ? `${lastTest.latency_ms < 1 ? '<1' : lastTest.latency_ms} ms round trip${lastTest.via_cloudflare ? ' via Cloudflare' : ''}` : lastTest.message}</dd>
        {:else}
          <dd class="optional">Not tested yet</dd>
        {/if}
      </div>
      <div>
        <dt>Created</dt>
        <dd>{formatRelative(active.created_at)}{#if active.created_by_name}{' '}<span class="optional">by {active.created_by_name}</span>{/if}</dd>
      </div>
    </dl>

    {#if view?.public_hook_base}
      <div class="public-url-row hook-base">
        <span class="optional">Webhook URLs start with</span>
        <code data-public-hook-base>{view.public_hook_base}…</code>
        <button class="text-button" type="button" on:click={() => view?.public_hook_base && copy('base', view.public_hook_base)}>{copied === 'base' ? 'Copied' : 'Copy'}</button>
      </div>
    {/if}

    <details class="public-resources">
      <summary>Cloudflare resources and exposed paths</summary>
      <dl>
        <div><dt>Tunnel</dt><dd><code data-tunnel-id>{active.tunnel_id}</code>{' '}<button class="text-button" type="button" on:click={() => active && copy('tunnel', active.tunnel_id)}>{copied === 'tunnel' ? 'Copied' : 'Copy'}</button></dd></div>
        <div><dt>DNS record</dt><dd><code>{active.dns_record_id}</code>{' '}<span class="optional">CNAME {active.hostname} → {active.tunnel_id}.cfargotunnel.com</span></dd></div>
        <div><dt>Zone · account</dt><dd><code>{active.zone_id}</code> · <code>{active.account_id}</code></dd></div>
        <div><dt>Public paths</dt><dd class="path-list">{#each exposedPaths(view?.exposed_paths || '') as path}<code>{path}</code>{/each}<span class="optional">Everything else: 404 at Cloudflare.</span></dd></div>
      </dl>
    </details>

    {#if disableOpen}
      <form class="public-form" on:submit|preventDefault={disable}>
        <label>Cloudflare API token <span class="optional">Optional — also deletes the DNS record and tunnel</span><input type="password" autocomplete="off" bind:value={disableToken} /></label>
        <div class="public-actions"><button class="text-button" type="button" on:click={() => { disableOpen = false; disableToken = ''; }}>Cancel</button><button class="button danger-button" type="submit" disabled={saving}>{saving ? 'Removing…' : 'Remove public URL'}</button></div>
      </form>
    {:else}
      <div class="public-actions">
        <button class="button quiet-button" type="button" on:click={runTest} disabled={testing}>{testing ? 'Testing…' : 'Test public URL'}</button>
        <button class="button quiet-button" type="button" on:click={() => { disableOpen = true; }}>Remove public URL…</button>
      </div>
    {/if}
  {:else if view}
    <p>Not set up. <strong>Connect Cloudflare</strong> above does it for you; experts can set it up by hand.</p>
    {#if !view.connector_available}<div class="inline-alert warning" role="note"><span>!</span>cloudflared is not installed on this server, so a public URL cannot run here. The Docker image includes it; other installs need it on PATH or HELM_CLOUDFLARED_BINARY.</div>{/if}
    <details class="manual-setup" data-manual-public>
      <summary>Set up manually</summary>
      <ol class="manual-steps">
        <li>Create a Cloudflare API token with the permissions below (the <strong>Create token on Cloudflare</strong> button in Connect Cloudflare pre-selects them).</li>
        <li>Enter a <strong>new subdomain</strong> such as <code>hooks.example.com</code>, not the domain itself: Helm creates a DNS record for it.</li>
        <li>Paste the token and select <strong>Create public URL</strong>. The token is used once and never stored.</li>
      </ol>
      <form class="public-form" on:submit|preventDefault={create}>
        <label>Hostname<input aria-label="Public hostname" bind:value={hostname} placeholder="hooks.example.com" autocomplete="off" required /></label>
        <label>Cloudflare API token<input aria-label="Cloudflare API token" type="password" bind:value={token} autocomplete="off" required /></label>
        <button class="button primary" type="submit" disabled={!hostname.trim() || !token.trim() || saving}>{saving ? 'Creating tunnel…' : 'Create public URL'}</button>
      </form>
      {#if hostnameSuggestion}<button class="text-button" type="button" data-hostname-suggestion on:click={() => { hostname = hostnameSuggestion; hostnameSuggestion = ''; error = ''; }}>Use {hostnameSuggestion} instead</button>{/if}
      <details>
        <summary>Token permissions</summary>
        <ul>{#each view.required_permissions as permission}<li>{permission}</li>{/each}</ul>
        <p class="optional">Exposed paths: {exposedPaths(view.exposed_paths).join(' and ')}. The server needs outbound access to Cloudflare (port 7844) and the <code>cloudflared</code> binary.</p>
      </details>
    </details>
  {/if}

  {#if earlier.length}
    <details class="public-history" open={pendingCleanup.length > 0}>
      <summary>Earlier public URLs ({earlier.length}){#if pendingCleanup.length}{' '}<span class="cleanup-count">· {pendingCleanup.length} need{pendingCleanup.length === 1 ? 's' : ''} Cloudflare cleanup</span>{/if}</summary>
      <ul>
        {#each earlier as item (item.id)}
          <li data-history-row={item.hostname}>
            <div class="history-main">
              <span class="history-host">{item.hostname}</span>
              <span class={`status-badge ${item.cleanup_pending ? 'badge-restarting' : 'badge-off'}`}>{item.cleanup_pending ? 'Needs cleanup' : 'Removed'}</span>
              <span class="optional">{historyMeta(item)}</span>
              {#if item.cleanup_pending && cleanupId !== item.id}
                <button class="text-button" type="button" on:click={() => { cleanupId = item.id; cleanupToken = ''; }}>Finish cleanup…</button>
              {/if}
            </div>
            {#if item.cleanup_pending}
              <p class="optional">Still in Cloudflare: tunnel <code>{item.tunnel_id}</code> and DNS record <code>{item.dns_record_id}</code>.</p>
              {#if cleanupId === item.id}
                <form class="public-form" on:submit|preventDefault={() => finishCleanup(item)}>
                  <label>Cloudflare API token<input aria-label={`Cloudflare API token for ${item.hostname}`} type="password" autocomplete="off" bind:value={cleanupToken} /></label>
                  <div class="public-actions"><button class="text-button" type="button" on:click={() => { cleanupId = ''; cleanupToken = ''; }}>Cancel</button><button class="button danger-button" type="submit" disabled={!cleanupToken.trim() || saving}>{saving ? 'Deleting…' : 'Delete in Cloudflare'}</button></div>
                </form>
              {/if}
            {/if}
          </li>
        {/each}
      </ul>
    </details>
  {/if}

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class={`inline-alert notice-${noticeKind}`} class:warning={noticeKind === 'warning'} role="status"><span>{noticeKind === 'warning' ? '!' : '✓'}</span>{notice}</div>{/if}
</section>

<style>
  .public-endpoint { display: grid; gap: 10px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .public-heading { display: flex; align-items: center; gap: 8px; }
  .learn-more { margin-left: auto; }
  .manual-setup { display: grid; gap: 8px; }
  .manual-setup[open] > summary { margin-bottom: 8px; }
  .manual-steps { margin: 0 0 8px; padding-left: 20px; font-size: 13px; line-height: 1.5; }
  .public-endpoint h3 { margin: 0; font-size: 14px; }
  .public-endpoint p { margin: 0; font-size: 13px; line-height: 1.5; }
  .status-badge { padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 800; }
  .badge-connected { color: var(--semantic-green); background: var(--green-soft); }
  .badge-starting { color: var(--semantic-amber); background: var(--amber-soft); }
  .badge-restarting, .badge-error { color: var(--semantic-red); background: var(--red-soft); }
  .badge-stopped, .badge-off { color: var(--muted); background: var(--surface); border: 1px solid var(--border); }
  .public-url-row { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; min-width: 0; }
  .public-url-row a { font-size: 15px; font-weight: 800; color: var(--ink); overflow-wrap: anywhere; }
  .hook-base code { overflow-wrap: anywhere; font-size: 12px; }
  .public-facts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin: 0; }
  .public-facts > div { display: grid; align-content: start; gap: 3px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  dt { color: var(--muted); font-size: 11px; font-weight: 800; text-transform: uppercase; letter-spacing: 0.04em; }
  dd { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .connector-state { font-weight: 800; }
  .state-connected { color: var(--semantic-green); }
  .state-starting { color: var(--semantic-amber); }
  .state-restarting, .state-error { color: var(--semantic-red); }
  .connector-error { color: var(--semantic-red); font-size: 12px; }
  .test-ok { color: var(--semantic-green); font-weight: 800; }
  .test-failed { color: var(--semantic-red); font-weight: 800; }
  .public-resources dl { display: grid; gap: 6px; margin: 8px 0 0; }
  .public-resources dl > div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: 8px; align-items: baseline; }
  .public-form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 10px; align-items: end; }
  .public-form label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .public-actions { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
  .danger-button { color: var(--semantic-red); border-color: color-mix(in srgb, var(--semantic-red), var(--border) 60%); background: var(--red-soft); }
  .path-list { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: baseline; }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  details { font-size: 12px; }
  details summary { cursor: pointer; font-weight: 700; }
  details ul { margin: 6px 0; padding-left: 18px; }
  .public-history ul { display: grid; gap: 8px; padding: 0; list-style: none; }
  .public-history li { display: grid; gap: 6px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  .history-main { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .history-host { font-weight: 800; overflow-wrap: anywhere; }
  .cleanup-count { color: var(--semantic-red); }
  @media (max-width: 760px) {
    .public-form, .public-facts { grid-template-columns: minmax(0, 1fr); }
    .public-resources dl > div { grid-template-columns: minmax(0, 1fr); }
  }
</style>
