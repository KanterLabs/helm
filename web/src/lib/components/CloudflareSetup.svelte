<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import type { CloudflareConnectStatus, CloudflareSetupRun, CloudflareZoneOption } from '../types';

  // Guided setup: sign in with Cloudflare (or use a token), confirm the
  // domain, and Helm creates the public URL and email addresses with a live
  // checklist. See docs/PUBLIC_ACCESS.md#set-up-with-cloudflare.
  export let onChanged: () => void = () => undefined;
  /** Called when the admin closes a finished checklist. */
  export let onFinished: () => void = () => undefined;
  /** Pre-ticks email (e.g. "Add email addresses"). */
  export let preferEmail = true;

  const stepIcons: Record<string, string> = { pending: '○', running: '◌', done: '✓', skipped: '–', failed: '✕' };

  let status: CloudflareConnectStatus | null = null;
  let zones: CloudflareZoneOption[] = [];
  let zoneId = '';
  let hostname = '';
  let editingHostname = false;
  let withEmail = true;
  let localPart = 'helm-alerts';
  let fallback = '';
  let consent = false;
  let token = '';
  let tokenOpen = false;
  let busy = '';
  let error = '';
  let notice = '';
  let run: CloudflareSetupRun | null = null;
  let poll: ReturnType<typeof setTimeout> | undefined;

  $: usableZones = zones.filter((item) => item.usable);
  $: otherZones = zones.filter((item) => !item.usable);
  $: zone = zones.find((item) => item.id === zoneId);
  $: emailPossible = zone?.email_routing === 'ready';
  $: needsConsent = Boolean(zone && emailPossible && withEmail && !zone.plus_addressing);
  $: canStart = Boolean(zone && zone.usable && (hostname.trim() || status?.active_public_hostname) && (!withEmail || !emailPossible || !needsConsent || consent) && !busy);
  $: if (status && !status.oauth_available) tokenOpen = true;

  function message(value: unknown, fallbackText: string): string {
    return value instanceof Error && value.message ? value.message : fallbackText;
  }

  async function load() {
    try {
      status = await api.getCloudflareConnect();
      // Pick up a run still in progress (e.g. after a reload).
      if (status.run?.status === 'running' && (!run || run.id === status.run.id)) run = status.run;
      if (status.connected && !zones.length) await loadZones();
    } catch (cause) {
      error = message(cause, 'Cloudflare setup status could not be loaded.');
    }
  }

  async function loadZones() {
    busy = 'zones';
    try {
      zones = (await api.listCloudflareZones()).data;
      const usable = zones.filter((item) => item.usable);
      const preferred = usable.find((item) => item.id === zoneId) || usable.find((item) => item.email_routing === 'ready') || usable[0];
      if (preferred) pick(preferred);
    } catch (cause) {
      error = message(cause, 'Your domains could not be listed.');
    } finally {
      busy = '';
    }
  }

  function pick(next: CloudflareZoneOption) {
    zoneId = next.id;
    hostname = next.suggested_hostname || `hooks.${next.name}`;
    editingHostname = false;
    withEmail = preferEmail && next.email_routing === 'ready';
    consent = false;
  }

  async function signIn() {
    busy = 'oauth';
    error = '';
    try {
      window.location.assign((await api.startCloudflareOAuth()).authorize_url);
    } catch (cause) {
      error = message(cause, 'Sign-in could not start.');
      busy = '';
    }
  }

  async function connectToken() {
    if (!token.trim()) return;
    busy = 'token';
    error = '';
    try {
      status = await api.connectCloudflareToken(token.trim());
      notice = 'Connected with your token.';
      await loadZones();
    } catch (cause) {
      error = message(cause, 'The token could not be used.');
    } finally {
      token = '';
      if (busy === 'token') busy = '';
    }
  }

  async function disconnect() {
    busy = 'disconnect';
    try {
      status = await api.disconnectCloudflare();
      zones = [];
      zoneId = '';
      notice = 'Disconnected. Helm no longer holds the Cloudflare credential.';
    } finally {
      busy = '';
    }
  }

  async function start() {
    if (!zone || !canStart) return;
    busy = 'setup';
    error = '';
    notice = '';
    try {
      run = await api.startCloudflareSetup({
        zone: zone.id,
        hostname: hostname.trim(),
        email: withEmail && emailPossible ? { local_part: localPart.trim() || 'helm-alerts', fallback_address: fallback.trim() || undefined, enable_subaddressing: consent } : undefined
      });
      schedule();
    } catch (cause) {
      error = message(cause, 'Setup could not start.');
    } finally {
      busy = '';
    }
  }

  function schedule() {
    clearTimeout(poll);
    if (!run || run.status !== 'running') return;
    poll = setTimeout(async () => {
      if (!run) return;
      try {
        run = await api.getCloudflareSetup(run.id);
      } catch {
        // Keep polling; a transient error must not hide the checklist.
      }
      if (run && run.status !== 'running') {
        onChanged();
        await load();
      }
      schedule();
    }, 1500);
  }

  function finish() {
    const succeeded = run?.status === 'done';
    run = null;
    zones = [];
    zoneId = '';
    void load();
    if (succeeded) onFinished();
  }

  onMount(async () => {
    const url = new URL(window.location.href);
    const connected = url.searchParams.get('cloudflare');
    const failed = url.searchParams.get('cloudflare_error');
    if (connected || failed) {
      if (connected === 'connected') notice = 'Signed in with Cloudflare.';
      if (failed) error = failed;
      url.searchParams.delete('cloudflare');
      url.searchParams.delete('cloudflare_error');
      window.history.replaceState({}, '', url.pathname + url.search);
    }
    await load();
    if (run?.status === 'running') schedule();
  });
  onDestroy(() => clearTimeout(poll));
