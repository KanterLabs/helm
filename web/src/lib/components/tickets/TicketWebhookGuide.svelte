<script lang="ts">
  import { openHelp } from '../../help';

  // "How to send a ticket": the request shape, a runnable curl example and
  // the accepted fields. Shown on demand from the Webhooks tab.
  /** The just-created URL, the URL base plus <secret>, or empty for the origin. */
  export let exampleURL = '';
  /** Public hostname used by Send test, when one is live. */
  export let publicHostname = '';
  /** Admins create inboxes; others ask an administrator. */
  export let admin = true;

  const fields: { name: string; required: boolean; text: string }[] = [
    { name: 'title', required: true, text: 'Ticket title shown in the queue (max 300 characters).' },
    { name: 'description', required: false, text: 'Markdown details shown under “What needs attention”.' },
    { name: 'priority', required: false, text: 'low, normal (default), high or urgent.' },
    { name: 'dedupe_key', required: false, text: 'Stable ID for the problem, e.g. db-1:disk. Repeats while the ticket is open only add to its count; after it is completed the next post opens a new linked ticket. Leave it out to open a ticket on every post.' },
    { name: 'source', required: false, text: 'Name of the sending app, e.g. grafana.' },
    { name: 'url', required: false, text: 'Link back to the sending app.' },
    { name: 'fields', required: false, text: 'Up to 20 short facts as an object, e.g. {"host": "db-1"}. Shown as evidence on the ticket.' }
  ];

  let copied = false;

  $: url = exampleURL || `${window.location.origin}/api/v1/hooks/tickets/<secret>`;
  $: curlExample = `curl -X POST '${url}' \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title": "Disk almost full on db-1", "priority": "high", "dedupe_key": "db-1:disk", "source": "grafana", "fields": {"host": "db-1", "usage": "93%"}}'`;
  $: curlExample, copied = false;

  async function copy() {
    try {
      await navigator.clipboard.writeText(curlExample);
      copied = true;
    } catch {
      copied = false;
    }
  }
</script>

<section class="webhook-guide" aria-labelledby="webhook-guide-heading" data-webhook-guide>
  <header class="conn-head">
    <h3 id="webhook-guide-heading">How to send a ticket</h3>
    <button class="text-button" type="button" on:click={() => openHelp('ticket-webhooks')}>Full guide</button>
  </header>
  <p>POST a JSON object to the webhook URL with <code>Content-Type: application/json</code>. No other authentication is needed: keep the URL private, and rotate it if it leaks.</p>
  <div class="webhook-code">
    <pre data-webhook-curl><code>{curlExample}</code></pre>
    <button class="button quiet-button" type="button" on:click={copy}>{copied ? 'Copied' : 'Copy example'}</button>
  </div>
  <div class="conn-table-wrap">
    <table class="conn-table webhook-fields">
      <caption class="sr-only">Accepted JSON fields</caption>
      <thead><tr><th scope="col">Field</th><th scope="col">Required</th><th scope="col">Meaning</th></tr></thead>
      <tbody>{#each fields as field (field.name)}<tr><td><code>{field.name}</code></td><td>{field.required ? 'Yes' : 'No'}</td><td>{field.text}</td></tr>{/each}</tbody>
    </table>
  </div>
  <p><strong>Response.</strong> <code>201</code> with <code>{'{"disposition": "created", "ticket": {"key": "OPS-61", "url": "…"}}'}</code> for a new ticket, or <code>200</code> with <code>"repeated"</code> when a <code>dedupe_key</code> matched an open ticket. A bad body returns <code>400</code> and names the field to fix in <code>error.details.field</code>; unknown fields are rejected so typos are caught. An unknown, rotated or disabled URL returns <code>404</code>.</p>
  {#if publicHostname}<p data-webhook-test-guide><strong>Check it end to end.</strong> <strong>Send test</strong> on a webhook makes Helm post a test ticket to <code>https://{publicHostname}</code>, through Cloudflare and the tunnel, exactly as an outside app would. Repeated tests count on one low-priority test ticket.</p>{/if}
  <h4 id="webhook-email-heading">Sending by email</h4>
  <p data-webhook-email-guide>Apps that send alerts by email use an <strong>Email inbox</strong> instead of a webhook: {admin ? 'create one on the Email tab' : 'ask an administrator for one'} and point the app at its address.</p>
  <p class="optional">Choose the <strong>Coolify notifications</strong> format to paste the URL into Coolify → Notifications → Webhook. The full reference is in the API document at <a href="/openapi.json">/openapi.json</a> (operation <code>postTicketWebhook</code>).</p>
</section>

<style>
  .webhook-guide { display: grid; gap: 10px; padding-top: 14px; border-top: 1px solid var(--border); }
  .webhook-guide h4 { margin: 4px 0 0; font-size: var(--text-sm); }
  .webhook-guide p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.55; }
  .webhook-guide p.optional { color: var(--muted); font-size: var(--text-xs); }
  .webhook-code { display: grid; gap: 6px; justify-items: start; min-width: 0; }
  .webhook-code pre { box-sizing: border-box; width: 100%; margin: 0; padding: 12px; overflow-x: auto; border-radius: 8px; background: var(--surface-muted); font-size: var(--text-xs); line-height: 1.5; white-space: pre-wrap; word-break: break-all; }
  .webhook-fields td:first-child { white-space: nowrap; }
</style>
