<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { openHelp } from '../help';
  import { formatRelative } from '../state';
  import { ApiError, type EmailIntake, type EmailIntakeView, type EmailReceipt } from '../types';

  export let onChanged: () => void = () => undefined;

  const outcomeLabels: Record<string, string> = {
    created: 'Opened',
    repeated: 'Repeat',
    retained: 'Recorded',
    duplicate: 'Duplicate',
    unknown_recipient: 'Bounced: unknown address',
    unreadable: 'Bounced: unreadable',
    too_large: 'Bounced: too large'
  };

  let view: EmailIntakeView | null = null;
  let unavailable = '';
  let error = '';
  let notice = '';
  let noticeKind: 'success' | 'warning' = 'success';
  let domain = '';
  let localPart = 'helm-alerts';
  let fallback = '';
  let token = '';
  let consentNeeded = '';
  let consent = false;
  let saving = false;
  let removeOpen = false;
  let removeToken = '';
  let cleanupId = '';
  let cleanupToken = '';
  let poll: ReturnType<typeof setInterval> | undefined;

  $: active = view?.active;
  $: earlier = (view?.history || []).filter((item) => item.status !== 'active');
  $: if (view?.suggested_domain && !domain) domain = view.suggested_domain;
  $: preview = `${localPart.trim() || 'helm-alerts'}+<tag>@${domain.trim() || 'example.com'}`;

  function message(value: unknown, fallbackText: string): string {
    return value instanceof Error && value.message ? value.message : fallbackText;
  }

  async function load() {
    try {
      view = await api.getEmailIntake();
      unavailable = '';
    } catch (cause) {
      if (cause instanceof ApiError && cause.code === 'public_endpoints_disabled') unavailable = cause.message;
      else error = message(cause, 'Email status could not be loaded.');
    }
  }

  async function create() {
    if (!domain.trim() || !localPart.trim() || !token.trim() || saving) return;
    saving = true;
    error = '';
    notice = '';
    try {
      view = await api.createEmailIntake({ domain: domain.trim(), local_part: localPart.trim(), fallback_address: fallback.trim() || undefined, enable_subaddressing: consent, api_token: token.trim() });
      notice = `Email is on. Each webhook now gets an address like ${view.active?.local_part}+…@${view.active?.domain}.`;
      noticeKind = 'success';
      consentNeeded = '';
      consent = false;
      onChanged();
    } catch (cause) {
      if (cause instanceof ApiError && cause.code === 'subaddressing_consent_required') {
        consentNeeded = cause.message;
      } else {
        error = message(cause, 'Email could not be set up.');
      }
    } finally {
      token = '';
      saving = false;
    }
  }

  async function remove(intake: EmailIntake, apiToken: string) {
    if (saving) return;
    saving = true;
    error = '';
    try {
      const result = await api.disableEmailIntake(intake.id, apiToken.trim());
      notice = result.warning || (intake.status === 'active' ? `Email addresses at ${intake.domain} are off; the Worker and rule were deleted in Cloudflare.` : `Deleted the Worker and routing rule for ${intake.local_part}@${intake.domain} in Cloudflare.`);
      noticeKind = result.warning ? 'warning' : 'success';
      removeOpen = false;
      cleanupId = '';
      onChanged();
      await load();
    } catch (cause) {
      error = message(cause, 'Email could not be removed.');
    } finally {
      removeToken = '';
      cleanupToken = '';
      saving = false;
    }
  }

  function outcome(receipt: EmailReceipt): string {
    const label = outcomeLabels[receipt.outcome] || receipt.outcome;
    if (receipt.outcome === 'repeated') return `${label} ×${receipt.occurrence_count}`;
    return label;
  }

  function receiptMeta(receipt: EmailReceipt): string {
    const parts = [formatRelative(receipt.received_at), receipt.sender];
    if (receipt.webhook_name) parts.push(`→ ${receipt.webhook_name}`);
    return parts.filter(Boolean).join(' · ');
  }

  function intakeMeta(item: EmailIntake): string {
    const created = `Created ${formatRelative(item.created_at)}${item.created_by_name ? ` by ${item.created_by_name}` : ''}`;
    return item.disabled_at ? `${created} · removed ${formatRelative(item.disabled_at)}` : created;
  }

  onMount(() => {
    void load();
    poll = setInterval(() => {
      if (document.visibilityState === 'visible' && view?.active) void load();
    }, 20_000);
  });
  onDestroy(() => clearInterval(poll));
</script>

