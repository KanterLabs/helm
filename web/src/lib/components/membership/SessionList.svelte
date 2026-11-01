<svelte:options runes={true} />

<script lang="ts">
  // Local cookie sessions for the signed-in human, or an administrator's view
  // of an admitted member. The server never says which session belongs to
  // this browser, so none is labelled "current".
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type SessionDescriptor } from '../../types';
  import { deviceLabel, formatMoment, rawDevice, sessionState, sortSessions } from './access';
  import { accessGeneration, describeError } from './membersState.svelte';
  import type { ConfirmAction } from './types';

  let { label, actorId, actorName = '', autoLoad = false, refreshToken = 0, confirmAction, onSelfRevoked }: {
    /** Accessible region name, e.g. "Signed-in sessions". */
    label: string;
    /** Administrator target; omitted for the signed-in human's own sessions. */
    actorId?: string;
    actorName?: string;
    autoLoad?: boolean;
    /** Increment to reload after a password change or reset. */
    refreshToken?: number;
    confirmAction: ConfirmAction;
    /** Called when revoking one of your own sessions ended this browser's sign-in. */
    onSelfRevoked?: () => void | Promise<void>;
  } = $props();

  let sessions = $state.raw<SessionDescriptor[]>([]);
  let loaded = $state(false);
  let loading = $state(false);
  let error = $state('');
  let notice = $state('');
  let revoking = $state('');
  let showAll = $state(false);
  const visibleLimit = 5;
  let alive = true;
  let seenToken = 0;
  const headingId = `mship-sessions-${Math.random().toString(36).slice(2, 9)}`;
  const active = $derived(sessions.filter((session) => sessionState(session) === 'active'));
  const ended = $derived(sessions.filter((session) => sessionState(session) !== 'active'));

  async function load() {
    const generation = accessGeneration();
    loading = true;
    error = '';
    try {
      const result = await api.listSessions(actorId);
      if (!alive || generation !== accessGeneration()) return;
      sessions = sortSessions(result.data);
      loaded = true;
    } catch (failure) {
      if (!alive || generation !== accessGeneration()) return;
      if (failure instanceof ApiError && (failure.status === 401 || failure.status === 403)) {
        sessions = [];
        loaded = false;
        if (!actorId && failure.status === 401) { await onSelfRevoked?.(); return; }
      }
      error = describeError(failure, 'Sessions could not be loaded.');
    } finally {
      if (alive && generation === accessGeneration()) loading = false;
    }
  }

  async function revoke(session: SessionDescriptor) {
    const device = deviceLabel(session);
    const confirmed = await confirmAction({
      title: 'Revoke this session?',
      message: actorId
        ? `${actorName || 'This member'} will be signed out on ${device}. Other sessions stay signed in.`
        : `${device} will be signed out. Helm cannot tell which listed session is this browser; if it is, you will be signed out here too.`,
      confirmLabel: 'Revoke session'
    });
    if (!confirmed || !alive) return;
    const generation = accessGeneration();
    revoking = session.id;
    error = '';
    notice = '';
    try {
      await api.revokeSession(session.id, actorId);
      if (!alive || generation !== accessGeneration()) return;
      if (!actorId) {
        const status = await api.authStatus().catch(() => null);
        if (!alive || generation !== accessGeneration()) return;
        if (status && !status.authenticated) {
          await onSelfRevoked?.();
          return;
        }
      }
      notice = 'Session revoked.';
      await load();
    } catch (failure) {
      if (alive && generation === accessGeneration()) error = describeError(failure, 'The session could not be revoked.');
    } finally {
      if (alive) revoking = '';
    }
  }

  $effect(() => {
    if (refreshToken !== seenToken) {
      seenToken = refreshToken;
      if (loaded) void load();
    }
  });
  onMount(() => { if (autoLoad) void load(); });
  onDestroy(() => { alive = false; });
</script>

{#snippet row(session: SessionDescriptor)}
  {@const status = sessionState(session)}
  <li class="mship-session">
    <div>
      <strong title={rawDevice(session)}>{deviceLabel(session)}</strong>
      <small>Last active {formatMoment(session.last_seen_at)} · Signed in {formatMoment(session.created_at)}{session.ip_address ? ` · IP ${session.ip_address}` : ''}</small>
      <small>{status === 'revoked' ? `Revoked ${formatMoment(session.revoked_at)}` : status === 'expired' ? `Expired ${formatMoment(session.expires_at)}` : `Expires ${formatMoment(session.expires_at)}`}</small>
    </div>
    {#if status === 'active'}
      <button class="button quiet-button compact-button danger-button" type="button" disabled={Boolean(revoking)} aria-label={`Revoke session: ${deviceLabel(session)}, last active ${formatMoment(session.last_seen_at)}`} onclick={() => revoke(session)}>{#if revoking === session.id}<span class="button-spinner"></span>{/if}Revoke</button>
    {:else}
      <span class="mship-badge state-ended">{status === 'revoked' ? 'Revoked' : 'Expired'}</span>
    {/if}
  </li>
{/snippet}

<section class="mship-card" aria-labelledby={headingId}>
  <div class="mship-actions start">
    <h3 id={headingId}>{label}</h3>
    {#if actorId || loaded || error}
      <button class="button quiet-button compact-button" type="button" disabled={loading} onclick={load}>{#if loading}<span class="button-spinner"></span>{/if}{loaded ? 'Refresh sessions' : 'Show sessions'}</button>
    {/if}
  </div>
  {#if error}<div class="inline-alert error" role="alert"><span>!</span>{error}</div>{/if}
  {#if notice}<p class="mship-status" role="status">{notice}</p>{/if}
  {#if loading && !loaded}
    <div class="list-skeleton" aria-label="Loading sessions"><div></div></div>
  {:else if loaded}
    {#if active.length}
      <ul class="mship-sessions">{#each showAll ? active : active.slice(0, visibleLimit) as session (session.id)}{@render row(session)}{/each}</ul>
      {#if active.length > visibleLimit}
        <button class="text-button" type="button" aria-expanded={showAll} onclick={() => { showAll = !showAll; }}>{showAll ? 'Show fewer sessions' : `Show all ${active.length} active sessions`}</button>
      {/if}
    {:else}
      <p class="mship-note">No active sessions.</p>
    {/if}
    {#if ended.length}
      <details class="mship-ended"><summary>Ended sessions ({ended.length})</summary><ul class="mship-sessions">{#each ended as session (session.id)}{@render row(session)}{/each}</ul></details>
    {/if}
    <p class="mship-note">Shows up to the 200 most recent sessions. Revoking signs that browser out immediately.</p>
  {/if}
</section>
