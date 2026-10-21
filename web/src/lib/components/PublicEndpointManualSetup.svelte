<script lang="ts">
  import type { PublicEndpointView } from '../types';

  // Hand-run alternative to Connect Cloudflare: the parent owns the API call
  // and reports success so this form can clear or suggest a fix.
  export let view: PublicEndpointView;
  export let saving = false;
  export let exposedPaths: (pattern: string) => string[];
  export let onCreate: (hostname: string, token: string) => Promise<boolean>;
  export let onClearError: () => void = () => undefined;

  let hostname = '';
  let token = '';
  let hostnameSuggestion = '';

  async function create() {
    if (!hostname.trim() || !token.trim() || saving) return;
    const submitted = hostname.trim();
    const created = await onCreate(submitted, token.trim());
    token = '';
    if (created) {
      hostname = '';
      return;
    }
    // A bare domain is the most common mistake: offer the subdomain.
    hostnameSuggestion = /^[a-z0-9-]+\.[a-z0-9-]+$/i.test(submitted) ? `hooks.${submitted.toLowerCase()}` : '';
  }
</script>

<details class="manual-setup" data-manual-public>
  <summary>Set up manually</summary>
  <ol class="manual-steps">
    <li>Create a Cloudflare API token with the permissions below (the <strong>Create token on Cloudflare</strong> button in Connect Cloudflare pre-selects them).</li>
    <li>Enter a <strong>new subdomain</strong> such as <code>hooks.example.com</code>, not the domain itself: Helm creates a DNS record for it.</li>
    <li>Paste the token and select <strong>Create public URL</strong>. The token is used once and never stored.</li>
  </ol>
  <form class="public-form" on:submit|preventDefault={create}>
    <label>Hostname<input aria-label="Public hostname" bind:value={hostname} placeholder="hooks.example.com" autocomplete="off" required /></label>
    <label>Cloudflare API token<input aria-label="Cloudflare API token" type="password" bind:value={token} autocomplete="off" required /></label>
    <button class="button primary" type="submit" disabled={!hostname.trim() || !token.trim() || saving}>{saving ? 'Creating tunnel…' : 'Create public URL'}</button>
  </form>
  {#if hostnameSuggestion}<button class="text-button" type="button" data-hostname-suggestion on:click={() => { hostname = hostnameSuggestion; hostnameSuggestion = ''; onClearError(); }}>Use {hostnameSuggestion} instead</button>{/if}
  <details>
    <summary>Token permissions</summary>
    <ul>{#each view.required_permissions as permission}<li>{permission}</li>{/each}</ul>
    <p class="optional">Exposed paths: {exposedPaths(view.exposed_paths).join(' and ')}. The server needs outbound access to Cloudflare (port 7844) and the <code>cloudflared</code> binary.</p>
  </details>
</details>

<style>
  .manual-setup { display: grid; gap: 8px; }
  .manual-setup[open] > summary { margin-bottom: 8px; }
  .manual-steps { margin: 0 0 8px; padding-left: 20px; font-size: var(--text-sm); line-height: 1.5; }
  .manual-setup p { margin: 0; font-size: var(--text-sm); line-height: 1.5; }
  .public-form { display: grid; grid-template-columns: 1fr 1fr auto; gap: 10px; align-items: end; }
  .public-form label { display: grid; gap: 5px; font-size: var(--text-xs); font-weight: 600; }
  .optional { color: var(--muted); font-size: var(--text-xs); font-weight: 400; }
  details { font-size: var(--text-xs); }
  details summary { cursor: pointer; font-weight: 600; }
  details ul { margin: 6px 0; padding-left: 18px; }
  @media (max-width: 760px) {
    .public-form { grid-template-columns: minmax(0, 1fr); }
  }
</style>
