<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../../api';
  import type { GithubConnection, GithubIntegration } from '../../types';
  import GitHubDeviceCode from './GitHubDeviceCode.svelte';
  import GitHubDisconnectConfirm from './GitHubDisconnectConfirm.svelte';
  import { announcement, connectionView, githubUrl, healthNote, NOT_HELMS_ANSWER, plainError, POLL_MS, polls, secondsLeft, timeLeft, type ConnectionView } from './connection';
  import { relativeTime } from './pullRequests';

  /**
   * Device-flow GitHub connection (contract C6). The server polls GitHub; this
   * panel polls Helm every 3 s only while an attempt is pending, the panel is
   * mounted and the page is visible, and never runs two polls at once.
   */
  export let integration: GithubIntegration;
  export let onChange: (next: GithubIntegration) => void;
  /** The view on screen, for the section's header badge. */
  export let view: ConnectionView = 'disconnected';

  const fallback: GithubConnection = { mode: 'device_flow', signin_available: false, state: 'disconnected' };
  let doc = integration;
  let busy: '' | 'connect' | 'cancel' | 'disconnect' | 'check' = '';
  let lastAction = '';
  let epoch = 0;
  let now = Date.now();
  let error = '';
  let pollNote = '';
  let checkNote = '';
  let live = '';
  let confirming = false;
  let shown: ConnectionView | '' = '';
  let warnedMinute = '';
  let root: HTMLElement;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let ticker: ReturnType<typeof setInterval> | undefined;
  let inFlight = false;
  let destroyed = false;

  $: if (integration !== doc) doc = integration;
  $: connection = doc.connection || fallback;
  $: attempt = connection.attempt || null;
  $: view = busy === 'connect' ? 'starting' : connectionView(connection, now);
  $: installUrl = githubUrl(connection.install_url);
  $: count = doc.allowed_repositories?.length || 0;
  $: health = healthNote(doc.state);
  $: summary = [
    `${count} ${count === 1 ? 'repository' : 'repositories'} available`,
    connection.connected_at ? `connected ${relativeTime(connection.connected_at)}` : '',
    relativeTime(doc.checked_at) === 'never' ? 'not checked yet' : `last checked ${relativeTime(doc.checked_at)}`
  ].filter(Boolean).join(' · ');
  $: void viewChanged(view);
  $: if (attempt && view === 'pending' && secondsLeft(attempt.expires_at, now) === 60 && warnedMinute !== attempt.id) {
    warnedMinute = attempt.id;
    say('One minute left to enter the code.');
  }

  function currentView(): ConnectionView {
    return busy === 'connect' ? 'starting' : connectionView(doc.connection || fallback, Date.now());
  }

  function say(text: string): void {
    live = '';
    void tick().then(() => { live = text; });
  }

  async function viewChanged(next: ConnectionView): Promise<void> {
    syncPolling();
    if (shown === next) return;
    const first = shown === '';
    shown = next;
    if (next !== 'connected' && next !== 'no_repositories') confirming = false;
    if (first) return;
    say(announcement(next, doc.connection || fallback, lastAction));
    await tick();
    const active = document.activeElement;
    // Move focus only when it was lost with the old view or is already ours.
    if (root && (!active || active === document.body || root.contains(active))) {
      root.querySelector<HTMLElement>('[data-github-view-heading]')?.focus({ preventScroll: !root.contains(active) });
    }
  }

  function syncPolling(): void {
    if (destroyed || !polls(currentView())) {
      clearTimeout(timer);
      timer = undefined;
      clearInterval(ticker);
      ticker = undefined;
      return;
    }
    if (!ticker) ticker = setInterval(() => { now = Date.now(); }, 1000);
    if (!timer && !inFlight && !document.hidden) timer = setTimeout(pollTick, POLL_MS);
  }

  async function pollTick(): Promise<void> {
    timer = undefined;
    if (destroyed || document.hidden) return;
    await pollOnce();
    syncPolling();
  }

  async function pollOnce(): Promise<void> {
    if (inFlight) return;
    inFlight = true;
    const started = epoch;
    try {
      const next = await api.getGithubIntegration();
      // An answer that began before the admin's last action is out of date.
      if (!destroyed && started === epoch) { pollNote = ''; apply(next); }
    } catch (failure) {
      if (!destroyed && started === epoch) {
        pollNote = plainError(failure, '') === NOT_HELMS_ANSWER
          ? 'Helm did not get a proper answer from its own server. It will check again in a few seconds.'
          : 'Helm could not check just now. It will check again in a few seconds.';
      }
    } finally {
      inFlight = false;
    }
  }

  function visibility(): void {
    if (destroyed) return;
    if (document.hidden) { clearTimeout(timer); timer = undefined; return; }
    now = Date.now();
    if (!polls(currentView()) || inFlight) return;
    clearTimeout(timer);
    timer = undefined;
    // Back from GitHub: check at once rather than waiting for the timer.
    void pollOnce().then(syncPolling);
  }

  function apply(next: GithubIntegration): void {
    doc = next;
    now = Date.now();
    onChange(next);
    syncPolling();
  }

  async function act(kind: typeof busy, call: () => Promise<GithubIntegration>, failureText: string): Promise<void> {
    if (busy || !kind) return;
    busy = kind;
    lastAction = kind;
    epoch += 1;
    error = '';
    pollNote = '';
    checkNote = '';
    clearTimeout(timer);
    timer = undefined;
    const before = currentView();
    try {
      const next = await call();
      if (destroyed) return;
      busy = '';
      apply(next);
      if (kind === 'check' && currentView() === before) {
        checkNote = before === 'no_repositories'
          ? 'Helm still sees no repositories. Finish installing on GitHub (choose at least one repository), then press I’ve installed it again.'
          : healthNote(next.state) || `Test passed. Helm can read ${next.allowed_repositories.length === 1 ? 'your repository' : 'your repositories'}.`;
      }
    } catch (failure) {
      if (destroyed) return;
      error = plainError(failure, failureText);
      if (failure instanceof Error && 'status' in failure && failure.status === 409) void refresh();
    } finally {
      busy = '';
      syncPolling();
    }
  }

  async function refresh(): Promise<void> {
    const started = epoch;
    try {
      const next = await api.getGithubIntegration();
      if (!destroyed && started === epoch) apply(next);
    } catch {
      // The error already on screen says what to do.
    }
  }

  const connect = () => act('connect', api.connectGithub, 'Helm could not start GitHub sign-in. Try again in a moment.');
  const cancel = () => attempt && act('cancel', () => api.cancelGithubConnect(attempt!.id), 'Helm could not cancel the sign-in. Try again in a moment.');
  const check = () => act('check', api.checkGithubIntegration, 'Helm could not test the connection. Try again in a moment.');
  const disconnect = async () => {
    await act('disconnect', api.disconnectGithub, 'Helm could not disconnect GitHub. Try again in a moment.');
    if (!error) confirming = false;
  };

  async function keep(): Promise<void> {
    confirming = false;
    await tick();
    root?.querySelector<HTMLElement>('[data-github-disconnect]')?.focus();
  }

  onMount(() => {
    document.addEventListener('visibilitychange', visibility);
    syncPolling();
  });

  onDestroy(() => {
    destroyed = true;
    clearTimeout(timer);
    clearInterval(ticker);
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', visibility);
  });
