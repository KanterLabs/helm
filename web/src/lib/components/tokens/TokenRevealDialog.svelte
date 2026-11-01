<script lang="ts">
  import { tokens } from '../../../app/tokens.svelte';
  import { workspace } from '../../../app/state/workspace.svelte';
  import { dialogs } from '../../../app/state/dialogs.svelte';
  import { focusTrap } from '../../../app/dialogs';
  import { expiryLabel, tokenProjectLabel } from '../../tokenMetadata';
  let field = $state<HTMLInputElement>();
  const origin = window.location.origin;
</script>

{#if tokens.reveal && tokens.revealAgent && !dialogs.confirmRequest}
  <div class="modal-backdrop credential-backdrop" role="presentation"></div>
  <div class="modal credential-dialog credential-reveal" role="dialog" aria-modal="true" aria-label="Save your new token" use:focusTrap>
    <header class="credential-dialog-header"><span class="eyebrow">One-time secret</span><h2>Save your new token</h2><p>This value is shown once. Keep it in your secret manager or owner-only agent configuration.</p></header>
    <div class="credential-form-body">
      <div class="credential-review"><strong>{tokens.reveal.name}</strong><p>{tokens.revealAgent.name} · {tokenProjectLabel(tokens.revealAgent, tokens.reveal, workspace.projects)} · {expiryLabel(tokens.reveal)}</p></div>
      <label class="credential-label">Token<input bind:this={field} data-token-secret data-token-reveal-value aria-label="Token" data-dialog-initial-focus type="text" readonly value={tokens.reveal.token || ''} autocomplete="off" spellcheck="false" onfocus={(event) => event.currentTarget.select()} /></label>
      <div class="credential-copy-line"><button class="button primary" type="button" aria-label="Copy token" onclick={() => void tokens.copy(field)}>{tokens.copied ? 'Copied ✓' : 'Copy token'}</button><span role="status" aria-live="polite">{tokens.copied ? 'Copied. Save it before closing.' : ''}</span></div>
      {#if tokens.manualCopy}<p class="credential-warning" data-token-manual-copy>Select the token and press Ctrl+C or ⌘C to copy it manually.</p>{/if}
      <label class="check-label"><input type="checkbox" bind:checked={tokens.saved} /><span>I have saved this token somewhere safe</span></label>
      <div class="credential-setup" data-token-setup data-token-setup-guidance>
        <h3>Install where your agent runs</h3>
        <p>Confirm the authorized Helm origin: <code>{origin}</code>. Copying a token does not install or test it.</p>
        <details><summary>Helm skill</summary><p>Store <code>base_url</code> and <code>token</code> in <code>~/.config/helm/credentials.json</code> with owner-only permissions (<code>0600</code>). Use your secret manager to write the value.</p><pre>{JSON.stringify({ base_url: origin, token: '<your token>' }, null, 2)}</pre></details>
        <details><summary>Environment</summary><p>Set <code>HELM_URL</code> to the authorized origin and inject <code>HELM_TOKEN</code> from your secret manager or CI secrets. Do not paste the value into a command, ticket, chat or repository.</p></details>
        <details><summary>CI</summary><p>Store HELM_TOKEN as a protected repository or organization secret. Set HELM_URL to the authorized origin and inject both only into the agent job.</p></details>
        <p>After installation, verify the endpoint and identity with <code>python3 scripts/helm.py auth-check</code>. Use one token per installation.</p>
      </div>
      {#if tokens.refreshError}<div class="inline-alert warning" role="alert">{tokens.refreshError}</div>{/if}
      {#if tokens.replacement}<div class="credential-review"><h3>Replace {tokens.replacement.name}</h3><p>Both tokens still work. Update your client and verify the new token before retiring the old one.</p><button class="text-button credential-revoke" type="button" disabled={!tokens.saved && !tokens.copied || Boolean(tokens.revokePending)} onclick={() => void tokens.revokeOld()}>Revoke old token</button></div>{/if}
    </div>
    <footer class="modal-actions credential-dialog-footer"><button class="text-button" type="button" onclick={() => void tokens.closeReveal(true)}>Close without saving</button>{#if tokens.replacement}<button class="button quiet-button" type="button" disabled={!tokens.saved && !tokens.copied} onclick={() => void tokens.closeReveal()}>Keep both for now</button>{/if}<button class="button primary" type="button" disabled={!tokens.saved && !tokens.copied} onclick={() => void tokens.closeReveal()}>Done</button></footer>
  </div>
{/if}
