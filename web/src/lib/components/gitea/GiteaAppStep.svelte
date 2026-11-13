<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../api';
  import type { GiteaProbeResult } from '../../types';
  import GiteaCopyValue from './GiteaCopyValue.svelte';
  import GiteaMessage from './GiteaMessage.svelte';
  import { authorizeAllowed, CLIENT_ID_PATTERN, errorCode, extractClientId, messageFor, saveDraft, type Message, type ProductLabel } from './giteaConnection';

  /** Step 2: create the OAuth2 application on the forge, paste its client ID, sign in. */
  export let probe: GiteaProbeResult;
  export let product: ProductLabel = 'Gitea';
  export let clientId = '';
  export let result: Message | null = null;
  export let revokeUrl = '';
  export let unavailableReason = '';
  export let announce: (text: string) => void = () => undefined;
  export let onShowConnection: () => void = () => undefined;

  let root: HTMLElement;
  let clientInput: HTMLInputElement;
  let touched = Boolean(clientId);
  let busy = false;

  $: valid = CLIENT_ID_PATTERN.test(clientId);
  $: showClientError = touched && clientId !== '' && !valid;

  function pasted(event: ClipboardEvent) {
    const text = event.clipboardData?.getData('text/plain');
    if (text === undefined) return;
    event.preventDefault();
    clientId = extractClientId(text);
    touched = true;
  }

  async function fail(message: Message) {
    result = message;
    busy = false;
    announce(message.text);
    await tick();
    root?.querySelector<HTMLElement>('[data-gitea-result]')?.focus();
  }

  async function signIn() {
    if (busy || !valid) return;
    busy = true;
    result = null;
    const input = { base_url: probe.server.base_url, client_id: clientId, allow_insecure_http: probe.server.insecure_http };
    saveDraft(input);
    try {
      const { authorize_url } = await api.startGiteaSignIn(input);
      if (!authorizeAllowed(authorize_url, probe.server.base_url)) return void fail(messageFor('authorize_outside', product));
      // Keep the button disabled: the page is leaving for the forge.
      window.location.assign(authorize_url);
    } catch (cause) {
      const { code, details } = errorCode(cause);
      void fail(messageFor(code, product, details, unavailableReason));
    }
  }

  async function focusClient() {
    await tick();
    clientInput?.focus();
    clientInput?.select();
  }
</script>

<svelte:window on:pageshow={(event) => { if (event.persisted) busy = false; }} />

