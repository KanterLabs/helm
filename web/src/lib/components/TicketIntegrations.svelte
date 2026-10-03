<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import type { Actor, EmailSummary, Project, TicketTestResult, TicketWebhook, TicketWebhookFormat, TicketWebhookSecret } from '../types';
  import EmailIntakeCard from './EmailIntakeCard.svelte';
  import PublicEndpointCard from './PublicEndpointCard.svelte';

  export let user: Actor;
  export let projects: Project[] = [];
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
  let project = '';
  let format: TicketWebhookFormat = 'generic';
  let assignMe = true;
  let saving = false;
  let revealed: TicketWebhookSecret | null = null;
  let revealedEmail: { webhook: TicketWebhook; email_address: string } | null = null;
  let email: EmailSummary | null = null;
  let publicHostname = '';
  let testResults: Record<string, TicketTestResult> = {};
  let copied = '';
  let busyId = '';

  $: if (!project && projects.length) project = projects[0].key;
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
      email = response.email;
      publicHostname = response.public_hostname;
    } catch (cause) {
      error = message(cause, 'Webhooks could not be loaded.');
    } finally {
      loading = false;
    }
  }

  async function create() {
    if (!name.trim() || !project || saving) return;
    saving = true;
    error = '';
    try {
      revealed = await api.createTicketWebhook({ name: name.trim(), project, format, assignee: assignMe ? 'me' : '' });
      revealedEmail = null;
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

  async function setEmail(hook: TicketWebhook) {
    if (hook.email_tag_hint && !window.confirm(`Replace the email address of “${hook.name}”? The current address stops working immediately.`)) return;
    busyId = hook.id;
    error = '';
    try {
      revealedEmail = await api.setTicketWebhookEmail(hook.id);
      revealed = null;
      await load();
    } catch (cause) {
      error = message(cause, 'The email address could not be created.');
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

  function testSummary(result: TicketTestResult): string {
    const timing = `${result.latency_ms < 1 ? '<1' : result.latency_ms} ms${result.via_cloudflare ? ' via Cloudflare' : ''}`;
    return result.ok ? `${result.message} · ${timing}` : result.message;
  }

  function emailHint(hook: TicketWebhook): string {
    return email && hook.email_tag_hint ? `${email.local_part}+…${hook.email_tag_hint}@${email.domain}` : '';
  }

  async function disable(hook: TicketWebhook) {
    if (!window.confirm(`Disable “${hook.name}”? Apps using it will get 404. Existing tickets stay.`)) return;
    busyId = hook.id;
    try {
      await api.disableTicketWebhook(hook.id);
      if (revealed?.webhook.id === hook.id) revealed = null;
      if (revealedEmail?.webhook.id === hook.id) revealedEmail = null;
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
    <p>Any app that can send an HTTP POST can open tickets: monitoring, CI, forms, scripts or Zapier-style tools. Create a webhook URL here, paste it into the app, and new tickets land in that project's <strong>Needs triage</strong> queue.</p>
  </div>

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}

  {#if user.admin}<PublicEndpointCard onChanged={() => { void load(); onPublicAccessChanged(); }} />{/if}
  {#if user.admin}<EmailIntakeCard onChanged={() => { void load(); onPublicAccessChanged(); }} />{/if}

  {#if revealed}
    <div class="webhook-reveal" role="status" data-webhook-secret-reveal>
      <strong>Copy this URL now. It will not be shown again.</strong>
      <p>Webhook “{revealed.webhook.name}” → {revealed.webhook.project_key} · {revealed.webhook.format === 'coolify' ? 'Coolify notifications' : 'Generic JSON'}</p>
      <div class="webhook-url-row">
        <label class="sr-only" for="webhook-url">Webhook URL</label>
        <input id="webhook-url" readonly value={revealed.url} on:focus={(event) => event.currentTarget.select()} />
        <button class="button quiet-button" type="button" on:click={() => revealed && copy('url', revealed.url)}>{copied === 'url' ? 'Copied' : 'Copy URL'}</button>
      </div>
      {#if revealed.email_address}
        <div class="webhook-url-row">
          <label class="sr-only" for="webhook-email">Webhook email address</label>
          <input id="webhook-email" readonly value={revealed.email_address} data-webhook-email-reveal on:focus={(event) => event.currentTarget.select()} />
          <button class="button quiet-button" type="button" on:click={() => revealed?.email_address && copy('email', revealed.email_address)}>{copied === 'email' ? 'Copied' : 'Copy address'}</button>
        </div>
        <p>Apps that can only send email can use the address instead; it opens tickets the same way.</p>
      {/if}
      <button class="text-button" type="button" on:click={() => { revealed = null; copied = ''; }}>I saved it</button>
    </div>
  {/if}

  {#if revealedEmail}
    <div class="webhook-reveal" role="status" data-webhook-email-only-reveal>
      <strong>Copy this email address now. It will not be shown again.</strong>
      <p>Mail to it opens tickets for webhook “{revealedEmail.webhook.name}” → {revealedEmail.webhook.project_key}. Any earlier address for this webhook has stopped working.</p>
      <div class="webhook-url-row">
        <label class="sr-only" for="webhook-email-only">Webhook email address</label>
        <input id="webhook-email-only" readonly value={revealedEmail.email_address} data-webhook-email-reveal on:focus={(event) => event.currentTarget.select()} />
        <button class="button quiet-button" type="button" on:click={() => revealedEmail && copy('email', revealedEmail.email_address)}>{copied === 'email' ? 'Copied' : 'Copy address'}</button>
      </div>
      <button class="text-button" type="button" on:click={() => { revealedEmail = null; copied = ''; }}>I saved it</button>
    </div>
  {/if}

  {#if user.admin}
    <form class="webhook-create" aria-label="New webhook" on:submit|preventDefault={create}>
      <label>Name<input aria-label="Webhook name" bind:value={name} maxlength="100" placeholder="e.g. Grafana alerts" required /></label>
      <label>Project<select aria-label="Webhook project" bind:value={project}>{#each projects as item (item.id)}<option value={item.key}>{item.key} · {item.name}</option>{/each}</select></label>
      <label>Format<select aria-label="Webhook format" bind:value={format}><option value="generic">Generic JSON</option><option value="coolify">Coolify notifications</option></select></label>
      <label class="webhook-check"><input type="checkbox" bind:checked={assignMe} /> Assign new tickets to me</label>
      <button class="button primary" type="submit" disabled={!name.trim() || saving}>{saving ? 'Creating…' : 'Create webhook URL'}</button>
    </form>

    {#if loading && !hooks.length}
      <p class="optional">Loading webhooks…</p>
    {:else if hooks.length}
      <table class="webhook-table">
        <caption class="sr-only">Ticket webhooks</caption>
        <thead><tr><th scope="col">Name</th><th scope="col">Project</th><th scope="col">Format</th><th scope="col">URL ends</th>{#if email}<th scope="col">Email</th>{/if}<th scope="col">Deliveries</th><th scope="col">Last used</th><th scope="col"><span class="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {#each hooks as hook (hook.id)}
            <tr class:disabled={Boolean(hook.disabled_at)} data-webhook-name={hook.name}>
              <td>{hook.name}{#if hook.disabled_at}{' '}<span class="optional">· disabled</span>{/if}</td>
              <td>{hook.project_key}</td>
              <td>{hook.format === 'coolify' ? 'Coolify' : 'Generic JSON'}</td>
              <td><code>…{hook.secret_hint}</code></td>
              {#if email}<td data-webhook-email>{#if emailHint(hook)}<code>{emailHint(hook)}</code>{:else}<span class="optional">None</span>{/if}</td>{/if}
              <td data-webhook-deliveries>{hook.delivery_count}</td>
              <td>{when(hook.last_delivery_at)}</td>
              <td class="webhook-actions">{#if !hook.disabled_at}{#if publicHostname}<button class="text-button" type="button" disabled={busyId === hook.id} title={`Send a test ticket through https://${publicHostname}`} on:click={() => sendTest(hook)}>{busyId === hook.id && !testResults[hook.id] ? 'Sending…' : 'Send test'}</button>{/if}{#if email}<button class="text-button" type="button" disabled={busyId === hook.id} on:click={() => setEmail(hook)}>Email address…</button>{/if}<button class="text-button" type="button" disabled={busyId === hook.id} on:click={() => rotate(hook)}>Rotate</button><button class="text-button danger" type="button" disabled={busyId === hook.id} on:click={() => disable(hook)}>Disable</button>{/if}</td>
            </tr>
            {#if testResults[hook.id]}
              {@const result = testResults[hook.id]}
              <tr class="webhook-test-row" data-webhook-test-result={hook.name}>
                <td colspan={email ? 8 : 7}>
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

  <section class="webhook-guide" aria-labelledby="webhook-guide-heading">
    <h3 id="webhook-guide-heading">How to send a ticket</h3>
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
    {#if email}
      <p data-webhook-email-guide>Send mail to the webhook's address (<code>{email.local_part}+…@{email.domain}</code>). The subject becomes the title and the text part becomes the description; <code>X-Priority: 1</code> or <code>Importance: high</code> raise the priority. Mail from the same sender with the same subject repeats the open ticket instead of opening another. Attachments are listed but not stored, and messages over 1 MiB bounce.</p>
    {:else}
      <p data-webhook-email-guide>Apps that can only send email can get an address per webhook too. {#if user.admin}Turn on <strong>Email</strong> above.{:else}Ask a workspace administrator to turn on email.{/if}</p>
    {/if}
    <p class="optional">Choose the <strong>Coolify notifications</strong> format to paste the URL into Coolify → Notifications → Webhook. The full reference is in the API document at <a href="/openapi.json">/openapi.json</a> (operation <code>postTicketWebhook</code>).</p>
  </section>
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
  .webhook-create { display: grid; grid-template-columns: 2fr 1.5fr 1.2fr auto auto; gap: 10px; align-items: end; }
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
  .webhook-guide { display: grid; gap: 10px; }
  .webhook-guide h3 { margin: 0; font-size: 14px; }
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
