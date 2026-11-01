<script lang="ts">
  import { tokens as tokenActions } from '../../../app/tokens.svelte';
  import { settings } from '../../../app/state/settings.svelte';
  import { workspace } from '../../../app/state/workspace.svelte';
  import { createAgent, loadAgents } from '../../../app/settings';
  import { onMount } from 'svelte';
  import type { Agent, ApiToken } from '../../types';
  import { effectiveProjectIds, projectLabels, sortTokens, tokenStatus } from '../../tokenMetadata';
  import TokenRow from './TokenRow.svelte';
  let query = $state('');
  let status = $state('all');
  let project = $state('all');
  let now = $state(Date.now());
  const automation = $derived(settings.agents.filter((agent) => !agent.intake_source));
  const sources = $derived(settings.agents.filter((agent) => agent.intake_source));
  const count = $derived(automation.reduce((total, agent) => total + (agent.tokens?.length || 0), 0));
  const reviews = $derived(sources.reduce((total, agent) => total + (agent.tokens?.length || 0), 0));
  function matches(agent: Agent, token: ApiToken) {
    const text = `${agent.name} ${token.name}`.toLowerCase();
    const ids = effectiveProjectIds(agent, token);
    return text.includes(query.trim().toLowerCase())
      && (status === 'all' || status === 'never-used' && !token.last_used_at || tokenStatus(token, now).toLowerCase().replaceAll(' ', '-') === status)
      && (project === 'all' || !ids.length && !agent.project_ids?.length && !token.project_ids?.length || ids.includes(project));
  }
  const groups = $derived(automation.map((agent) => ({ agent, tokens: sortTokens(agent.tokens || [], now).filter((token) => matches(agent, token)) }))
    .filter(({ agent, tokens }) => tokens.length || !agent.tokens?.length && status === 'all' && project === 'all' && agent.name.toLowerCase().includes(query.trim().toLowerCase())));
  const filtering = $derived(Boolean(query || status !== 'all' || project !== 'all'));
  function clearFilters() { query = ''; status = 'all'; project = 'all'; }
  onMount(() => { const timer = window.setInterval(() => now = Date.now(), 30_000); return () => window.clearInterval(timer); });
</script>

<section class="settings-section credential-inventory" aria-labelledby="agent-tokens-heading">
  <div class="settings-section-heading">
    <div><span class="eyebrow">Coordination</span><h2 id="agent-tokens-heading">Agents &amp; tokens</h2><p>Manage where your agents can work, and keep each installation easy to identify.</p></div>
    <button class="button primary" type="button" onclick={() => settings.showAgentForm = !settings.showAgentForm}>＋ Add agent</button>
  </div>
  {#if settings.showAgentForm}
    <form class="settings-form" onsubmit={(event) => { event.preventDefault(); void createAgent(); }}>
      <label>Agent name<input bind:value={settings.agentNameDraft} placeholder="Release assistant" maxlength="200" required /></label>
      <label>Description <span class="optional">Optional</span><textarea rows="2" bind:value={settings.agentDescriptionDraft} placeholder="What will this agent work on?"></textarea></label>
      <div class="form-actions"><button class="text-button" type="button" onclick={() => settings.showAgentForm = false}>Cancel</button><button class="button primary" type="submit" disabled={settings.agentSaving || !settings.agentNameDraft.trim()}>Create agent</button></div>
    </form>
  {/if}
  <div class="credential-toolbar">
    <label class="credential-search"><span class="sr-only">Search agents and tokens</span><input type="search" aria-label="Search agents and tokens" bind:value={query} placeholder="Search agents or tokens…" /></label>
    <label><span class="sr-only">Token status</span><select aria-label="Token status" bind:value={status}><option value="all">All current tokens</option><option value="active">Active</option><option value="expiring-soon">Expiring soon</option><option value="expired">Expired</option><option value="never-used">Never used</option></select></label>
    <label><span class="sr-only">Token project</span><select aria-label="Token project" bind:value={project}><option value="all">All projects</option>{#each workspace.projects as item}<option value={item.id}>{item.name}</option>{/each}</select></label>
  </div>
  <div class="credential-summary"><span data-token-summary>{count} {count === 1 ? 'token' : 'tokens'} · {automation.length} {automation.length === 1 ? 'agent' : 'agents'}</span><details><summary>About token access</summary><p>Tokens are shown only when generated. Permissions and project restrictions combine with each agent’s ceiling. Last use is recorded about every five minutes.</p></details></div>
  {#if settings.agentsError}<div class="inline-alert error" role="alert">{settings.agentsError}<button class="text-button" type="button" onclick={() => void loadAgents()}>Refresh inventory</button></div>{/if}
  {#if settings.agentsLoading && !settings.agents.length}<p role="status">Loading agent tokens…</p>
  {:else if !groups.length}<div class="empty-state compact-empty"><h3>{filtering ? 'No tokens match these filters' : 'Connect your first agent'}</h3><p>{filtering ? 'Try a different name, status or project.' : 'Create an agent identity, then generate a token for each installation.'}</p>{#if filtering}<button class="button quiet-button" type="button" onclick={clearFilters}>Clear filters</button>{/if}</div>
  {:else}
    <div class="credential-groups">{#each groups as { agent, tokens } (agent.id)}
      <article class="credential-group" data-agent-group={agent.id} data-agent-id={agent.id}>
        <header class="credential-agent"><span class="agent-avatar" aria-hidden="true">✦</span><div><h3>{agent.name}</h3><p>{agent.description || 'Automation identity'} · {agent.project_ids?.length ? projectLabels(agent.project_ids, workspace.projects) : 'All projects'}</p>{#if agent.disabled_at}<small class="credential-warning">Disabled</small>{/if}</div>{#if !agent.disabled_at}<button class="button quiet-button compact-button" data-token-trigger type="button" aria-label={`New token for ${agent.name}`} onclick={() => tokenActions.open(agent)}>＋ New token</button>{/if}</header>
        {#each tokens as token (token.id)}<TokenRow {agent} {token} {now} />{/each}
        {#if !agent.tokens?.length}<p class="credential-empty">No tokens yet</p>{/if}
        {#if (agent.tokens?.length || 0) > tokens.length}<p class="credential-empty" data-token-hidden-count>{(agent.tokens?.length || 0) - tokens.length} more tokens hidden by filters</p>{/if}
      </article>
    {/each}</div>
  {/if}
  {#if sources.length}<details class="credential-sources" data-alert-sources open={reviews > 0}>
    <summary>Alert sources ({sources.length}) {#if reviews}<span class="credential-warning">Review {reviews} existing credentials</span>{/if}</summary>
    <p>Sources record incoming alerts under their own identity. New agent tokens cannot be generated for them.</p>
    <a href="/tickets?connect=1">Manage integrations</a>
    {#each sources as agent (agent.id)}<article class="credential-group" data-alert-source={agent.id}><header class="credential-agent"><div><h3>{agent.name}</h3><p>Passive alert attribution</p></div></header>{#each agent.tokens || [] as token (token.id)}<TokenRow {agent} {token} {now} />{/each}</article>{/each}
  </details>{/if}
</section>
