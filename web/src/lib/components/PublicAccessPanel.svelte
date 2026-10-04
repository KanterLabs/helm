<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import type { EmailIntakeView, PublicEndpointView } from '../types';
  import CloudflareSetup from './CloudflareSetup.svelte';
  import EmailIntakeCard from './EmailIntakeCard.svelte';
  import PublicEndpointCard from './PublicEndpointCard.svelte';

  // "Reach Helm from outside": one place for public access. Not set up →
  // guided setup (Sign in with Cloudflare). Set up → a short summary.
  // Detailed status, history and manual setup sit in one collapsed section.
  export let onChanged: () => void = () => undefined;

  const detailsKey = 'helm.publicAccess.detailsOpen';

  let endpoints: PublicEndpointView | null = null;
  let email: EmailIntakeView | null = null;
  let unavailable = false;
  let showSetup: boolean | null = null;
  let addingEmail = false;
  let cardsKey = 0;
  let detailsOpen = false;
  let poll: ReturnType<typeof setInterval> | undefined;

  $: active = endpoints?.active;
  $: live = endpoints?.connector.state === 'connected';
  $: emailOn = Boolean(email?.active);
  $: if (showSetup === null && endpoints) showSetup = !active;

  async function load() {
    try {
      [endpoints, email] = await Promise.all([api.getPublicEndpoints(), api.getEmailIntake()]);
      unavailable = false;
    } catch {
      unavailable = true;
    }
  }

  // Guided setup changed things behind the detailed cards: remount them.
  function setupChanged() {
    cardsKey += 1;
    void load();
    onChanged();
  }

  // A card changed its own state; keep it mounted so its notice stays.
  function cardChanged() {
    void load();
    onChanged();
  }

  function finished() {
    showSetup = false;
    addingEmail = false;
    void load();
  }

  function toggleDetails(event: Event) {
    detailsOpen = (event.currentTarget as HTMLDetailsElement).open;
    try {
      localStorage.setItem(detailsKey, detailsOpen ? '1' : '0');
    } catch {
      // Storage may be unavailable (private mode); the section still works.
    }
  }

  onMount(() => {
    // Returning from Cloudflare sign-in (or its error) shows setup, even
    // when a public URL already exists.
    const params = new URL(window.location.href).searchParams;
    if (params.has('cloudflare') || params.has('cloudflare_error')) showSetup = true;
    try {
      detailsOpen = localStorage.getItem(detailsKey) === '1';
    } catch {
      detailsOpen = false;
    }
    void load();
    poll = setInterval(() => {
      if (document.visibilityState === 'visible' && !showSetup) void load();
    }, 30_000);
  });
  onDestroy(() => clearInterval(poll));
</script>

{#if !unavailable}
  <section class="public-access" aria-labelledby="public-access-heading" data-public-access>
    <header>
      <div>
        <h3 id="public-access-heading">Reach Helm from outside</h3>
        <p>Outside apps can send tickets only if they can reach Helm. Sign in with Cloudflare and Helm sets up a public URL that exposes just the webhooks, plus optional email addresses. No ports to open.</p>
      </div>
      <div class="public-access-actions">
        {#if endpoints}
          <span class={`status-badge ${active ? (live ? 'badge-live' : 'badge-warn') : 'badge-off'}`} data-public-access-status>{active ? (live ? 'Live' : 'Connecting') : 'Not set up'}</span>
        {/if}
        <button class="text-button" type="button" on:click={() => openHelp('public-access')}>Learn more</button>
      </div>
    </header>

    {#if endpoints && active && !showSetup}
      <dl class="public-summary" data-public-access-summary>
        <div>
          <dt>Public URL</dt>
          <dd><strong data-summary-url>https://{active.hostname}</strong> <span class="optional">· {live ? 'live' : endpoints.connector.state}</span></dd>
        </div>
        <div>
          <dt>Email addresses</dt>
          {#if emailOn && email?.active}
            <dd data-summary-email><strong>{email.active.local_part}+…@{email.active.domain}</strong> <span class="optional">· each webhook gets its own</span></dd>
          {:else}
            <dd data-summary-email>Off <button class="text-button" type="button" on:click={() => { addingEmail = true; showSetup = true; }} data-add-email>Add email addresses</button></dd>
          {/if}
        </div>
      </dl>
      <p class="optional">Test it with <strong>Send test</strong>{#if emailOn}{' '}and <strong>Test email</strong>{/if} on a webhook below. <button class="text-button" type="button" on:click={() => { showSetup = true; }} data-rerun-setup>Run setup again</button></p>
    {:else if showSetup}
      <CloudflareSetup preferEmail={true} onChanged={setupChanged} onFinished={finished} />
      {#if active}
        <button class="text-button cancel-setup" type="button" on:click={() => { showSetup = false; addingEmail = false; }}>Back to summary</button>
      {/if}
    {/if}

    <details class="public-details" open={detailsOpen} on:toggle={toggleDetails} data-public-access-advanced>
      <summary>Details, history and manual setup</summary>
      {#key cardsKey}
        <PublicEndpointCard onChanged={cardChanged} />
        <EmailIntakeCard onChanged={cardChanged} />
      {/key}
    </details>
  </section>
{/if}

<style>
  .public-access { display: grid; gap: 12px; padding: 14px; border: 1px solid color-mix(in srgb, var(--purple), var(--border) 65%); border-radius: 12px; background: var(--surface); }
  header { display: flex; justify-content: space-between; gap: 12px; align-items: flex-start; }
  h3 { margin: 0; font-size: 15px; }
  header p { margin: 4px 0 0; font-size: 13px; line-height: 1.5; color: var(--ink-soft); }
  .public-access-actions { display: flex; flex: 0 0 auto; gap: 10px; align-items: center; }
  .status-badge { padding: 2px 9px; border-radius: 999px; font-size: 11px; font-weight: 800; white-space: nowrap; }
  .badge-live { color: var(--semantic-green); background: var(--green-soft); }
  .badge-warn { color: var(--semantic-amber); background: var(--amber-soft); }
  .badge-off { color: var(--muted); background: var(--surface-muted); border: 1px solid var(--border); }
  .public-summary { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin: 0; }
  .public-summary > div { display: grid; gap: 3px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface-muted); }
  dt { color: var(--muted); font-size: 11px; font-weight: 800; text-transform: uppercase; letter-spacing: 0.04em; }
  dd { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .public-access > p { margin: 0; }
  .public-details { display: grid; gap: 10px; }
  .public-details > summary { cursor: pointer; font-size: 12px; font-weight: 700; color: var(--ink-soft); }
  .public-details[open] > summary { margin-bottom: 10px; }
  .public-details :global(.public-endpoint), .public-details :global(.email-intake) { margin-bottom: 10px; }
  .cancel-setup { justify-self: start; }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  @media (max-width: 760px) { header { flex-direction: column; } .public-summary { grid-template-columns: minmax(0, 1fr); } }
</style>
