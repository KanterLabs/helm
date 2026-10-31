<svelte:options runes={true} />

<script lang="ts">
  // Shown instead of the workspace to a verified identity that no owner or
  // administrator has admitted yet. No workspace data is requested.
  import type { Actor, AuthStatus } from '../../types';
  import HelmMark from '../HelmMark.svelte';
  import './membership.css';

  let { user, authStatus, checking = false, error = '', onCheckAgain }: {
    user: Actor;
    authStatus: AuthStatus | null;
    checking?: boolean;
    error?: string;
    onCheckAgain: () => void;
  } = $props();
</script>

<main class="auth-page">
  <section class="auth-panel mship-admission" aria-labelledby="mship-admission-heading">
    <div class="auth-form">
      <HelmMark size={30} className="brand-mark" />
      <div class="form-heading">
        <h2 id="mship-admission-heading">Waiting for workspace access</h2>
        <p>You are signed in{authStatus?.mode === 'cloudflare' ? ' through Cloudflare Access' : ''} as <strong>{user.name}</strong>{user.email ? ` (${user.email})` : ''}.</p>
      </div>
      <p>A workspace owner or administrator has not admitted you yet. Helm shows no projects until they choose your role and project access in Settings → Members.</p>
      {#if error}<p class="inline-alert error" role="alert">{error}</p>{/if}
      <button class="button primary button-large" type="button" disabled={checking} onclick={onCheckAgain}>{#if checking}<span class="button-spinner"></span>{/if}Check again</button>
    </div>
  </section>
</main>
