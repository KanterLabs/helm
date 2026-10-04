<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import type { EmailInbox } from '../types';

  // Email inboxes: addresses created in Helm. Apps (backups, monitoring,
  // Proxmox, Coolify…) send their alert emails here and each email becomes
  // a ticket in Needs triage. See docs/PUBLIC_ACCESS.md#email-inboxes.
  /** Bumped by the parent when setup may have created an inbox. */
  export let refreshKey = 0;

  let inboxes: EmailInbox[] = [];
  let emailOn = false;
  let loaded = false;
  let error = '';
  let notice = '';
  let name = '';
  let assignMe = true;
  let saving = false;
  let busyId = '';
  let copied = '';
  let newest = '';
  let poll: ReturnType<typeof setInterval> | undefined;

  $: active = inboxes.filter((inbox) => !inbox.disabled_at);
  $: retired = inboxes.filter((inbox) => inbox.disabled_at);
  $: refreshKey, void load();

  function message(value: unknown, fallback: string): string {
    return value instanceof Error && value.message ? value.message : fallback;
  }

  async function load() {
    try {
      const response = await api.listEmailInboxes();
      inboxes = response.data;
      emailOn = response.email_on;
      loaded = true;
    } catch (cause) {
      error = message(cause, 'Inboxes could not be loaded.');
    }
  }

  async function create() {
    if (!name.trim() || saving) return;
    saving = true;
    error = '';
    try {
      const inbox = await api.createEmailInbox({ name: name.trim(), assignee: assignMe ? 'me' : '' });
      newest = inbox.id;
      notice = `Inbox “${inbox.name}” is ready. Send email to ${inbox.address} and it becomes a ticket in Needs triage.`;
      name = '';
      await load();
    } catch (cause) {
      error = message(cause, 'The inbox could not be created.');
    } finally {
      saving = false;
    }
  }

  async function replace(inbox: EmailInbox) {
    if (!window.confirm(`Give “${inbox.name}” a new address? Mail to the current address will bounce.`)) return;
    busyId = inbox.id;
    try {
      const next = await api.replaceEmailInboxAddress(inbox.id);
      notice = `“${inbox.name}” now receives at ${next.address}. Update the apps that send to it.`;
      await load();
    } catch (cause) {
      error = message(cause, 'The address could not be replaced.');
    } finally {
      busyId = '';
    }
  }

  async function disable(inbox: EmailInbox) {
    if (!window.confirm(`Turn off “${inbox.name}”? Mail to it will bounce. Existing tickets stay.`)) return;
    busyId = inbox.id;
    try {
      await api.disableEmailInbox(inbox.id);
      notice = `“${inbox.name}” is off.`;
      await load();
    } catch (cause) {
      error = message(cause, 'The inbox could not be turned off.');
    } finally {
      busyId = '';
    }
  }

  async function copy(inbox: EmailInbox) {
    try {
      await navigator.clipboard.writeText(inbox.address);
      copied = inbox.id;
      setTimeout(() => { if (copied === inbox.id) copied = ''; }, 2000);
    } catch {
      copied = '';
    }
  }

  function fromMenu(event: Event, action: () => void) {
    (event.currentTarget as HTMLElement).closest('details')?.removeAttribute('open');
    action();
  }

  function mailto(inbox: EmailInbox): string {
    return `mailto:${inbox.address}?subject=${encodeURIComponent(`Test for ${inbox.name}`)}&body=${encodeURIComponent('Testing my Helm inbox.')}`;
  }

  onMount(() => {
    // New mail shows up without reloading.
    poll = setInterval(() => {
      if (document.visibilityState === 'visible') void load();
    }, 15_000);
  });
  onDestroy(() => clearInterval(poll));
</script>

