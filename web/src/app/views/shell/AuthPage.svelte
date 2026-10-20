<svelte:options runes={true} />

<script lang="ts">
  // Sign-in, first-run setup and tailnet access screens.
  import HelmMark from '../../../lib/components/HelmMark.svelte';
  import { bootstrap, submitAuth } from '../../session';
  import { session } from '../../state/session.svelte';
</script>

<main class="auth-page">
  <section class="auth-panel" aria-labelledby="auth-heading">
    <div class="auth-intro">
      <HelmMark size={30} className="brand-mark" />
      <span class="eyebrow">Agent-first planning</span>
      <h1 id="auth-heading">Make progress visible.</h1>
      <p>Helm keeps projects focused, tasks accountable, and every handoff easy to follow.</p>
      <div class="auth-proof">
        <span class="proof-icon">✦</span>
        <span>One calm place for humans and agents to move work forward.</span>
      </div>
    </div>
    {#if session.authView === 'tailnet'}
      <div class="auth-form">
        <div class="form-heading">
          <h2>Tailnet access required</h2>
          <p>Open Helm from the authorized private Tailnet device. Helm does not use a local password in this mode.</p>
        </div>
        {#if session.authError}
          <div class="inline-alert error" role="alert"><span>!</span><span>{session.authError}</span></div>
        {/if}
        <button class="button primary button-large" type="button" on:click={bootstrap} disabled={session.authSubmitting}>Retry private access</button>
      </div>
    {:else}
    <form class="auth-form" on:submit|preventDefault={submitAuth}>
      <div class="form-heading">
        <h2>{session.authView === 'setup' ? 'Create your workspace' : 'Welcome back'}</h2>
        <p>{session.authView === 'setup' ? 'Set up the first administrator account.' : 'Sign in to pick up where you left off.'}</p>
      </div>
      {#if session.authError}
        <div class="inline-alert error" role="alert"><span>!</span><span>{session.authError}</span>{#if session.authBootstrapFailed}<button class="text-button" type="button" on:click={bootstrap}>Retry</button>{/if}</div>
      {/if}
      {#if session.authView === 'setup'}
        <label>Full name<input bind:value={session.setupName} autocomplete="name" placeholder="Alex Morgan" /></label>
        <label>Email<input type="email" bind:value={session.setupEmail} autocomplete="email" placeholder="alex@company.com" /></label>
        <label>Password<input type="password" bind:value={session.setupPassword} minlength="12" autocomplete="new-password" placeholder="At least 12 characters" /></label>
      {:else}
        <label>Email<input type="email" bind:value={session.loginEmail} autocomplete="email" placeholder="you@company.com" /></label>
        <label>Password<input type="password" bind:value={session.loginPassword} autocomplete="current-password" placeholder="Your password" /></label>
      {/if}
      <button class="button primary button-large" type="submit" disabled={session.authSubmitting}>
        {#if session.authSubmitting}<span class="button-spinner"></span>{/if}
        {session.authView === 'setup' ? 'Create workspace' : 'Sign in'}
      </button>
      {#if session.authStatus?.mode === 'local'}
        <button class="text-button auth-switch" type="button" on:click={() => { session.authView = session.authView === 'setup' ? 'login' : 'setup'; session.authError = ''; }}>
          {session.authView === 'setup' ? 'Already have an account? Sign in' : 'First time here? Set up your workspace'}
        </button>
      {/if}
    </form>
    {/if}
  </section>
</main>
