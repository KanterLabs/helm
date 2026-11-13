<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../../api';
  import type { GiteaProbeResult, GiteaStatus } from '../../types';
  import GiteaAppStep from './GiteaAppStep.svelte';
  import GiteaMessage from './GiteaMessage.svelte';
  import { displayAddress, errorCode, looksLikeHost, messageFor, productLabel, type Draft, type Message, type ProductLabel } from './giteaConnection';

  /** Steps 1 and 2 of docs/GITEA_OAUTH_UX.md, one active at a time. */
  export let status: GiteaStatus;
  /** Values from a failed sign-in and its error code, when returning from the forge. */
  export let restore: { draft: Draft | null; code: string } | null = null;
  export let announce: (text: string) => void = () => undefined;
  export let onProduct: (product: ProductLabel) => void = () => undefined;
  export let onShowConnection: () => void = () => undefined;

  type Source = 'type' | 'submit' | 'paste' | 'blur' | 'retry' | 'consent' | 'restore';

  let root: HTMLElement;
  let addressInput: HTMLInputElement;
  let consentBox: HTMLInputElement;
  let address = '';
  let allowHttp = false;
  let checking = false;
  let checkingLabel = '';
  let error: Message | null = null;
  let errorValue = '';
  let probe: GiteaProbeResult | null = null;
  let clientId = '';
  let result: Message | null = null;
  let lastKey = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  let controller: AbortController | null = null;

  $: product = probe ? productLabel(probe.server.product) : productLabel(status.server?.product);
  $: onProduct(product);

  function typed() {
    clearTimeout(timer);
    error = null;
    if (looksLikeHost(address)) timer = setTimeout(() => void check('type'), 400);
  }

  async function check(source: Source): Promise<void> {
    clearTimeout(timer);
    const value = address.trim();
    if (!value) {
      if (source === 'submit') fail(messageFor('invalid_server_url', product), source, value);
      return;
    }
    if (source === 'submit' && error?.code === 'insecure_http' && !allowHttp && value === errorValue) {
      consentBox?.focus();
      return;
    }
    const key = `${value}\n${allowHttp}`;
    const forced = source === 'submit' || source === 'retry' || source === 'consent' || source === 'restore';
    if (key === lastKey && (checking || !forced)) return;
    lastKey = key;
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    checking = true;
    checkingLabel = displayAddress(value);
    error = null;
    try {
      const found = await api.probeGitea({ base_url: value, allow_insecure_http: allowHttp }, mine.signal);
      if (controller !== mine) return;
      probe = { ...found, redirect_uri: found.redirect_uri || status.redirect_uri };
      const name = productLabel(found.server.product);
      announce(`Found ${name}${found.server.version ? ` ${found.server.version}` : ''} at ${displayAddress(found.server.base_url)}.`);
      if (source !== 'restore') {
        await tick();
        root?.querySelector<HTMLElement>('#gitea-step2-title')?.focus();
      }
    } catch (cause) {
      if (controller !== mine) return;
      const { code, details } = errorCode(cause);
      fail(messageFor(code, product, details, status.unavailable_reason), source, value);
    } finally {
      if (controller === mine) {
        controller = null;
        checking = false;
      }
    }
  }

  function fail(message: Message, source: Source, value: string) {
    error = message;
    errorValue = value;
    announce(message.text);
    if (source !== 'blur' && source !== 'restore') void tick().then(() => addressInput?.focus());
  }

  async function change() {
    probe = null;
    result = null;
    lastKey = '';
    await tick();
    addressInput?.focus();
  }

  function useIssuer(issuer: string) {
    address = issuer;
    void check('submit');
  }

  onMount(async () => {
    if (!restore) return;
    const { draft, code } = restore;
    if (draft) {
      address = draft.base_url;
      allowHttp = draft.allow_insecure_http;
      clientId = draft.client_id;
      await check('restore');
    }
    result = messageFor(code, probe ? productLabel(probe.server.product) : product, {}, status.unavailable_reason);
    await tick();
    root?.querySelector<HTMLElement>('[data-gitea-result]')?.focus();
  });
  onDestroy(() => { clearTimeout(timer); controller?.abort(); });
</script>

