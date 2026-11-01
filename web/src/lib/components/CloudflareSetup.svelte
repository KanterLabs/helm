<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import '../../styles/cloudflare-setup.css';
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
  /** Unsaved user input, including a token hidden by its disclosure. */
  export let dirty = false;

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
  let inboxName = 'Alerts';
  let token = '';
  let tokenOpen = false;
  let optionsEdited = false;
  let busy = '';
  let error = '';
  let notice = '';
  let run: CloudflareSetupRun | null = null;
  let poll: ReturnType<typeof setTimeout> | undefined;

  $: dirty = Boolean(token || optionsEdited);

  function edited(event: Event) {
    // Defaults loaded from Cloudflare are clean. Only browser input events
    // mark the setup options; token lifetime is tracked by its value.
    if (event.target instanceof HTMLInputElement && event.target.type !== 'password') optionsEdited = true;
  }

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
      optionsEdited = false;
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
        email: withEmail && emailPossible ? { local_part: localPart.trim() || 'helm-alerts', fallback_address: fallback.trim() || undefined, enable_subaddressing: consent, inbox_name: inboxName.trim() || 'Alerts', assign_to_me: true } : undefined
      });
      optionsEdited = false;
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

<div class="cf-setup" data-cloudflare-setup on:input={edited} on:change={edited}>
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
          <div class="cf-result" data-setup-result>
            <p>Outside apps can reach this Helm at <strong>{run.public_url}</strong>.</p>
            {#if run.inbox_address}
              <p>Your inbox <strong>{run.inbox_name}</strong>: <code data-setup-inbox>{run.inbox_address}</code>. Point your apps' alert emails at it; each email becomes a ticket. Send one now to try it.</p>
            {/if}
          </div>
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

          <label class="cf-check"><input type="checkbox" bind:checked={withEmail} disabled={!emailPossible} data-setup-email /> Turn on email and create an inbox</label>
          {#if !emailPossible}
            <p class="optional">Turn on Email Routing for {zone.name} in Cloudflare (Email › Email Routing) to add email later.</p>
          {:else if withEmail}
            <div class="cf-inbox">
              <label class="cf-field">Inbox name<input aria-label="Guided inbox name" bind:value={inboxName} autocomplete="off" /></label>
            </div>
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
