<script lang="ts">
  import { formatRelative } from '../state';
  import type { EmailIntakeView, PublicEndpointView } from '../types';

  // Whether this Helm is reachable from the internet is a security fact
  // administrators should not have to dig for.
  export let publicAccess: PublicEndpointView;
  export let emailAccess: EmailIntakeView | null = null;

  function publicAccessDetail(access: PublicEndpointView): string {
    const connector = access.connector.state === 'connected' ? 'live' : access.connector.state;
    const test = access.last_test ? ` · last test ${access.last_test.ok ? 'passed' : 'failed'} ${formatRelative(access.last_test.checked_at)}` : '';
    return `· connector ${connector}${test}`;
  }

  $: active = publicAccess.active;
  $: pending = publicAccess.history.filter((item) => item.cleanup_pending).length;
  $: emailPending = emailAccess?.history.filter((item) => item.cleanup_pending).length || 0;
  $: email = emailAccess?.active;
</script>

<section class="public-access" class:is-public={Boolean(active)} aria-label="Public access" data-admin-public-access>
  <div>
    <strong>Public access</strong>
    {#if active}
      <span>Webhook paths are public at <code>https://{active.hostname}</code>{' '}{publicAccessDetail(publicAccess)}</span>
    {:else}
      <span>No public URL. This Helm is reachable only at its own address.</span>
    {/if}
    {#if email}<span data-admin-email-access>Email addresses on at <code>{email.local_part}+…@{email.domain}</code></span>{/if}
    {#if pending}<span class="public-access-warning">{pending} removed public URL{pending === 1 ? ' still has' : 's still have'} resources in Cloudflare.</span>{/if}
    {#if emailPending}<span class="public-access-warning">{emailPending} removed email setup{emailPending === 1 ? ' still has' : 's still have'} a Worker in Cloudflare.</span>{/if}
  </div>
  <a class="button quiet-button" href="/tickets?connect=1">{active || pending || emailPending ? 'Manage' : 'Set up'}</a>
</section>

<style>
  .public-access { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 14px; padding: 10px 14px; border: 1px solid var(--border); border-radius: 11px; background: var(--surface); font-size: var(--text-sm); }
  .public-access > div { display: flex; flex-wrap: wrap; gap: 4px 10px; align-items: baseline; min-width: 0; }
  .public-access.is-public { border-color: color-mix(in srgb, var(--semantic-amber), var(--border) 60%); background: var(--amber-soft); }
  .public-access code { overflow-wrap: anywhere; }
  .public-access-warning { color: var(--semantic-red); font-weight: 600; }
  .public-access a.button { text-decoration: none; }
</style>
