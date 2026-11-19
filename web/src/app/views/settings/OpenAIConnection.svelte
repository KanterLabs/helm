<svelte:options runes={true} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../../lib/api';
  import { friendlyError } from '../../feedback';

  const lifetime = new AbortController();
  let connected = $state<boolean | null>(null);
  let replacing = $state(false);
  let busy = $state(false);
  let key = $state('');
  let error = $state('');

  onMount(() => {
    void load();
    return () => lifetime.abort();
  });

  async function load() {
    busy = true;
    error = '';
    try {
      connected = (await api.openAIConnection(lifetime.signal)).connected;
    } catch (failure) {
      if (!lifetime.signal.aborted) error = friendlyError(failure, 'Could not load the OpenAI connection.');
    } finally {
      busy = false;
    }
  }

  async function update(action: 'save' | 'remove') {
    if (busy || (action === 'save' && !key.trim())) return;
    const submitted = key.trim();
    key = '';
    busy = true;
    error = '';
    try {
      const result = action === 'save'
        ? await api.saveOpenAIKey(submitted, lifetime.signal)
        : await api.removeOpenAIKey(lifetime.signal);
      connected = result.connected;
      replacing = false;
    } catch (failure) {
      if (!lifetime.signal.aborted) error = friendlyError(failure, 'Could not update the OpenAI connection.');
    } finally {
      busy = false;
    }
  }
</script>

<section class="settings-section" aria-labelledby="openai-heading">
  <div class="settings-section-heading">
    <div>
      <h2 id="openai-heading">OpenAI</h2>
      <p>Connect your own project API key for task triage. Usage is billed to your OpenAI project.</p>
    </div>
    {#if connected}<span class="codex-connected">Connected</span>{/if}
  </div>
  {#if error}<div class="inline-alert error" role="alert">{error}</div>{/if}
  {#if connected === null}
    {#if busy}<p role="status">Checking connection…</p>
    {:else}<button class="button quiet-button" type="button" onclick={load}>Retry connection check</button>{/if}
  {:else if !connected || replacing}
    <form onsubmit={(event) => { event.preventDefault(); void update('save'); }}>
      <label>OpenAI API key<input type="password" bind:value={key} autocomplete="off" spellcheck={false} maxlength={512} disabled={busy} /></label>
      <div class="form-actions">
        {#if replacing}
          <button class="text-button" type="button" disabled={busy} onclick={() => { replacing = false; key = ''; error = ''; }}>Cancel</button>
        {/if}
        <button class="button primary" type="submit" disabled={busy || !key.trim()}>{busy ? 'Checking key…' : 'Save key'}</button>
      </div>
    </form>
  {:else}
    <div class="form-actions">
      <button class="button quiet-button" type="button" disabled={busy} onclick={() => { replacing = true; error = ''; }}>Replace key</button>
      <button class="button danger-button" type="button" disabled={busy} onclick={() => update('remove')}>Remove key</button>
    </div>
  {/if}
</section>