<div class="gitea-app-step" bind:this={root}>
  <h3 id="gitea-step2-title" tabindex="-1">Create Helm's application in {product}</h3>
  <ol class="gitea-actions">
    <li>
      <span class="gitea-action-num" aria-hidden="true">1</span>
      <div><a class="button quiet-button gitea-open" href={probe.register_url} target="_blank" rel="noopener noreferrer">Open {product}'s application page <span aria-hidden="true">↗</span></a></div>
    </li>
    <li>
      <span class="gitea-action-num" aria-hidden="true">2</span>
      <div class="gitea-action-body">
        <p>Under <em>Create a new OAuth2 Application</em>, enter these:</p>
        <dl class="gitea-values">
          <GiteaCopyValue label="Application name" value="Helm" noun="application name" {announce} />
          <GiteaCopyValue label="Redirect URI" value={probe.redirect_uri} noun="redirect URI" attrs={{ 'data-gitea-redirect-uri': '' }} {announce} />
        </dl>
        <p class="gitea-untick"><span class="gitea-box" aria-hidden="true"></span><span><strong>Untick "Confidential Client"</strong>. Helm keeps no client secret, so the application must not be confidential.</span></p>
      </div>
    </li>
    <li>
      <span class="gitea-action-num" aria-hidden="true">3</span>
      <div class="gitea-action-body">
        <label class="gitea-label" for="gitea-client-id">Paste the Client ID {product} shows you</label>
        <input id="gitea-client-id" class="gitea-input" type="text" bind:this={clientInput} bind:value={clientId} on:paste={pasted}
          on:input={() => { clientId = clientId.trim(); }} on:blur={() => { touched = true; }}
          inputmode="text" autocapitalize="off" autocomplete="off" spellcheck="false" placeholder="3f2b9c1e-…" maxlength="512"
          aria-invalid={showClientError ? 'true' : undefined} aria-describedby={showClientError ? 'gitea-client-error' : undefined} />
        {#if showClientError}<p id="gitea-client-error" class="gitea-field-error">That does not look like a Client ID. Copy it again from the application page.</p>{/if}
      </div>
    </li>
  </ol>
  <div class="gitea-signin">
    <button class="button primary gitea-primary" type="button" disabled={!valid || busy} aria-busy={busy} on:click={signIn}>{#if busy}<span class="button-spinner" aria-hidden="true"></span>{/if}Sign in with {product}</button>
    <p class="optional">If {product} shows <em>Unregistered Redirect URI</em>, the redirect URI saved in the application does not match the one above. Copy it again.</p>
  </div>
  {#if result}
    <GiteaMessage message={result} {product} result {revokeUrl} registerUrl={probe.register_url} onRetry={signIn} {onShowConnection} onFocusClient={focusClient} />
  {/if}
</div>

<style>
  .gitea-app-step { display: grid; gap: 12px; min-width: 0; animation: gitea-in var(--dur-base) var(--ease-out); }
  h3 { margin: 0; font-size: var(--text-base); }
  /* Focused only by script when the step opens; not a control. */
  h3:focus { outline: none; }
  em { color: var(--ink); font-style: normal; font-weight: 600; }
  .gitea-open { text-decoration: none; }
  .gitea-actions { display: grid; gap: 14px; margin: 0; padding: 0; list-style: none; }
  .gitea-actions > li { display: grid; grid-template-columns: 24px minmax(0, 1fr); gap: 10px; align-items: start; }
  .gitea-action-num { width: 24px; height: 24px; display: grid; place-items: center; border-radius: 50%; color: var(--purple); background: var(--purple-soft); font-size: var(--text-xs); font-weight: 600; }
  .gitea-action-body { display: grid; gap: 8px; min-width: 0; }
  .gitea-action-body > p { margin: 2px 0 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.45; }
  .gitea-values { display: grid; gap: 8px; margin: 0; }
  .gitea-untick { display: flex; gap: 10px; align-items: flex-start; margin: 0; padding: 9px 11px; border: 1px solid color-mix(in srgb, var(--amber), var(--border) 65%); border-radius: 8px; color: var(--ink-soft); background: var(--amber-soft); font-size: var(--text-sm); line-height: 1.45; }
  .gitea-box { width: 16px; height: 16px; flex: 0 0 16px; margin-top: 2px; border: 2px solid var(--semantic-amber); border-radius: 4px; background: var(--surface); }
  .gitea-label { color: var(--ink); font-size: var(--text-sm); font-weight: 600; }
  .gitea-input { width: 100%; min-width: 0; height: 40px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: 8px; color: var(--ink); background: var(--surface); font-family: var(--font-mono); font-size: var(--text-sm); }
  .gitea-input[aria-invalid='true'] { border-color: var(--red); }
  .gitea-field-error { margin: 0; color: var(--semantic-red); font-size: var(--text-xs); }
  .gitea-signin { display: grid; gap: 6px; justify-items: start; }
  .gitea-primary { min-height: 40px; padding: 0 18px; font-size: var(--text-sm); }
  .optional { margin: 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.45; }
  @keyframes gitea-in { from { opacity: 0; transform: translateY(4px); } }
  @media (prefers-reduced-motion: reduce) { .gitea-app-step { animation: none; } }
  @media (max-width: 640px) {
    .gitea-open, .gitea-primary { width: 100%; min-height: 44px; }
    .gitea-signin { justify-items: stretch; }
    .gitea-input { height: 44px; }
  }
</style>
