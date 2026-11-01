<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../../api';
  import type { TicketTestResult, TicketWebhook, TicketWebhookFormat, TicketWebhookSecret } from '../../types';
  import TicketWebhookGuide from './TicketWebhookGuide.svelte';

  // Webhooks tab of Connect apps (admins only): existing URLs first; the
  // create form and the API guide open on request. A new or rotated URL is
  // shown once and stays until "I saved it", across tab changes.
  /** Bound by the manager: active webhook count for the tab summary. */
  export let activeCount = 0;
  /** Bound by the manager: a one-time URL is on screen, not yet acknowledged. */
  export let secretPending = false;
  /** Bound by the manager: the create form holds unsaved text. */
  export let dirty = false;

  let hooks: TicketWebhook[] = [];
  let endpointBase = '';
  let publicHostname = '';
  let loading = false;
  let error = '';
  let creating = false;
  let guideOpen = false;
  let name = '';
  let format: TicketWebhookFormat = 'generic';
  let assignMe = true;
  let saving = false;
  let revealed: TicketWebhookSecret | null = null;
  let testResults: Record<string, TicketTestResult> = {};
  let copied = false;
  let busyId = '';
  let urlInput: HTMLInputElement | null = null;
  let nameInput: HTMLInputElement | null = null;

  $: activeCount = hooks.filter((hook) => !hook.disabled_at).length;
  $: secretPending = Boolean(revealed);
  $: dirty = Boolean(name.trim());
  $: exampleURL = revealed?.url || (endpointBase ? `${endpointBase}<secret>` : '');

  function message(value: unknown, fallback: string): string {
    return value instanceof Error && value.message ? value.message : fallback;
  }

  function when(value?: string): string {
    if (!value) return 'Never';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(date);
  }

  async function load() {
    loading = true;
    error = '';
    try {
      const response = await api.listTicketWebhooks();
      hooks = response.data;
      endpointBase = response.endpoint_base;
      publicHostname = response.public_hostname;
    } catch (cause) {
      error = message(cause, 'Webhooks could not be loaded.');
    } finally {
      loading = false;
    }
  }

  /** Reloads after guided setup may have changed the public hostname. */
  export function refresh() {
    void load();
  }

  async function show(secret: TicketWebhookSecret) {
    revealed = secret;
    copied = false;
    await tick();
    urlInput?.focus();
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
    format = 'generic';
    assignMe = true;
  }

  async function create() {
    if (!name.trim() || saving) return;
    saving = true;
    error = '';
    try {
      const secret = await api.createTicketWebhook({ name: name.trim(), format, assignee: assignMe ? 'me' : '' });
      cancelCreate();
      await load();
      await show(secret);
    } catch (cause) {
      error = message(cause, 'The webhook could not be created.');
    } finally {
      saving = false;
    }
  }

  async function rotate(hook: TicketWebhook) {
    if (!window.confirm(`Rotate “${hook.name}”? The current URL stops working immediately.`)) return;
    busyId = hook.id;
    try {
      const secret = await api.rotateTicketWebhook(hook.id);
      await load();
      await show(secret);
    } catch (cause) {
      error = message(cause, 'The webhook could not be rotated.');
    } finally {
      busyId = '';
    }
  }

  async function sendTest(hook: TicketWebhook) {
    busyId = hook.id;
    error = '';
    try {
      testResults = { ...testResults, [hook.id]: await api.sendTestTicket(hook.id) };
      await load();
    } catch (cause) {
      error = message(cause, 'The test ticket could not be sent.');
    } finally {
      busyId = '';
    }
  }

  async function disable(hook: TicketWebhook) {
    if (!window.confirm(`Disable “${hook.name}”? Apps using it will get 404. Existing tickets stay.`)) return;
    busyId = hook.id;
    try {
      await api.disableTicketWebhook(hook.id);
      if (revealed?.webhook.id === hook.id) revealed = null;
      await load();
    } catch (cause) {
      error = message(cause, 'The webhook could not be disabled.');
    } finally {
      busyId = '';
    }
  }

  // Row menus close once an action is chosen.
  function fromMenu(event: Event, action: () => void) {
    (event.currentTarget as HTMLElement).closest('details')?.removeAttribute('open');
    action();
  }

  function testSummary(result: TicketTestResult): string {
    const timing = `${result.latency_ms < 1 ? '<1' : result.latency_ms} ms${result.via_cloudflare ? ' via Cloudflare' : ''}`;
    return result.ok ? `${result.message} · ${timing}` : result.message;
  }

  async function copyURL() {
    if (!revealed) return;
    try {
      await navigator.clipboard.writeText(revealed.url);
      copied = true;
    } catch {
      copied = false;
    }
  }

  onMount(load);
</script>

