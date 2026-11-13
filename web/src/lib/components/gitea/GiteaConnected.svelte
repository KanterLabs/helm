<script lang="ts">
  import { api } from '../../api';
  import type { GiteaStatus } from '../../types';
  import { relativeTime } from '../github/pullRequests';
  import GiteaDisconnect from './GiteaDisconnect.svelte';
  import GiteaRepositories from './GiteaRepositories.svelte';
  import { displayAddress, errorCode, messageFor, productLabel } from './giteaConnection';

  /** The connected panel: who Helm is signed in as, its access, repositories, test and disconnect. */
  export let status: GiteaStatus;
  export let onChange: (status: GiteaStatus) => void = () => undefined;
  export let announce: (text: string) => void = () => undefined;

  let testing = false;
  let failure = '';

  $: product = productLabel(status.server?.product);
  $: host = displayAddress(status.server?.base_url || '');
  $: version = status.server?.version || '';
  $: problem = status.check?.state === 'unreachable'
    ? `Helm could not reach ${host} at the last check. Other Helm work is unaffected; test again later.`
    : status.check?.state === 'unauthorized'
      ? `${product} refused Helm's sign-in at the last check. Test again; if it keeps failing, disconnect and connect again.`
      : '';

  async function test() {
    if (testing) return;
    testing = true;
    failure = '';
    try {
      const next = await api.checkGiteaConnection();
      onChange(next);
      const nextProblem = next.check?.state === 'ok' ? '' : next.check?.state === 'unreachable' ? `Helm could not reach ${displayAddress(next.server?.base_url || '')}.` : `${product} refused Helm's sign-in.`;
      announce(nextProblem || 'The connection works.');
    } catch (cause) {
      failure = messageFor(errorCode(cause).code, product).text;
      announce(failure);
    } finally {
      testing = false;
    }
  }
</script>

<div class="gitea-connected">
  <div class="gitea-identity">
    <span class="gitea-tick" aria-hidden="true">✓</span>
    <div class="gitea-identity-text">
      <h3 tabindex="-1" data-gitea-connected-heading data-product={product}>Connected to {host} as @{status.account?.login || 'unknown'}</h3>
      <p>{product}{version ? ` ${version}` : ''}{#if status.server?.insecure_http}{' · without encryption'}{/if} · connected {relativeTime(status.connected_at)} · checked {relativeTime(status.checked_at)}</p>
    </div>
    {#if status.access === 'scoped'}<span class="gitea-badge" data-gitea-access="scoped">Read-only access</span>{/if}
  </div>

  {#if status.access === 'full'}
    <div class="inline-alert warning" data-gitea-access="full"><span aria-hidden="true">!</span><span>This server gave Helm more than read-only access to this account. Helm only reads, but use a dedicated account with access to just the repositories Helm needs.</span></div>
  {:else if status.access !== 'scoped'}
    <p class="optional" data-gitea-access="unknown">Helm has not yet confirmed that its access is read-only. Test the connection to check.</p>
  {/if}
  {#if problem}
    <div class="inline-alert warning" data-gitea-check-problem><span aria-hidden="true">!</span><span>{problem}</span></div>
  {/if}
  {#if failure}
    <div class="inline-alert error"><span aria-hidden="true">!</span><span>{failure}</span></div>
  {/if}

  <GiteaRepositories count={status.repository_count} {product} {announce} />

  <div class="gitea-connected-actions">
    <button class="button quiet-button compact-button" type="button" disabled={testing} aria-busy={testing} on:click={test}>{#if testing}<span class="button-spinner gitea-dark-spinner" aria-hidden="true"></span>{/if}Test connection</button>
    <GiteaDisconnect {product} revokeUrl={status.revoke_url || status.register_url} {announce} onDisconnected={onChange} />
  </div>
</div>

<style>
  .gitea-connected { display: grid; gap: 12px; }
  .gitea-identity { display: flex; flex-wrap: wrap; gap: 8px 12px; align-items: flex-start; }
  .gitea-tick { width: 28px; height: 28px; display: grid; flex: 0 0 28px; place-items: center; border-radius: 50%; color: var(--semantic-green); background: var(--green-soft); font-weight: 600; }
  .gitea-identity-text { flex: 1 1 220px; min-width: 0; }
  h3 { margin: 0; font-size: var(--text-base); overflow-wrap: anywhere; }
  /* Focused only by script (after returning from the forge); not a control. */
  h3:focus { outline: none; }
  .gitea-identity-text p { margin: 3px 0 0; color: var(--muted); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .gitea-badge { flex: 0 0 auto; padding: 3px 10px; border: 1px solid var(--green); border-radius: 999px; color: var(--semantic-green); background: var(--green-soft); font-size: var(--text-xs); font-weight: 600; }
  .gitea-connected-actions { display: flex; flex-wrap: wrap; gap: 10px 16px; align-items: center; }
  .gitea-connected-actions > :global(.gitea-confirm) { flex: 1 1 100%; }
  .gitea-dark-spinner { border-color: color-mix(in srgb, var(--ink), transparent 70%); border-top-color: var(--ink); }
  .optional { margin: 0; color: var(--muted); font-size: var(--text-xs); }
  @media (max-width: 640px) {
    .gitea-connected-actions > .button { width: 100%; min-height: 44px; }
  }
</style>
