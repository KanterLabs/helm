<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import type { EmailInbox } from '../types';

  // Email inboxes: addresses created in Helm. Apps (backups, monitoring,
  // Proxmox, Coolify…) send their alert emails here and each email becomes
  // a ticket in Needs triage. See docs/PUBLIC_ACCESS.md#email-inboxes.
  /** Bumped by the parent when setup may have created an inbox. */
  export let refreshKey = 0;
  /** Bound by Connect apps for its tab summary (additive). */
  export let activeCount = 0;
  export let emailOn = false;
  export let loaded = false;
  /** Bound by Connect apps: the create form holds unsaved text. */
  export let dirty = false;
  /** Opens Public access, where email is turned on; omitted shows text only. */
  export let onSetUpEmail: (() => void) | null = null;

  let inboxes: EmailInbox[] = [];
  let creating = false;
  let nameInput: HTMLInputElement | null = null;
  let list: HTMLElement | null = null;
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
  $: activeCount = active.length;
  $: dirty = Boolean(name.trim());
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
      creating = false;
      await load();
      await tick();
      list?.querySelector<HTMLElement>(`[data-inbox-id="${CSS.escape(inbox.id)}"] button`)?.focus();
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

  async function openCreate() {
    creating = !creating;
    if (!creating) return;
    await tick();
    nameInput?.focus();
  }

  function cancelCreate() {
    creating = false;
    name = '';
    assignMe = true;
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

<section class="inboxes conn-section" aria-labelledby="inboxes-heading" data-email-inboxes>
  <header class="conn-head">
    <div>
      <h3 id="inboxes-heading">Email inboxes</h3>
      <p>Point an app's alert emails at an inbox (backups, monitoring, Proxmox, Coolify…). Each email opens a ticket; the same alert again adds to its count.</p>
    </div>
    <div class="conn-actions">
      <button class="text-button" type="button" on:click={() => openHelp('public-access', 'email-inboxes')}>Learn more</button>
      {#if loaded && emailOn}<button class="button quiet-button" type="button" aria-expanded={creating} on:click={openCreate}><span aria-hidden="true">＋</span> New inbox</button>{/if}
    </div>
  </header>

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class="inline-alert notice-success" role="status"><span>✓</span>{notice}</div>{/if}

  {#if loaded && !emailOn}
    <div class="conn-empty" data-inboxes-email-off>
      <p>Email is off.</p>
      <p class="optional">Turn it on in <strong>Public access</strong>: Helm sets up the address with Cloudflare, then inboxes appear here.</p>
      {#if onSetUpEmail}<button class="button quiet-button" type="button" on:click={onSetUpEmail}>Open Public access</button>{/if}
    </div>
  {:else if loaded}
    {#if creating}
      <form class="conn-form inbox-create" aria-label="New inbox" on:submit|preventDefault={create}>
        <label>Inbox name<input bind:this={nameInput} aria-label="Inbox name" bind:value={name} maxlength="100" placeholder="e.g. Homelab alerts" /></label>
        <label class="conn-check"><input type="checkbox" bind:checked={assignMe} /> Assign to me</label>
        <div class="conn-form-actions">
          <button class="text-button" type="button" on:click={cancelCreate}>Cancel</button>
          <button class="button primary" type="submit" disabled={!name.trim() || saving}>{saving ? 'Creating…' : 'Create inbox'}</button>
        </div>
      </form>
    {:else if dirty}
      <p class="optional conn-draft-note">Draft “{name.trim()}” kept. <button class="text-button" type="button" on:click={openCreate}>Continue</button></p>
    {/if}

    {#if active.length}
      <ul class="inbox-list" bind:this={list}>
        {#each active as inbox (inbox.id)}
          <li class:fresh={inbox.id === newest} data-inbox={inbox.name} data-inbox-id={inbox.id}>
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
    {:else if !creating}
      <div class="conn-empty"><p>No inboxes yet.</p><p class="optional">Create one and point an app's alert emails at its address.</p></div>
    {/if}

    {#if retired.length}
      <details class="inbox-retired"><summary>Turned off ({retired.length})</summary><ul>{#each retired as inbox (inbox.id)}<li>{inbox.name}</li>{/each}</ul></details>
    {/if}
  {/if}
</section>

<style>
  .inbox-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .inbox-list li { display: grid; gap: 4px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 9px; background: var(--surface); }
  .inbox-list li.fresh { border-color: var(--purple); }
  .inbox-main { display: flex; flex-wrap: wrap; gap: 6px; align-items: baseline; font-size: var(--text-sm); }
  .inbox-address { display: flex; flex-wrap: wrap; gap: 4px 12px; align-items: baseline; min-width: 0; }
  .inbox-address code { min-width: 0; font-size: var(--text-xs); font-weight: 600; overflow-wrap: anywhere; }
  .inbox-meta { display: flex; justify-content: space-between; gap: 10px; align-items: center; }
  .inbox-create { grid-template-columns: minmax(0, 1fr) auto; align-items: end; }
  .inbox-create .conn-form-actions { grid-column: 1 / -1; }
  .inbox-retired { color: var(--muted); font-size: var(--text-xs); }
  .inbox-retired > summary { cursor: pointer; font-weight: 600; }
  .inbox-retired ul { margin: 6px 0 0; padding-left: 18px; }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  @media (max-width: 640px) { .inbox-create { grid-template-columns: minmax(0, 1fr); } }
</style>