<section class="conn-section" aria-labelledby="webhooks-heading" data-ticket-webhooks>
  <header class="conn-head">
    <div>
      <h3 id="webhooks-heading">Webhooks</h3>
      <p>Apps post JSON to a private URL. Each post opens a ticket or adds to its open one.</p>
    </div>
    <div class="conn-actions">
      <button class="text-button" type="button" aria-expanded={guideOpen} aria-controls="webhook-guide-region" on:click={() => { guideOpen = !guideOpen; }}>How to send</button>
      <button class="button quiet-button" type="button" aria-expanded={creating} on:click={openCreate}><span aria-hidden="true">＋</span> New webhook</button>
    </div>
  </header>

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}

  {#if revealed}
    <div class="conn-reveal" role="status" data-webhook-secret-reveal>
      <strong>Copy this URL now. It will not be shown again.</strong>
      <p>Webhook “{revealed.webhook.name}” · {revealed.webhook.format === 'coolify' ? 'Coolify notifications' : 'Generic JSON'}</p>
      <div class="conn-reveal-row">
        <label class="sr-only" for="webhook-url">Webhook URL</label>
        <input id="webhook-url" bind:this={urlInput} readonly value={revealed.url} on:focus={(event) => event.currentTarget.select()} />
        <button class="button quiet-button" type="button" on:click={copyURL}>{copied ? 'Copied' : 'Copy URL'}</button>
      </div>
      <button class="text-button" type="button" on:click={() => { revealed = null; copied = false; }}>I saved it</button>
    </div>
  {/if}

  {#if creating}
    <form class="conn-form webhook-form" aria-label="New webhook" on:submit|preventDefault={create}>
      <label>Name<input bind:this={nameInput} aria-label="Webhook name" bind:value={name} maxlength="100" placeholder="e.g. Grafana alerts" required /></label>
      <label>Format<select aria-label="Webhook format" bind:value={format}><option value="generic">Generic JSON</option><option value="coolify">Coolify notifications</option></select></label>
      <label class="conn-check"><input type="checkbox" bind:checked={assignMe} /> Assign new tickets to me</label>
      <div class="conn-form-actions">
        <button class="text-button" type="button" on:click={cancelCreate}>Cancel</button>
        <button class="button primary" type="submit" disabled={!name.trim() || saving}>{saving ? 'Creating…' : 'Create webhook URL'}</button>
      </div>
    </form>
  {:else if dirty}
    <p class="optional conn-draft-note">Draft “{name.trim()}” kept. <button class="text-button" type="button" on:click={openCreate}>Continue</button></p>
  {/if}

  {#if loading && !hooks.length}
    <p class="optional">Loading webhooks…</p>
  {:else if hooks.length}
    <div class="conn-table-wrap">
      <table class="conn-table webhook-table">
        <caption class="sr-only">Ticket webhooks</caption>
        <thead><tr><th scope="col">Name</th><th scope="col">Format</th><th scope="col">URL ends</th><th scope="col">Deliveries</th><th scope="col">Last used</th><th scope="col"><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {#each hooks as hook (hook.id)}
            <tr class:disabled={Boolean(hook.disabled_at)} data-webhook-name={hook.name}>
              <td class="webhook-name">{hook.name}{#if hook.disabled_at}{' '}<span class="optional">· disabled</span>{/if}</td>
              <td>{hook.format === 'coolify' ? 'Coolify' : 'Generic JSON'}</td>
              <td><code>…{hook.secret_hint}</code></td>
              <td data-webhook-deliveries>{hook.delivery_count}</td>
              <td>{when(hook.last_delivery_at)}</td>
              <td class="webhook-actions-cell"><div class="webhook-actions">{#if !hook.disabled_at}
                {#if publicHostname}<button class="text-button" type="button" disabled={busyId === hook.id} title={`Send a test ticket through https://${publicHostname}`} on:click={() => sendTest(hook)}>{busyId === hook.id && !testResults[hook.id] ? 'Sending…' : 'Send test'}</button>{/if}
                <details class="row-menu">
                  <summary aria-label={`More actions for ${hook.name}`}>More</summary>
                  <div class="row-menu-items">
                    <button class="text-button" type="button" disabled={busyId === hook.id} on:click={(event) => fromMenu(event, () => rotate(hook))}>Rotate</button>
                    <button class="text-button danger" type="button" disabled={busyId === hook.id} on:click={(event) => fromMenu(event, () => disable(hook))}>Disable</button>
                  </div>
                </details>
              {/if}</div></td>
            </tr>
            {#if testResults[hook.id]}
              {@const result = testResults[hook.id]}
              <tr class="webhook-test-row" data-webhook-test-result={hook.name}>
                <td colspan="6">
                  <span class={result.ok ? 'test-ok' : 'test-failed'} data-webhook-test-status={result.ok ? 'ok' : 'failed'}>{result.ok ? '✓' : '✕'}</span>
                  {testSummary(result)}
                  {#if result.ok && result.ticket_url}<a href={result.ticket_url}>Open {result.ticket_key}</a>{/if}
                  {#if result.cf_ray}<span class="optional">· Ray {result.cf_ray}</span>{/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
  {:else if !error}
    <div class="conn-empty"><p>No webhooks yet.</p><p class="optional">Create one, then paste its URL into your monitoring, CI or form tool.</p></div>
  {/if}

  <div id="webhook-guide-region">
    {#if guideOpen}<TicketWebhookGuide {exampleURL} {publicHostname} />{/if}
  </div>
</section>

<style>
  .webhook-form { grid-template-columns: minmax(0, 2fr) minmax(0, 1.2fr); }
  .webhook-form .conn-check, .webhook-form .conn-form-actions { grid-column: 1 / -1; }
  .webhook-table tr.disabled { color: var(--muted); }
  .webhook-name { font-weight: 600; }
  .webhook-actions-cell { width: 1%; white-space: nowrap; }
  .webhook-actions { display: flex; gap: 12px; align-items: center; justify-content: flex-end; }
  .webhook-test-row td { color: var(--ink-soft); }
  .webhook-test-row a { margin-left: 6px; font-weight: 600; }
  .test-ok { color: var(--semantic-green); font-weight: 600; }
  .test-failed { color: var(--semantic-red); font-weight: 600; }
  @media (max-width: 640px) {
    .webhook-form { grid-template-columns: minmax(0, 1fr); }
    .webhook-table thead { display: none; }
    .webhook-table tr { display: grid; gap: 2px; padding: 8px 0; border-bottom: 1px solid var(--border); }
    .webhook-table td { padding: 0; border: 0; }
    .webhook-actions { justify-content: flex-start; }
  }
</style>