</script>

<div class="cf-setup" data-cloudflare-setup>
  {#if !status}
    <p class="optional">Loading…</p>
  {:else if run}
    <div class="cf-run" data-setup-run={run.status}>
      <h4>{run.status === 'running' ? `Setting up ${run.hostname}…` : run.status === 'done' ? 'Public access is ready' : 'Setup stopped'}</h4>
      <ol class="cf-steps">
        {#each run.steps as step (step.id)}
          <li class={`step-${step.status}`} data-setup-step={step.id} data-setup-status={step.status}>
            <span class="step-icon" aria-hidden="true">{stepIcons[step.status]}</span>
            <div><strong>{step.label}</strong>{#if step.detail}<p>{step.detail}</p>{/if}</div>
          </li>
        {/each}
      </ol>
      {#if run.status !== 'running'}
        {#if run.public_url}
          <p class="cf-result" data-setup-result>Outside apps can reach this Helm at <strong>{run.public_url}</strong>{#if run.email_base}{' '}and webhooks can have addresses like <strong>{run.email_base}</strong>{/if}. Next: create a webhook below and use <strong>Send test</strong>{#if run.email_base}{' '}and <strong>Test email</strong>{/if} on it.</p>
        {/if}
        <button class="button quiet-button" type="button" on:click={finish}>{run.status === 'done' ? 'Done' : 'Try again'}</button>
      {/if}
    </div>
  {:else if !status.connected}
    <div class="cf-start">
      {#if status.oauth_available}
        <button class="button primary cf-signin" type="button" on:click={signIn} disabled={Boolean(busy)} data-cloudflare-signin>{busy === 'oauth' ? 'Opening Cloudflare…' : 'Sign in with Cloudflare'}</button>
        <p class="optional">You choose the account and approve the permissions at Cloudflare, then confirm you are returning to this Helm. Helm sets everything up and forgets the sign-in.</p>
        <button class="text-button" type="button" aria-expanded={tokenOpen} on:click={() => { tokenOpen = !tokenOpen; }} data-token-toggle>{tokenOpen ? 'Hide the token option' : 'Use an API token instead'}</button>
      {/if}
      {#if tokenOpen}
        <ol class="cf-token-steps">
          <li><a class="button quiet-button" href={status.token_link} target="_blank" rel="noopener noreferrer" data-cloudflare-token-link>Create token on Cloudflare ↗</a> It opens with every permission Helm needs already selected.</li>
          <li>On that page choose your <strong>account</strong>, and under <strong>Zone resources</strong> your <strong>domain</strong>. Then <strong>Continue to summary → Create token</strong>.</li>
          <li>Paste the token:
            <form class="cf-token-form" on:submit|preventDefault={connectToken}>
              <label class="sr-only" for="cf-token">Cloudflare API token</label>
              <input id="cf-token" type="password" autocomplete="off" bind:value={token} placeholder="Cloudflare API token" />
              <button class="button primary" type="submit" disabled={!token.trim() || Boolean(busy)}>{busy === 'token' ? 'Checking…' : 'Connect'}</button>
            </form>
          </li>
        </ol>
      {/if}
    </div>
  {:else}
    <div class="cf-choose">
      <p class="cf-connected" data-cloudflare-connected={status.connected_via}>✓ Connected {status.connected_via === 'cloudflare' ? 'with Cloudflare sign-in' : 'with your token'}
        <button class="text-button" type="button" on:click={disconnect} disabled={busy === 'disconnect'}>Disconnect</button></p>
      {#if busy === 'zones'}
        <p class="optional">Loading your domains…</p>
      {:else if !zones.length}
        <button class="button quiet-button" type="button" on:click={loadZones}>Load my domains</button>
      {:else}
        {#if usableZones.length > 1 || !usableZones.length}
          <fieldset class="cf-zones">
            <legend>Domain</legend>
            {#each (usableZones.length ? usableZones : zones) as item (item.id)}
              <label class="cf-zone" class:selected={item.id === zoneId} class:unusable={!item.usable} data-zone={item.name} title={item.reason || ''}>
                <input type="radio" name="cf-zone" checked={item.id === zoneId} disabled={!item.usable} on:change={() => pick(item)} />
                <span class="zone-name">{item.name}</span>
                {#if item.usable}
                  <span class={`zone-badge email-${item.email_routing}`}>{item.email_routing === 'ready' ? 'Email Routing on' : item.email_routing === 'off' ? 'Email Routing off' : 'Email Routing unknown'}</span>
                {:else}
                  <span class="zone-badge" data-zone-unusable>No DNS access</span>
                {/if}
              </label>
            {/each}
          </fieldset>
          {#if !usableZones.length}<p class="optional">This connection cannot manage DNS in any of your domains. Create the token again and include your domain under <strong>Zone resources</strong>.</p>{/if}
        {:else if zone}
          <p class="cf-line" data-zone={zone.name}>Domain <strong>{zone.name}</strong></p>
        {/if}
        {#if usableZones.length && otherZones.length}
          <p class="optional" data-other-zones>{otherZones.length} other domain{otherZones.length === 1 ? '' : 's'} in your account cannot be used with this connection (no DNS access).</p>
        {/if}

        {#if zone && zone.usable}
          {#if status.active_public_hostname}
            <p class="cf-line cf-existing" data-setup-existing>Public URL <strong>https://{status.active_public_hostname}</strong> is already working; setup keeps it.</p>
          {:else if editingHostname}
            <label class="cf-field">Public hostname<input aria-label="Guided public hostname" bind:value={hostname} autocomplete="off" /></label>
            <p class="optional">A new, unused name in {zone.name}. Only webhook paths become public on it.</p>
          {:else}
            <p class="cf-line" data-setup-hostname>Public URL <strong>https://{hostname}</strong> <button class="text-button" type="button" on:click={() => { editingHostname = true; }}>Change</button></p>
          {/if}

          <label class="cf-check"><input type="checkbox" bind:checked={withEmail} disabled={!emailPossible} data-setup-email /> Give webhooks email addresses too</label>
          {#if !emailPossible}
            <p class="optional">Turn on Email Routing for {zone.name} in Cloudflare (Email › Email Routing) to add email later.</p>
          {:else if withEmail}
            {#if needsConsent}
              <label class="cf-check consent" data-setup-consent><input type="checkbox" bind:checked={consent} /> Turn on plus addressing for {zone.name}, so <code>name+anything@{zone.name}</code> also reaches <code>name@{zone.name}</code>.</label>
            {/if}
            <details class="cf-email-options">
              <summary>Email options: <code>{localPart || 'helm-alerts'}+…@{zone.name}</code>{fallback ? `, fallback ${fallback}` : ''}</summary>
              <div class="cf-email">
                <label class="cf-field">Address name<input aria-label="Guided email address name" bind:value={localPart} autocomplete="off" /></label>
                <label class="cf-field">Fallback (optional)<input aria-label="Guided fallback email address" bind:value={fallback} placeholder="you@example.com" autocomplete="off" /></label>
              </div>
              <p class="optional">The fallback receives mail Helm cannot accept (for example while it is down). It must be a verified destination in Cloudflare Email Routing.</p>
            </details>
          {/if}
          <button class="button primary" type="button" on:click={start} disabled={!canStart} data-setup-start>Set up</button>
        {/if}
      {/if}
      {#if status.expires_at}<p class="optional">Helm forgets this connection {formatRelative(status.expires_at)} or right after setup.</p>{/if}
    </div>
  {/if}
  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class="inline-alert notice-success" role="status"><span>✓</span>{notice}</div>{/if}
  <button class="text-button cf-help" type="button" on:click={() => openHelp('public-access', 'set-up-with-cloudflare')}>How this works</button>
</div>

<style>
  .cf-setup { display: grid; gap: 12px; }
  .cf-start, .cf-choose, .cf-run { display: grid; gap: 10px; justify-items: start; }
  .cf-choose > *, .cf-run > *, .cf-start > p, .cf-token-steps { justify-self: stretch; }
  .cf-signin { padding: 0 22px; height: 42px; font-size: 14px; }
  .cf-start p, .cf-choose p, .cf-run p { margin: 0; font-size: 13px; line-height: 1.5; }
  .cf-token-steps { display: grid; gap: 8px; margin: 0; padding-left: 20px; font-size: 13px; }
  .cf-token-steps .button { margin-right: 6px; }
  .cf-token-form { display: flex; gap: 8px; margin-top: 6px; }
  .cf-token-form input { flex: 1; min-width: 0; }
  .cf-connected { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: baseline; color: var(--semantic-green); font-weight: 700; }
  .cf-zones { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .cf-zones legend { margin-bottom: 4px; font-size: 12px; font-weight: 700; }
  .cf-zone { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); cursor: pointer; font-size: 13px; }
  .cf-zone.selected { border-color: var(--purple); }
  .cf-zone.unusable { cursor: not-allowed; opacity: 0.6; }
  .zone-name { font-weight: 800; }
  .zone-badge { margin-left: auto; padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: 700; color: var(--muted); background: var(--surface-muted); }
  .zone-badge.email-ready { color: var(--semantic-green); background: var(--green-soft); }
  .cf-line { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: baseline; }
  .cf-existing { padding: 8px 10px; border-radius: 8px; background: var(--green-soft); }
  .cf-field { display: grid; gap: 4px; font-size: 12px; font-weight: 700; }
  .cf-email { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-top: 8px; }
  .cf-email-options { font-size: 12px; }
  .cf-email-options summary { cursor: pointer; color: var(--ink-soft); }
  .cf-check { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .cf-check input, .cf-zone input { width: auto; height: auto; padding: 0; flex: 0 0 auto; margin: 3px 0 0; }
  .cf-check.consent { padding: 8px 10px; border-radius: 8px; background: var(--amber-soft); }
  .cf-steps { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .cf-steps li { display: flex; gap: 10px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); font-size: 13px; }
  .cf-steps p { margin: 2px 0 0; color: var(--ink-soft); }
  .cf-run h4 { margin: 0; font-size: 13px; }
  .step-icon { width: 18px; flex: 0 0 18px; font-weight: 800; text-align: center; color: var(--muted); }
  .step-done .step-icon { color: var(--semantic-green); }
  .step-failed .step-icon, .step-failed strong { color: var(--semantic-red); }
  .step-running .step-icon { color: var(--purple); }
  .cf-result { padding: 10px 12px; border-radius: 8px; background: var(--green-soft); }
  .cf-help { justify-self: start; }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  @media (max-width: 760px) { .cf-email { grid-template-columns: minmax(0, 1fr); } }
</style>