<div class="gitea-setup" bind:this={root}>
  {#if result && !probe}
    <GiteaMessage message={result} {product} result revokeUrl={status.revoke_url} registerUrl={status.register_url} onRetry={() => { result = null; void tick().then(() => addressInput?.focus()); }} {onShowConnection} />
  {/if}
  <ol class="gitea-steps">
    <li class="gitea-step" class:done={probe} aria-current={probe ? undefined : 'step'}>
      <span class="gitea-step-num" aria-hidden="true">{probe ? '✓' : '1'}</span>
      <div class="gitea-step-body">
        {#if probe}
          <p class="gitea-found" data-gitea-found>
            <span>Found {product}{probe.server.version ? ` ${probe.server.version}` : ''} at <strong>{displayAddress(probe.server.base_url)}</strong>{#if probe.server.insecure_http}<span class="gitea-insecure"> · without encryption</span>{/if}</span>
            <button class="text-button" type="button" aria-label="Change server address" on:click={change}>Change</button>
          </p>
        {:else}
          <h3 id="gitea-step1-title">Where is your {product}?</h3>
          <form class="gitea-field-row" on:submit|preventDefault={() => void check('submit')}>
            <label class="gitea-label" for="gitea-address">Server address</label>
            <input id="gitea-address" class="gitea-input" type="text" bind:this={addressInput} bind:value={address}
              on:input={typed} on:paste={() => setTimeout(() => void check('paste'), 0)} on:blur={() => void check('blur')}
              placeholder="gitea.example.com" inputmode="url" autocapitalize="off" autocomplete="off" spellcheck="false" maxlength="255"
              aria-invalid={error && error.code !== 'insecure_http' ? 'true' : undefined}
              aria-describedby={`gitea-address-help${error ? ' gitea-address-error' : ''}`} />
            <button class="button primary gitea-continue" type="submit">Continue</button>
          </form>
          <p id="gitea-address-help" class="optional">The address you open {product} at, or any page link copied from it.</p>
          <div class="gitea-result-slot">
            {#if checking}
              <p class="gitea-checking" data-gitea-checking><span class="button-spinner gitea-dark-spinner" aria-hidden="true"></span>Checking {checkingLabel}…</p>
            {:else if error?.code === 'insecure_http'}
              <div class="inline-alert warning" data-gitea-http-warning>
                <span aria-hidden="true">!</span>
                <div class="gitea-consent">
                  <p id="gitea-address-error">{error.text}</p>
                  <label><input type="checkbox" bind:this={consentBox} bind:checked={allowHttp} on:change={() => { if (allowHttp) void check('consent'); }} /> Continue without encryption</label>
                </div>
              </div>
            {:else if error}
              <GiteaMessage message={error} {product} textId="gitea-address-error" onRetry={() => void check('retry')} onUseIssuer={useIssuer} {onShowConnection} />
            {/if}
          </div>
        {/if}
      </div>
    </li>
    <li class="gitea-step" class:upcoming={!probe} aria-current={probe ? 'step' : undefined}>
      <span class="gitea-step-num" aria-hidden="true">2</span>
      <div class="gitea-step-body">
        {#if probe}
          <GiteaAppStep {probe} {product} bind:clientId bind:result revokeUrl={status.revoke_url || probe.register_url} unavailableReason={status.unavailable_reason} {announce} {onShowConnection} />
        {:else}
          <p class="gitea-upcoming">Create Helm's application on {product}</p>
        {/if}
      </div>
    </li>
    <li class="gitea-step upcoming">
      <span class="gitea-step-num" aria-hidden="true">3</span>
      <div class="gitea-step-body"><p class="gitea-upcoming">Approve on {product}, and you come back here connected</p></div>
    </li>
  </ol>
</div>

<style>
  .gitea-setup { display: grid; gap: 12px; min-width: 0; }
  .gitea-steps { display: grid; gap: 0; margin: 0; padding: 0; list-style: none; }
  .gitea-step { position: relative; display: grid; grid-template-columns: 28px minmax(0, 1fr); gap: 12px; padding-bottom: 16px; }
  .gitea-step:not(:last-child)::before { content: ''; position: absolute; top: 30px; bottom: 2px; left: 13px; width: 2px; border-radius: 1px; background: var(--border); }
  .gitea-step-num { position: relative; z-index: 1; width: 28px; height: 28px; display: grid; place-items: center; border-radius: 50%; color: #fff; background: var(--purple); font-size: var(--text-xs); font-weight: 600; }
  .gitea-step.done .gitea-step-num { color: var(--semantic-green); background: var(--green-soft); }
  .gitea-step.upcoming .gitea-step-num { color: var(--muted); background: var(--surface-muted); border: 1px solid var(--border-strong); }
  .gitea-step-body { display: grid; gap: 8px; min-width: 0; padding-top: 3px; }
  h3 { margin: 0; font-size: var(--text-base); }
  .gitea-field-row { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 6px 8px; align-items: center; }
  .gitea-label { grid-column: 1 / -1; color: var(--ink-soft); font-size: var(--text-xs); font-weight: 600; }
  .gitea-input { width: 100%; min-width: 0; height: 40px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: 8px; color: var(--ink); background: var(--surface); font-size: var(--text-sm); }
  .gitea-input[aria-invalid='true'] { border-color: var(--red); }
  .gitea-continue { min-height: 40px; padding: 0 16px; }
  .gitea-result-slot { min-height: 40px; }
  .gitea-checking { display: flex; gap: 8px; align-items: center; min-height: 38px; margin: 0; color: var(--ink-soft); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .gitea-dark-spinner { flex: 0 0 auto; border-color: color-mix(in srgb, var(--purple), transparent 70%); border-top-color: var(--purple); }
  .gitea-consent { display: grid; gap: 6px; }
  .gitea-consent p { margin: 0; }
  .gitea-consent label { display: flex; gap: 8px; align-items: center; min-height: 28px; color: var(--ink); font-weight: 600; cursor: pointer; }
  .gitea-consent input { width: 16px; height: 16px; margin: 0; }
  .gitea-found { display: flex; flex-wrap: wrap; gap: 4px 12px; align-items: baseline; margin: 3px 0 0; color: var(--ink); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .gitea-found span { min-width: 0; }
  .gitea-insecure { color: var(--semantic-amber); font-weight: 600; }
  .gitea-upcoming { margin: 3px 0 0; color: var(--muted); font-size: var(--text-sm); }
  .optional { margin: 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.45; }
  @media (prefers-reduced-motion: reduce) { .gitea-dark-spinner { animation-duration: 2.4s; } }
  @media (max-width: 640px) {
    .gitea-field-row { grid-template-columns: minmax(0, 1fr); }
    .gitea-input { height: 44px; }
    .gitea-continue { width: 100%; min-height: 44px; }
    .gitea-found .text-button, .gitea-consent label { min-height: 44px; }
  }
</style>