<section class="email-intake" aria-labelledby="email-intake-heading">
  <div class="email-heading">
    <h3 id="email-intake-heading">Email</h3>
    {#if view && !unavailable}<span class={`status-badge ${active ? 'badge-on' : 'badge-off'}`} data-email-badge>{active ? 'On' : 'Off'}</span>{/if}
    <button class="text-button learn-more" type="button" on:click={() => openHelp('public-access', 'email-addresses')}>Learn more</button>
  </div>

  {#if unavailable}
    <p class="optional">{unavailable}</p>
  {:else if active}
    <p>Every webhook has its own address. Mail to it opens or repeats a ticket like a webhook POST: the subject becomes the title and the text becomes the description.</p>
    <div class="address-row">
      <code data-email-base>{active.local_part}+&lt;tag&gt;@{active.domain}</code>
      <span class="optional">Use <strong>Email address…</strong> on a webhook below to get its address.</span>
    </div>
    <dl class="email-facts">
      <div><dt>Cloudflare Worker</dt><dd><code data-email-worker>{active.worker_name}</code></dd><dd class="optional">Version {view?.worker_version}</dd></div>
      <div><dt>Routing rule</dt><dd><code>{active.local_part}@{active.domain}</code></dd><dd class="optional">Rule <code>{active.rule_id}</code>{active.subaddress_enabled_by_helm ? ' · plus addressing turned on by Helm' : ''}</dd></div>
      <div><dt>If Helm is unreachable</dt><dd>{#if active.fallback_address}Forwarded to <code>{active.fallback_address}</code>{:else}No fallback address{/if}</dd><dd class="optional">Created {formatRelative(active.created_at)}{active.created_by_name ? ` by ${active.created_by_name}` : ''}</dd></div>
    </dl>

    <div class="recent" data-email-recent>
      <h4>Recent emails</h4>
      {#if view?.recent.length}
        <ul>
          {#each view.recent as receipt (receipt.id)}
            <li data-email-receipt={receipt.subject}>
              <span class={`status-badge outcome-${receipt.outcome}`} data-email-outcome={receipt.outcome}>{outcome(receipt)}</span>
              <span class="receipt-subject">{receipt.subject || '(no subject)'}</span>
              {#if receipt.task_key && receipt.project_slug}<a href={`/p/${receipt.project_slug}/tasks/${receipt.task_key}`}>{receipt.task_key}</a>{/if}
              <span class="optional receipt-meta">{receiptMeta(receipt)}</span>
              {#if receipt.reason}<span class="optional receipt-reason">{receipt.reason}</span>{/if}
            </li>
          {/each}
        </ul>
      {:else}
        <p class="optional">No email yet. Send a message to a webhook's address to test it; it appears here within seconds.</p>
      {/if}
    </div>

    {#if removeOpen}
      <form class="email-form" on:submit|preventDefault={() => active && remove(active, removeToken)}>
        <label>Cloudflare API token <span class="optional">Optional — also deletes the Worker and routing rule</span><input type="password" autocomplete="off" bind:value={removeToken} /></label>
        <div class="email-actions"><button class="text-button" type="button" on:click={() => { removeOpen = false; removeToken = ''; }}>Cancel</button><button class="button danger-button" type="submit" disabled={saving}>{saving ? 'Removing…' : 'Turn off email'}</button></div>
        {#if active.subaddress_enabled_by_helm}<p class="optional">Plus addressing stays on for {active.domain}; turn it off in Cloudflare if nothing else uses it.</p>{/if}
      </form>
    {:else}
      <div class="email-actions"><button class="button quiet-button" type="button" on:click={() => { removeOpen = true; }}>Turn off email…</button></div>
    {/if}
  {:else if view && !view.public_hostname}
    <p class="optional" data-email-needs-public>Email needs a Public URL first: Cloudflare's email Worker delivers each message to Helm through it. Create one above, then come back here.</p>
  {:else if view}
    <p>Give every webhook an email address, for apps that can only send alerts by email. <strong>Connect Cloudflare</strong> above can turn this on for you; experts can set it up by hand.</p>
    <details class="manual-setup" data-manual-email>
    <summary>Set up manually</summary>
    <p class="optional">Cloudflare Email Routing receives the mail and a small Worker passes it to Helm through your Public URL. No mailbox password is stored and no port is opened. The token is used once and never stored.</p>
    <form class="email-form email-setup" on:submit|preventDefault={create}>
      <label>Domain<input aria-label="Email domain" bind:value={domain} placeholder="example.com" autocomplete="off" required /></label>
      <label>Address name<input aria-label="Email address name" bind:value={localPart} autocomplete="off" required /></label>
      <label>Fallback (optional)<input aria-label="Fallback email address" bind:value={fallback} placeholder="you@example.com" autocomplete="off" /></label>
      <label>Cloudflare API token<input aria-label="Cloudflare API token for email" type="password" bind:value={token} autocomplete="off" required /></label>
      <button class="button primary" type="submit" disabled={!domain.trim() || !localPart.trim() || !token.trim() || (Boolean(consentNeeded) && !consent) || saving}>{saving ? 'Setting up…' : 'Turn on email'}</button>
    </form>
    <p class="optional">Addresses will look like <code data-email-preview>{preview}</code>. The fallback receives mail Helm cannot accept (for example while it is down); it must be a verified destination in Cloudflare Email Routing.</p>
    {#if consentNeeded}
      <div class="inline-alert warning" role="note" data-email-consent><span>!</span><div><p>{consentNeeded}</p><label class="consent"><input type="checkbox" bind:checked={consent} /> Turn on plus addressing for this domain</label><p class="optional">Re-enter the token and select <strong>Turn on email</strong> again.</p></div></div>
    {/if}
    <details>
      <summary>Token permissions</summary>
      <ul>{#each view.required_permissions as permission}<li>{permission}</li>{/each}</ul>
      <p class="optional">Email Routing must already be enabled for the domain in Cloudflare (Email › Email Routing).</p>
    </details>
    </details>
  {/if}

  {#if earlier.length}
    <details class="email-history" open={earlier.some((item) => item.cleanup_pending)}>
      <summary>Earlier email setups ({earlier.length})</summary>
      <ul>
        {#each earlier as item (item.id)}
          <li data-email-history={item.id}>
            <div class="history-main">
              <code>{item.local_part}@{item.domain}</code>
              <span class={`status-badge ${item.cleanup_pending ? 'badge-pending' : 'badge-off'}`}>{item.cleanup_pending ? 'Needs cleanup' : 'Removed'}</span>
              <span class="optional">{intakeMeta(item)}</span>
              {#if item.cleanup_pending && cleanupId !== item.id}<button class="text-button" type="button" on:click={() => { cleanupId = item.id; cleanupToken = ''; }}>Finish cleanup…</button>{/if}
            </div>
            {#if item.cleanup_pending}
              <p class="optional">Still in Cloudflare: Worker <code>{item.worker_name}</code> and routing rule <code>{item.rule_id}</code>.</p>
              {#if cleanupId === item.id}
                <form class="email-form" on:submit|preventDefault={() => remove(item, cleanupToken)}>
                  <label>Cloudflare API token<input aria-label={`Cloudflare API token for ${item.local_part}@${item.domain}`} type="password" autocomplete="off" bind:value={cleanupToken} /></label>
                  <div class="email-actions"><button class="text-button" type="button" on:click={() => { cleanupId = ''; cleanupToken = ''; }}>Cancel</button><button class="button danger-button" type="submit" disabled={!cleanupToken.trim() || saving}>{saving ? 'Deleting…' : 'Delete in Cloudflare'}</button></div>
                </form>
              {/if}
            {/if}
          </li>
        {/each}
      </ul>
    </details>
  {/if}

  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<div class={`inline-alert notice-${noticeKind}`} class:warning={noticeKind === 'warning'} role="status"><span>{noticeKind === 'warning' ? '!' : '✓'}</span>{notice}</div>{/if}
</section>

<style>
  .email-intake { display: grid; gap: 10px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .email-heading { display: flex; align-items: center; gap: 8px; }
  .learn-more { margin-left: auto; }
  .manual-setup { display: grid; gap: 8px; }
  .email-intake h3 { margin: 0; font-size: 14px; }
  .email-intake h4 { margin: 0 0 6px; font-size: 12px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.04em; }
  .email-intake p { margin: 0; font-size: 13px; line-height: 1.5; }
  .status-badge { padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: 800; white-space: nowrap; }
  .badge-on, .outcome-created, .outcome-repeated, .outcome-retained { color: var(--semantic-green); background: var(--green-soft); }
  .badge-off { color: var(--muted); background: var(--surface); border: 1px solid var(--border); }
  .badge-pending, .outcome-unknown_recipient, .outcome-unreadable, .outcome-too_large { color: var(--semantic-red); background: var(--red-soft); }
  .address-row { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; }
  .address-row code { font-size: 14px; font-weight: 800; overflow-wrap: anywhere; }
  .email-facts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin: 0; }
  .email-facts > div { display: grid; align-content: start; gap: 3px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  dt { color: var(--muted); font-size: 11px; font-weight: 800; text-transform: uppercase; letter-spacing: 0.04em; }
  dd { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .recent ul, .email-history ul { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .recent li { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; padding: 7px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); font-size: 13px; }
  .receipt-subject { font-weight: 700; overflow-wrap: anywhere; }
  .receipt-meta, .receipt-reason { flex-basis: 100%; }
  .receipt-reason { color: var(--semantic-red); }
  .email-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; align-items: end; }
  .email-setup { grid-template-columns: 1fr 1fr 1fr 1fr auto; }
  .email-form label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .consent { display: flex !important; align-items: center; gap: 6px; margin: 6px 0; }
  .email-actions { display: flex; flex-wrap: wrap; gap: 10px; align-items: center; }
  .danger-button { color: var(--semantic-red); border-color: color-mix(in srgb, var(--semantic-red), var(--border) 60%); background: var(--red-soft); }
  .notice-success { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .notice-success > span:first-child { background: var(--semantic-green); }
  .optional { color: var(--muted); font-size: 12px; font-weight: 400; }
  details { font-size: 12px; }
  details summary { cursor: pointer; font-weight: 700; }
  details ul { margin: 6px 0; padding-left: 18px; }
  .email-history li { display: grid; gap: 6px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  .history-main { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  @media (max-width: 900px) {
    .email-setup, .email-form, .email-facts { grid-template-columns: minmax(0, 1fr); }
  }
</style>
