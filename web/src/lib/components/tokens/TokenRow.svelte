<script lang="ts">
  import { tokens } from '../../../app/tokens.svelte';
  import { workspace } from '../../../app/state/workspace.svelte';
  import type { Agent, ApiToken } from '../../types';
  import { accessLabel, effectiveProjectIds, exactTime, expiryLabel, projectLabels, tokenProjectLabel, tokenStatus, usedAgo } from '../../tokenMetadata';
  let { agent, token, now }: { agent: Agent; token: ApiToken; now: number } = $props();
  const status = $derived(tokenStatus(token, now));
  const restricted = $derived(token.project_ids?.length && effectiveProjectIds(agent, token).length !== token.project_ids.length);
</script>

<article class="credential-row" data-token-row={token.id}>
  <div class="credential-identity">
    <strong>{token.name}</strong>
    <small title={exactTime(token.created_at)}>Created {exactTime(token.created_at)} · <code>{token.id.slice(0, 8)}</code></small>
    <div class="credential-badges">
      <span class="credential-status" class:warning={status === 'Expiring soon'} class:expired={status === 'Expired'} data-token-status>{status}</span>
      {#if agent.disabled_at}<span class="credential-warning">Agent disabled</span>{/if}
      {#if agent.intake_source}<span class="credential-warning">Needs review</span>{/if}
    </div>
  </div>
  <div class="credential-access" data-token-access>
    <details><summary>{accessLabel(token)}</summary><ul>{#each token.scopes as scope}<li><code>{scope}</code></li>{/each}</ul></details>
    <div class="credential-projects" data-token-projects>{tokenProjectLabel(agent, token, workspace.projects)}</div>
    {#if restricted}<small class="credential-warning">Stored grant: {projectLabels(token.project_ids, workspace.projects)}. Effective access follows the current agent ceiling.</small>{/if}
  </div>
  <div class="credential-timing">
    <span data-token-last-used><small>Last used</small>{#if token.last_used_at}<time datetime={token.last_used_at} title={`${exactTime(token.last_used_at)} · usage is recorded about every five minutes`}>{usedAgo(token.last_used_at, now)}</time>{:else}Never{/if}</span>
    <span data-token-expiry title={token.expires_at ? exactTime(token.expires_at) : 'This token has no expiry'}>{expiryLabel(token, now)}</span>
  </div>
  <div class="credential-actions">
    {#if !agent.intake_source && !agent.disabled_at}<button class="text-button" type="button" aria-label={`Replace ${token.name}`} onclick={() => tokens.open(agent, token)}>Replace</button>{/if}
    <button class="text-button credential-revoke" type="button" aria-label={`Revoke ${token.name}`} disabled={Boolean(tokens.revokePending)} onclick={() => void tokens.revoke(agent, token)}>Revoke</button>
  </div>
</article>
