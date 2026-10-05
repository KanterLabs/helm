<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import type { Actor, TicketTestResult, TicketWebhook, TicketWebhookFormat, TicketWebhookSecret } from '../types';
  import { openHelp } from '../help';
  import EmailInboxes from './EmailInboxes.svelte';
  import HelpDrawer from './HelpDrawer.svelte';
  import PublicAccessPanel from './PublicAccessPanel.svelte';

  export let user: Actor;
  export let onPublicAccessChanged: () => void = () => undefined;

  const fields: { name: string; required: boolean; text: string }[] = [
    { name: 'title', required: true, text: 'Ticket title shown in the queue (max 300 characters).' },
    { name: 'description', required: false, text: 'Markdown details shown under “What needs attention”.' },
    { name: 'priority', required: false, text: 'low, normal (default), high or urgent.' },
    { name: 'dedupe_key', required: false, text: 'Stable ID for the problem, e.g. db-1:disk. Repeats while the ticket is open only add to its count; after it is completed the next post opens a new linked ticket. Leave it out to open a ticket on every post.' },
    { name: 'source', required: false, text: 'Name of the sending app, e.g. grafana.' },
    { name: 'url', required: false, text: 'Link back to the sending app.' },
    { name: 'fields', required: false, text: 'Up to 20 short facts as an object, e.g. {"host": "db-1"}. Shown as evidence on the ticket.' }
  ];

  let hooks: TicketWebhook[] = [];
  let endpointBase = '';
  let loading = false;
  let error = '';
  let name = '';
  let format: TicketWebhookFormat = 'generic';
  let assignMe = true;
  let saving = false;
  let revealed: TicketWebhookSecret | null = null;
  let publicHostname = '';
  let testResults: Record<string, TicketTestResult> = {};
  // Bumped when guided setup may have created the first inbox.
  let inboxesKey = 0;
  let copied = '';
  let busyId = '';

  $: exampleURL = revealed?.url || `${endpointBase || `${window.location.origin}/api/v1/hooks/tickets/`}<secret>`;
  $: curlExample = `curl -X POST '${exampleURL}' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title": "Disk almost full on db-1", "priority": "high", "dedupe_key": "db-1:disk", "source": "grafana", "fields": {"host": "db-1", "usage": "93%"}}'`;

  function message(value: unknown, fallback: string): string {
    return value instanceof Error && value.message ? value.message : fallback;
  }

  function when(value?: string): string {
    if (!value) return 'Never';
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(date);
  }

  async function load() {
    if (!user.admin) return;
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

  async function create() {
    if (!name.trim() || saving) return;
    saving = true;
    error = '';
    try {
      revealed = await api.createTicketWebhook({ name: name.trim(), format, assignee: assignMe ? 'me' : '' });
      name = '';
      await load();
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
      revealed = await api.rotateTicketWebhook(hook.id);
      await load();
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

  // Row menus close once an action is chosen.
  function fromMenu(event: Event, action: () => void) {
    (event.currentTarget as HTMLElement).closest('details')?.removeAttribute('open');
    action();
  }

  function testSummary(result: TicketTestResult): string {
    const timing = `${result.latency_ms < 1 ? '<1' : result.latency_ms} ms${result.via_cloudflare ? ' via Cloudflare' : ''}`;
    return result.ok ? `${result.message} · ${timing}` : result.message;
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

  async function copy(label: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      copied = label;
    } catch {
      copied = '';
    }
  }

  onMount(load);
</script>

<section class="ticket-integrations" aria-labelledby="ticket-integrations-heading">
  <div class="integrations-heading">
    <h2 id="ticket-integrations-heading">Connect apps</h2>
    <p>Any app that can send an HTTP POST can open tickets: monitoring, CI, forms, scripts or Zapier-style tools. Create a webhook URL here, paste it into the app, and new tickets land in <strong>Needs triage</strong>.</p>
  </div>

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}

  {#if user.admin}
    <PublicAccessPanel onChanged={() => { inboxesKey += 1; void load(); onPublicAccessChanged(); }} />
    <EmailInboxes refreshKey={inboxesKey} />
  {/if}
  <HelpDrawer />

  {#if revealed}
    <div class="webhook-reveal" role="status" data-webhook-secret-reveal>
      <strong>Copy this URL now. It will not be shown again.</strong>
      <p>Webhook “{revealed.webhook.name}” · {revealed.webhook.format === 'coolify' ? 'Coolify notifications' : 'Generic JSON'}</p>
      <div class="webhook-url-row">
        <label class="sr-only" for="webhook-url">Webhook URL</label>
        <input id="webhook-url" readonly value={revealed.url} on:focus={(event) => event.currentTarget.select()} />
        <button class="button quiet-button" type="button" on:click={() => revealed && copy('url', revealed.url)}>{copied === 'url' ? 'Copied' : 'Copy URL'}</button>
      </div>
      <button class="text-button" type="button" on:click={() => { revealed = null; copied = ''; }}>I saved it</button>
    </div>
  {/if}

  {#if user.admin}
    <h3 class="webhooks-heading">Webhooks</h3>
    <form class="webhook-create" aria-label="New webhook" on:submit|preventDefault={create}>
      <label>Name<input aria-label="Webhook name" bind:value={name} maxlength="100" placeholder="e.g. Grafana alerts" required /></label>
      <label>Format<select aria-label="Webhook format" bind:value={format}><option value="generic">Generic JSON</option><option value="coolify">Coolify notifications</option></select></label>
      <label class="webhook-check"><input type="checkbox" bind:checked={assignMe} /> Assign new tickets to me</label>
      <button class="button primary" type="submit" disabled={!name.trim() || saving}>{saving ? 'Creating…' : 'Create webhook URL'}</button>
    </form>

    {#if loading && !hooks.length}
      <p class="optional">Loading webhooks…</p>
    {:else if hooks.length}
      <table class="webhook-table">
        <caption class="sr-only">Ticket webhooks</caption>
        <thead><tr><th scope="col">Name</th><th scope="col">Format</th><th scope="col">URL ends</th><th scope="col">Deliveries</th><th scope="col">Last used</th><th scope="col"><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {#each hooks as hook (hook.id)}
            <tr class:disabled={Boolean(hook.disabled_at)} data-webhook-name={hook.name}>
              <td>{hook.name}{#if hook.disabled_at}{' '}<span class="optional">· disabled</span>{/if}</td>
              <td>{hook.format === 'coolify' ? 'Coolify' : 'Generic JSON'}</td>
              <td><code>…{hook.secret_hint}</code></td>
              <td data-webhook-deliveries>{hook.delivery_count}</td>
              <td>{when(hook.last_delivery_at)}</td>
              <td class="webhook-actions">{#if !hook.disabled_at}
                {#if publicHostname}<button class="text-button" type="button" disabled={busyId === hook.id} title={`Send a test ticket through https://${publicHostname}`} on:click={() => sendTest(hook)}>{busyId === hook.id && !testResults[hook.id] ? 'Sending…' : 'Send test'}</button>{/if}
                <details class="row-menu">
                  <summary aria-label={`More actions for ${hook.name}`}>More</summary>
                  <div class="row-menu-items">
                    <button class="text-button" type="button" disabled={busyId === hook.id} on:click={(event) => fromMenu(event, () => rotate(hook))}>Rotate</button>
                    <button class="text-button danger" type="button" disabled={busyId === hook.id} on:click={(event) => fromMenu(event, () => disable(hook))}>Disable</button>
                  </div>
                </details>
              {/if}</td>
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
    {:else}
      <p class="optional">No webhooks yet.</p>
    {/if}
  {:else}
    <p class="optional">Ask a workspace administrator to create a webhook URL for your app.</p>
  {/if}

  <details class="webhook-guide" open={!hooks.length}>
    <summary><h3 id="webhook-guide-heading">How to send a ticket</h3></summary>
    <button class="text-button guide-link" type="button" on:click={() => openHelp('ticket-webhooks')}>Full guide</button>
    <p>POST a JSON object to the webhook URL with <code>Content-Type: application/json</code>. No other authentication is needed: keep the URL private, and rotate it if it leaks.</p>
    <div class="webhook-code">
      <pre data-webhook-curl><code>{curlExample}</code></pre>
      <button class="button quiet-button" type="button" on:click={() => copy('curl', curlExample)}>{copied === 'curl' ? 'Copied' : 'Copy example'}</button>
    </div>
    <table class="webhook-table webhook-fields">
      <caption class="sr-only">Accepted JSON fields</caption>
      <thead><tr><th scope="col">Field</th><th scope="col">Required</th><th scope="col">Meaning</th></tr></thead>
      <tbody>{#each fields as field (field.name)}<tr><td><code>{field.name}</code></td><td>{field.required ? 'Yes' : 'No'}</td><td>{field.text}</td></tr>{/each}</tbody>
    </table>
    <p><strong>Response.</strong> <code>201</code> with <code>{'{"disposition": "created", "ticket": {"key": "OPS-61", "url": "…"}}'}</code> for a new ticket, or <code>200</code> with <code>"repeated"</code> when a <code>dedupe_key</code> matched an open ticket. A bad body returns <code>400</code> and names the field to fix in <code>error.details.field</code>; unknown fields are rejected so typos are caught. An unknown, rotated or disabled URL returns <code>404</code>.</p>
    {#if publicHostname}<p data-webhook-test-guide><strong>Check it end to end.</strong> <strong>Send test</strong> on a webhook makes Helm post a test ticket to <code>https://{publicHostname}</code>, through Cloudflare and the tunnel, exactly as an outside app would. Repeated tests count on one low-priority test ticket.</p>{/if}
    <h3 id="webhook-email-heading">Sending by email</h3>
    <p data-webhook-email-guide>Apps that send alerts by email use an <strong>Email inbox</strong> instead of a webhook: create one above and point the app at its address.</p>
    <p class="optional">Choose the <strong>Coolify notifications</strong> format to paste the URL into Coolify → Notifications → Webhook. The full reference is in the API document at <a href="/openapi.json">/openapi.json</a> (operation <code>postTicketWebhook</code>).</p>
  </details>
</section>

<style>
  .ticket-integrations { display: grid; gap: 14px; padding: 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); }
  .integrations-heading h2 { margin: 0; font: 800 18px var(--font-display); }
  .integrations-heading p, .webhook-guide p { margin: 6px 0 0; color: var(--ink-soft); font-size: 13px; line-height: 1.5; }
  .webhook-reveal { display: grid; gap: 8px; padding: 12px; border: 1px solid color-mix(in srgb, var(--amber), var(--border) 60%); border-radius: 10px; background: var(--amber-soft); }
  .webhook-reveal p { margin: 0; font-size: 12px; }
  .webhook-url-row { display: flex; gap: 8px; }
  .webhook-url-row input { flex: 1; min-width: 0; font-family: ui-monospace, monospace; font-size: 12px; }
  .webhook-reveal .text-button { justify-self: start; }
  .webhook-create { display: grid; grid-template-columns: 2fr 1.2fr auto auto; gap: 10px; align-items: end; }
  .webhook-create label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .webhook-check { display: flex !important; align-items: center; gap: 6px !important; min-height: 36px; }
  .webhook-table { width: 100%; border-collapse: collapse; font-size: 12px; }
  .webhook-table th, .webhook-table td { padding: 7px 8px; border-bottom: 1px solid var(--border); text-align: left; vertical-align: top; }
  .webhook-table th { color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: .04em; }
  .webhook-table tr.disabled { color: var(--muted); }
  .webhook-actions { display: flex; gap: 10px; justify-content: flex-end; }
  .danger { color: var(--semantic-red); }
  .webhook-test-row td { font-size: 12px; color: var(--ink-soft); }
  .webhook-test-row a { margin-left: 6px; font-weight: 700; }
  .test-ok { color: var(--semantic-green); font-weight: 800; }
  .test-failed { color: var(--semantic-red); font-weight: 800; }
  .webhooks-heading { margin: 4px 0 0; font-size: 15px; }
  .webhook-guide { display: grid; gap: 10px; }
  .webhook-guide > summary { cursor: pointer; }
  .webhook-guide h3 { display: inline; margin: 0; font-size: 14px; }
  .webhook-guide[open] > summary { margin-bottom: 10px; }
  .guide-link { justify-self: start; }
  .webhook-actions { align-items: center; flex-wrap: wrap; }
  .row-menu { position: relative; }
  .row-menu > summary { cursor: pointer; list-style: none; color: var(--muted); font-size: 12px; font-weight: 700; }
  .row-menu > summary::-webkit-details-marker { display: none; }
  .row-menu-items { position: absolute; right: 0; z-index: 5; display: grid; gap: 6px; min-width: 130px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); box-shadow: 0 8px 20px rgb(15 18 30 / 0.12); }
  .webhook-code { display: grid; gap: 6px; justify-items: start; }
  .webhook-code pre { width: 100%; margin: 0; padding: 12px; overflow-x: auto; border-radius: 8px; background: var(--surface-muted); font-size: 12px; line-height: 1.5; white-space: pre-wrap; word-break: break-all; }
  .webhook-fields td:first-child { white-space: nowrap; }
  .optional { color: var(--muted); font-size: 12px; }
  @media (max-width: 760px) {
    .webhook-create { grid-template-columns: minmax(0, 1fr); }
    .webhook-table thead { display: none; }
    .webhook-table tr { display: grid; padding: 6px 0; border-bottom: 1px solid var(--border); }
    .webhook-table td { padding: 2px 0; border: 0; }
    .webhook-actions { justify-content: flex-start; }
  }
</style>