</script>

<div class="connection" data-github-connection data-connection-view={view} bind:this={root}>
  <p class="sr-only" role="status" aria-live="polite" aria-atomic="true" data-github-connection-live>{live}</p>

  {#if view === 'unavailable'}
    <h3 tabindex="-1" data-github-view-heading>GitHub sign-in is not set up</h3>
    <p>This Helm has no GitHub App client ID, so sign-in is off. An operator can set <code>HELM_GITHUB_APP_CLIENT_ID</code> and restart Helm, or give Helm a read-only token through <code>HELM_GITHUB_TOKEN_FILE</code> instead.</p>
  {:else if view === 'disconnected'}
    <h3 tabindex="-1" data-github-view-heading>Connect Helm to GitHub</h3>
    <p>Helm shows pull request status on cards and runs audits on the repositories you choose. You enter a short code on GitHub; there are no tokens to copy, and access is read-only.</p>
    <button class="button primary button-large" type="button" disabled={Boolean(busy)} on:click={() => void connect()}>Connect GitHub</button>
  {:else if view === 'starting'}
    <h3 tabindex="-1" data-github-view-heading class="starting"><span class="spinner" aria-hidden="true"></span>Getting a code from GitHub…</h3>
  {:else if view === 'pending' && attempt}
    <GitHubDeviceCode {attempt} {now} busy={Boolean(busy)} onCancel={() => void cancel()} announce={say} />
  {:else if view === 'other_pending'}
    <h3 tabindex="-1" data-github-view-heading>Another administrator is connecting GitHub</h3>
    <p>Their code is shown only on their screen. This page updates by itself when they finish{#if attempt}; their code expires in <strong class="nums">{timeLeft(attempt.expires_at, now)}</strong>{/if}.</p>
    <p class="muted">If they have stopped, you can start over. That replaces their code.</p>
    <button class="button quiet-button" type="button" disabled={Boolean(busy)} on:click={() => void connect()}>Connect it myself instead</button>
  {:else if view === 'expired' || view === 'denied' || view === 'failed'}
    <h3 tabindex="-1" data-github-view-heading>{view === 'expired' ? 'The code expired' : view === 'denied' ? 'Sign-in was declined' : 'Sign-in did not finish'}</h3>
    <p>{view === 'expired' ? 'The code expired before it was entered on GitHub. Get a new code and enter it within 15 minutes.'
      : view === 'denied' ? 'The request was declined on GitHub, so Helm is not connected. Try again if that was a mistake.'
        : 'GitHub sign-in stopped before Helm was connected. Try again; if it keeps happening, ask your operator to check Helm’s logs.'}</p>
    <button class="button primary button-large" type="button" disabled={Boolean(busy)} on:click={() => void connect()}>Try again</button>
  {:else if view === 'reconnect_required'}
    <h3 tabindex="-1" data-github-view-heading>GitHub sign-in needs renewing</h3>
    <p>Helm’s GitHub sign-in{#if connection.login} for <strong>@{connection.login}</strong>{/if} expired or was removed on GitHub. Reconnect to keep pull request status and audits working.</p>
    <button class="button primary button-large" type="button" disabled={Boolean(busy)} on:click={() => void connect()}>Reconnect GitHub</button>
  {:else if view === 'no_repositories' || view === 'connected'}
    {#if view === 'no_repositories'}
      <h3 tabindex="-1" data-github-view-heading>One more step: choose repositories</h3>
      <p>You are signed in as <strong>@{connection.login}</strong>, but Helm is not installed on any repository yet. Install it on the repositories Helm should read, then come back here.</p>
    {:else}
      <h3 tabindex="-1" data-github-view-heading><span class="ok" aria-hidden="true">✓</span>Connected as @{connection.login}</h3>
      <p class="muted">{summary}</p>
      {#if connection.repositories_truncated}<p class="note">GitHub lists more repositories than Helm reads, so Helm uses the first 50. To control which ones, choose fewer on GitHub.</p>{/if}
    {/if}
    {#if checkNote || (view === 'connected' && health)}<p class="note" data-github-check-note>{checkNote || health}</p>{/if}
    {#if confirming}
      <GitHubDisconnectConfirm authorizationsUrl={connection.authorizations_url} busy={busy === 'disconnect'} onConfirm={() => void disconnect()} onKeep={() => void keep()} />
    {:else}
      <div class="actions">
        {#if view === 'no_repositories'}
          {#if installUrl}<a class="button primary button-large" href={installUrl} target="_blank" rel="noopener noreferrer">Install Helm on your repositories <span aria-hidden="true">↗</span><span class="sr-only"> (opens in a new tab)</span></a>{/if}
          <button class="button quiet-button button-large" type="button" disabled={Boolean(busy)} on:click={() => void check()}>{#if busy === 'check'}<span class="spinner" aria-hidden="true"></span>{/if}I’ve installed it</button>
        {:else}
          {#if installUrl}<a class="button quiet-button" href={installUrl} target="_blank" rel="noopener noreferrer">Choose repositories on GitHub <span aria-hidden="true">↗</span><span class="sr-only"> (opens in a new tab)</span></a>{/if}
          <button class="button quiet-button" type="button" disabled={Boolean(busy)} on:click={() => void check()}>{#if busy === 'check'}<span class="spinner" aria-hidden="true"></span>{/if}Test connection</button>
        {/if}
        <button class="text-button disconnect" type="button" disabled={Boolean(busy)} data-github-disconnect on:click={() => { confirming = true; }}>Disconnect</button>
      </div>
    {/if}
  {/if}

  {#if pollNote}<p class="muted" data-github-poll-note>{pollNote}</p>{/if}
  {#if error}<div class="inline-alert error" role="alert"><span aria-hidden="true">!</span><span data-github-error>{error}</span></div>{/if}
</div>

<style>
  .connection { display: grid; gap: 12px; justify-items: start; min-width: 0; }
  .connection > * { max-width: 100%; }
  .connection > p, .connection > .inline-alert, .connection > :global([data-github-disconnect-confirm]) { justify-self: stretch; }
  h3 { display: flex; align-items: center; gap: 8px; margin: 0; font-size: var(--text-base); outline: none; }
  p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; max-width: 62ch; overflow-wrap: anywhere; }
  code { font-size: var(--text-xs); }
  .muted { color: var(--muted); font-size: var(--text-xs); }
  .note { padding: 8px 10px; border-radius: 8px; background: var(--amber-soft); font-size: var(--text-xs); }
  .nums { font-variant-numeric: tabular-nums; }
  .ok { display: inline-grid; place-items: center; width: 20px; height: 20px; border-radius: 50%; color: #fff; background: var(--semantic-green); font-size: var(--text-2xs); }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 14px; }
  .actions .text-button { min-height: 40px; }
  .disconnect { color: var(--semantic-red); }
  .disconnect:hover { color: var(--red); }
  .spinner { width: 14px; height: 14px; flex: 0 0 14px; border: 2px solid color-mix(in srgb, var(--purple), transparent 70%); border-top-color: var(--purple); border-radius: 50%; animation: spin .75s linear infinite; }
  .starting { color: var(--ink-soft); font-weight: 600; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 2s; } }
</style>