<section class="inboxes" aria-labelledby="inboxes-heading" data-email-inboxes>
  <header>
    <div>
      <h3 id="inboxes-heading">Email inboxes</h3>
      <p>Point your apps' alert emails at an inbox (backups, monitoring, Proxmox, Coolify…). Every email becomes a ticket in Needs triage; the same alert again just adds to the open ticket's count.</p>
    </div>
    <button class="text-button" type="button" on:click={() => openHelp('public-access', 'email-inboxes')}>Learn more</button>
  </header>

  {#if loaded && !emailOn}
    <p class="optional" data-inboxes-email-off>Email is off. Turn it on in <strong>Reach Helm from outside</strong> (tick “Turn on email and create an inbox”).</p>
  {:else if loaded}
    {#if active.length}
      <ul class="inbox-list">
        {#each active as inbox (inbox.id)}
          <li class:fresh={inbox.id === newest} data-inbox={inbox.name}>
            <div class="inbox-main">
              <strong>{inbox.name}</strong>
              {#if inbox.assignee_name}<span class="optional">assigned to {inbox.assignee_name}</span>{/if}
            </div>
            <div class="inbox-address">
              <code data-inbox-address>{inbox.address}</code>
              <button class="text-button" type="button" on:click={() => copy(inbox)}>{copied === inbox.id ? 'Copied' : 'Copy'}</button>
              <a class="text-button" href={mailto(inbox)} data-inbox-mailto>Email it</a>
            </div>
            <div class="inbox-meta">
              <span class="optional" data-inbox-count>{inbox.received_count ? `${inbox.received_count} email${inbox.received_count === 1 ? '' : 's'} · last ${formatRelative(inbox.last_received_at)}` : 'No email yet: send one to try it'}</span>
              <details class="row-menu">
                <summary aria-label={`More actions for ${inbox.name}`}>More</summary>
                <div class="row-menu-items">
                  <button class="text-button" type="button" disabled={busyId === inbox.id} on:click={(event) => fromMenu(event, () => replace(inbox))}>Replace address</button>
                  <button class="text-button danger" type="button" disabled={busyId === inbox.id} on:click={(event) => fromMenu(event, () => disable(inbox))}>Turn off</button>
                </div>
              </details>
            </div>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="optional">No inboxes yet. Create one below.</p>
    {/if}

    <form class="inbox-create" aria-label="New inbox" on:submit|preventDefault={create}>
      <label>Inbox name<input aria-label="Inbox name" bind:value={name} maxlength="100" placeholder="e.g. Homelab alerts" /></label>
      <label class="inbox-check"><input type="checkbox" bind:checked={assignMe} /> Assign to me</label>
      <button class="button primary" type="submit" disabled={!name.trim() || saving}>{saving ? 'Creating…' : 'Create inbox'}</button>
    </form>

    {#if retired.length}
      <details class="optional"><summary>Turned off ({retired.length})</summary><ul>{#each retired as inbox (inbox.id)}<li>{inbox.name}</li>{/each}</ul></details>
    {/if}
  {/if}

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class="inline-alert notice-success" role="status"><span>✓</span>{notice}</div>{/if}
</section>

<style>
  .inboxes { display: grid; gap: 12px; padding: 14px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); }
  header { display: flex; justify-content: space-between; gap: 12px; align-items: flex-start; }
  h3 { margin: 0; font-size: 15px; }
  header p { margin: 4px 0 0; font-size: 13px; line-height: 1.5; color: var(--ink-soft); }
  .inbox-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .inbox-list li { display: grid; gap: 4px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface-muted); }
  .inbox-list li.fresh { border-color: var(--purple); }
  .inbox-main { display: flex; flex-wrap: wrap; gap: 6px; align-items: baseline; font-size: 13px; }
  .inbox-address { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: baseline; }
  .inbox-address code { font-size: 13px; font-weight: 700; overflow-wrap: anywhere; }
  .inbox-meta { display: flex; justify-content: space-between; gap: 10px; align-items: center; }
  .inbox-create { display: grid; grid-template-columns: 2fr auto auto; gap: 10px; align-items: end; }
  .inbox-create label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .inbox-check { display: flex !important; align-items: center; gap: 6px !important; min-height: 36px; }
  .inbox-check input { width: auto; height: auto; padding: 0; }
  .row-menu { position: relative; }
  .row-menu > summary { cursor: pointer; list-style: none; color: var(--muted); font-size: 12px; font-weight: 700; }
  .row-menu > summary::-webkit-details-marker { display: none; }
  .row-menu-items { position: absolute; right: 0; z-index: 5; display: grid; gap: 6px; min-width: 140px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); box-shadow: 0 8px 20px rgb(15 18 30 / 0.12); }
  .danger { color: var(--semantic-red); }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  @media (max-width: 760px) { header { flex-direction: column; } .inbox-create { grid-template-columns: minmax(0, 1fr); } }
</style>
