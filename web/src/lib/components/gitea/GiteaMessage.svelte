<script lang="ts">
  import { openHelp } from '../../help';
  import type { Message } from './giteaConnection';

  /** A failure message with the one action that fixes it (docs/GITEA_OAUTH_UX.md, Messages). */
  export let message: Message;
  export let product = 'Gitea';
  export let textId = '';
  export let tone: 'error' | 'warning' = 'error';
  /** Marks the result of a sign-in attempt: focusable, so focus can land on it. */
  export let result = false;
  export let revokeUrl = '';
  export let registerUrl = '';
  export let onRetry: () => void = () => undefined;
  export let onUseIssuer: (issuer: string) => void = () => undefined;
  export let onShowConnection: () => void = () => undefined;
  export let onFocusClient: () => void = () => undefined;
</script>

<!-- Focus target after a sign-in attempt (tabindex -1 only); never in the tab order. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class={`inline-alert ${tone} gitea-message`} data-gitea-result={result ? message.code : undefined} data-gitea-message={message.code} tabindex={result ? -1 : undefined}>
  <span aria-hidden="true">!</span>
  <div class="gitea-message-body">
    <p id={textId || undefined}>{message.text}</p>
    {#if message.action === 'retry'}
      <button class="text-button" type="button" on:click={onRetry}>Try again</button>
    {:else if message.action === 'guide'}
      <button class="text-button" type="button" on:click={() => openHelp('gitea-oauth', 'private-certificate-authorities')}>Open the setup guide</button>
    {:else if message.action === 'use-issuer' && message.issuer}
      <button class="text-button" type="button" on:click={() => onUseIssuer(message.issuer || '')}>Use that address</button>
    {:else if message.action === 'show-connection'}
      <button class="text-button" type="button" on:click={onShowConnection}>Show the connection</button>
    {:else if message.action === 'focus-client'}
      <button class="text-button" type="button" on:click={onFocusClient}>Edit the Client ID</button>
    {:else if message.action === 'revoke-link' && revokeUrl}
      <a class="text-button" href={revokeUrl} target="_blank" rel="noopener noreferrer">Open Authorized OAuth2 Applications ↗</a>
    {:else if message.action === 'register-link' && registerUrl}
      <a class="text-button" href={registerUrl} target="_blank" rel="noopener noreferrer">Edit the application in {product} ↗</a>
    {/if}
  </div>
</div>

<style>
  .gitea-message { align-items: flex-start; }
  /* Focused only by script to read out a result; the alert itself marks it. */
  .gitea-message:focus { outline: none; }
  a.text-button { text-decoration: none; }
  a.text-button:hover { text-decoration: underline; }
  .gitea-message-body { display: flex; flex-wrap: wrap; gap: 4px 12px; align-items: baseline; min-width: 0; }
  .gitea-message-body p { margin: 0; overflow-wrap: anywhere; }
  .gitea-message-body .text-button { min-height: 24px; margin-left: 0; text-align: left; }
  @media (max-width: 640px) { .gitea-message-body .text-button { min-height: 44px; } }
</style>
