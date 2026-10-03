<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import type { CloudflareConnectStatus, CloudflareSetupRun, CloudflareZoneOption } from '../types';

  // Guided setup ("Connect Cloudflare"): connect once, pick a domain, and
  // Helm creates the public URL (and optionally email addresses) with a live
  // checklist. See docs/PUBLIC_ACCESS.md#connect-cloudflare-guided-setup.
  export let onChanged: () => void = () => undefined;

  const stepIcons: Record<string, string> = { pending: '○', running: '◌', done: '✓', skipped: '–', failed: '✕' };

  let status: CloudflareConnectStatus | null = null;
  let zones: CloudflareZoneOption[] = [];
  let zoneId = '';
  let hostname = '';
  let withEmail = true;
  let localPart = 'helm-alerts';
  let fallback = '';
  let consent = false;
  let token = '';
  let busy = '';
  let error = '';
  let notice = '';
  let run: CloudflareSetupRun | null = null;
  let open = true;
  let poll: ReturnType<typeof setTimeout> | undefined;

  $: zone = zones.find((item) => item.id === zoneId);
  $: emailPossible = zone?.email_routing === 'ready';
  $: needsConsent = Boolean(zone && emailPossible && withEmail && !zone.plus_addressing);
  $: canStart = Boolean(zone && (hostname.trim() || status?.active_public_hostname) && (!withEmail || !emailPossible || !needsConsent || consent) && !busy);

  function message(value: unknown, fallbackText: string): string {
    return value instanceof Error && value.message ? value.message : fallbackText;
  }

  async function load() {
    try {
      status = await api.getCloudflareConnect();
      // Pick up a run still in progress (e.g. after a reload); finished
      // runs are shown only to the session that watched them.
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
    withEmail = next.email_routing === 'ready';
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
      token = '';
      notice = 'Connected with your token. Choose a domain below.';
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

  function reset() {
    run = null;
    zones = [];
    zoneId = '';
    void load();
  }

  onMount(async () => {
    const url = new URL(window.location.href);
    const connected = url.searchParams.get('cloudflare');
    const failed = url.searchParams.get('cloudflare_error');
    if (connected || failed) {
      if (connected === 'connected') notice = 'Signed in with Cloudflare. Choose a domain below.';
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

<section class="cf-setup" aria-labelledby="cf-setup-heading" data-cloudflare-setup>
  <div class="cf-heading">
    <div>
      <h3 id="cf-setup-heading">Connect Cloudflare</h3>
      <p>The easiest way to let outside apps reach Helm: connect once, pick a domain, and Helm sets up the public URL and email addresses for you.</p>
    </div>
    <div class="cf-heading-actions">
      <button class="text-button" type="button" on:click={() => openHelp('public-access', 'connect-cloudflare-guided-setup')} data-help-link>Learn more</button>
      <button class="text-button" type="button" aria-expanded={open} on:click={() => { open = !open; }}>{open ? 'Hide' : 'Show'}</button>
    </div>
  </div>

  {#if open && status}
    {#if run && (run.status === 'running' || run.finished_at)}
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
            <p class="cf-result" data-setup-result>Outside apps can reach this Helm at <strong>{run.public_url}</strong>{#if run.email_base}{' '}and webhooks can have addresses like <strong>{run.email_base}</strong>{/if}. Next: create a webhook below and select <strong>Send test</strong> on it.</p>
          {/if}
          <button class="button quiet-button" type="button" on:click={reset}>{run.status === 'done' ? 'Done' : 'Try again'}</button>
        {/if}
      </div>
    {:else}
      <ol class="cf-flow">
        <li class:current={!status.connected}>
          <h4>1. Connect your Cloudflare account</h4>
          {#if status.connected}
            <p class="cf-connected" data-cloudflare-connected={status.connected_via}>✓ Connected {status.connected_via === 'cloudflare' ? 'with Cloudflare sign-in' : 'with your token'}{#if status.expires_at}{' '}<span class="optional">· Helm forgets it {formatRelative(status.expires_at)} or after setup</span>{/if}
              <button class="text-button" type="button" on:click={disconnect} disabled={busy === 'disconnect'}>Disconnect</button></p>
          {:else}
            {#if status.oauth_available}
              <button class="button primary" type="button" on:click={signIn} disabled={Boolean(busy)} data-cloudflare-signin>{busy === 'oauth' ? 'Opening Cloudflare…' : 'Sign in with Cloudflare'}</button>
              <p class="optional">Cloudflare asks which account Helm may use and shows the permissions. Afterwards a short page shows where you are being returned; check it is this Helm and continue. If Cloudflare says the app is not available to your account, use a token below.</p>
              <p class="cf-or">or use an API token</p>
            {/if}
            <ol class="cf-token-steps">
              <li><a class="button quiet-button" href={status.token_link} target="_blank" rel="noopener noreferrer" data-cloudflare-token-link>Create token on Cloudflare ↗</a> It opens with every permission Helm needs already selected.</li>
              <li>On that page choose your <strong>account</strong>, and under <strong>Zone resources</strong> your <strong>domain</strong>. Then <strong>Continue to summary → Create token</strong>.</li>
              <li>Copy the token and paste it here:
                <form class="cf-token-form" on:submit|preventDefault={connectToken}>
                  <label class="sr-only" for="cf-token">Cloudflare API token</label>
                  <input id="cf-token" type="password" autocomplete="off" bind:value={token} placeholder="Cloudflare API token" />
                  <button class="button primary" type="submit" disabled={!token.trim() || Boolean(busy)}>{busy === 'token' ? 'Checking…' : 'Connect'}</button>
                </form>
              </li>
            </ol>
            <details class="optional"><summary>Permissions the token gets</summary><ul>{#each status.token_permissions as permission (permission.key)}<li>{permission.label}</li>{/each}</ul><p>Helm keeps the token in memory for at most 30 minutes and never stores it.</p></details>
          {/if}
        </li>

        <li class:current={status.connected} class:locked={!status.connected}>
          <h4>2. Choose a domain and what to set up</h4>
          {#if !status.connected}
            <p class="optional">Connect first.</p>
          {:else if busy === 'zones'}
            <p class="optional">Loading your domains…</p>
          {:else if zones.length}
            <div class="cf-zones" role="radiogroup" aria-label="Domain">
              {#each zones as item (item.id)}
                <label class="cf-zone" class:selected={item.id === zoneId} class:unusable={!item.usable} data-zone={item.name} title={item.reason || ''}>
                  <input type="radio" name="cf-zone" checked={item.id === zoneId} disabled={!item.usable} on:change={() => pick(item)} />
                  <span class="zone-name">{item.name}</span>
                  {#if item.account_name}<span class="optional">{item.account_name}</span>{/if}
                  {#if item.usable}
                    <span class={`zone-badge email-${item.email_routing}`}>{item.email_routing === 'ready' ? 'Email Routing on' : item.email_routing === 'off' ? 'Email Routing off' : 'Email Routing unknown'}</span>
                  {:else}
                    <span class="zone-badge" data-zone-unusable>No DNS access</span>
                  {/if}
                </label>
              {/each}
            </div>
            {#if zone}
              {#if status.active_public_hostname}
                <p class="cf-existing" data-setup-existing>Your public URL <strong>https://{status.active_public_hostname}</strong> is already working; setup keeps it as is.</p>
              {:else}
                <label class="cf-field">Public hostname<input aria-label="Guided public hostname" bind:value={hostname} autocomplete="off" /></label>
                <p class="optional">A new name Helm creates in {zone.name}. Only webhook paths become public on it.</p>
              {/if}
              <label class="cf-check"><input type="checkbox" bind:checked={withEmail} disabled={!emailPossible} data-setup-email /> Also give webhooks email addresses</label>
              {#if !emailPossible}
                <p class="optional">Turn on Email Routing for {zone.name} in Cloudflare (Email › Email Routing) to add email; you can run setup again later.</p>
              {:else if withEmail}
                <div class="cf-email">
                  <label class="cf-field">Address name<input aria-label="Guided email address name" bind:value={localPart} autocomplete="off" /></label>
                  <label class="cf-field">Fallback (optional)<input aria-label="Guided fallback email address" bind:value={fallback} placeholder="you@example.com" autocomplete="off" /></label>
                </div>
                <p class="optional">Addresses will look like <code>{localPart || 'helm-alerts'}+&lt;tag&gt;@{zone.name}</code>.</p>
                {#if needsConsent}
                  <label class="cf-check consent" data-setup-consent><input type="checkbox" bind:checked={consent} /> Turn on plus addressing for {zone.name}. Mail to any <code>name+anything@{zone.name}</code> will then also reach <code>name@{zone.name}</code>.</label>
                {/if}
              {/if}
              <button class="button primary" type="button" on:click={start} disabled={!canStart} data-setup-start>Set up public access</button>
            {/if}
          {:else}
            <button class="button quiet-button" type="button" on:click={loadZones}>Load my domains</button>
          {/if}
        </li>
      </ol>
    {/if}
    {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
    {#if notice}<div class="inline-alert notice-success" role="status"><span>✓</span>{notice}</div>{/if}
  {/if}
</section>

<style>
  .cf-setup { display: grid; gap: 12px; padding: 14px; border: 1px solid color-mix(in srgb, var(--purple), var(--border) 65%); border-radius: 12px; background: var(--surface); }
  .cf-heading { display: flex; justify-content: space-between; gap: 12px; align-items: flex-start; }
  .cf-heading h3 { margin: 0; font-size: 15px; }
  .cf-heading p { margin: 4px 0 0; font-size: 13px; color: var(--ink-soft); }
  .cf-heading-actions { display: flex; gap: 12px; flex: 0 0 auto; }
  .cf-flow, .cf-steps { display: grid; gap: 10px; margin: 0; padding: 0; list-style: none; }
  .cf-flow > li { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .cf-flow > li.locked { opacity: 0.6; }
  .cf-flow h4, .cf-run h4 { margin: 0; font-size: 13px; }
  .cf-flow p, .cf-run p { margin: 0; font-size: 13px; line-height: 1.5; }
  .cf-token-steps { display: grid; gap: 8px; margin: 0; padding-left: 20px; font-size: 13px; }
  .cf-token-steps .button { margin-right: 6px; }
  .cf-token-form { display: flex; gap: 8px; margin-top: 6px; }
  .cf-token-form input { flex: 1; min-width: 0; }
  .cf-or { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  .cf-connected { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: baseline; color: var(--semantic-green); font-weight: 700; }
  .cf-zones { display: grid; gap: 6px; }
  .cf-zone { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); cursor: pointer; font-size: 13px; }
  .cf-zone.selected { border-color: var(--purple); }
  .cf-zone.unusable { cursor: not-allowed; opacity: 0.6; }
  .cf-existing { padding: 8px 10px; border-radius: 8px; background: var(--green-soft); }
  .zone-name { font-weight: 800; }
  .zone-badge { margin-left: auto; padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: 700; color: var(--muted); background: var(--surface-muted); }
  .zone-badge.email-ready { color: var(--semantic-green); background: var(--green-soft); }
  .cf-field { display: grid; gap: 4px; font-size: 12px; font-weight: 700; }
  .cf-email { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
  .cf-check { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .cf-check input, .cf-zone input { width: auto; height: auto; padding: 0; flex: 0 0 auto; margin: 3px 0 0; }
  [data-cloudflare-signin] { justify-self: start; }
  .cf-check.consent { padding: 8px 10px; border-radius: 8px; background: var(--amber-soft); }
  .cf-steps li { display: flex; gap: 10px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface-muted); font-size: 13px; }
  .cf-steps p { margin: 2px 0 0; color: var(--ink-soft); }
  .step-icon { width: 18px; flex: 0 0 18px; font-weight: 800; text-align: center; color: var(--muted); }
  .step-done .step-icon { color: var(--semantic-green); }
  .step-failed .step-icon, .step-failed strong { color: var(--semantic-red); }
  .step-running .step-icon { color: var(--purple); }
  .cf-run { display: grid; gap: 10px; }
  .cf-result { padding: 10px 12px; border-radius: 8px; background: var(--green-soft); }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  details ul { margin: 6px 0; padding-left: 18px; }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
  @media (max-width: 760px) { .cf-email { grid-template-columns: minmax(0, 1fr); } .cf-heading { flex-direction: column; } }
</style>
