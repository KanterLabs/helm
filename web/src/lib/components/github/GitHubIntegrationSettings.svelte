<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api';
  import type { GithubIntegration, Project } from '../../types';
  import GitHubRepositorySelection from './GitHubRepositorySelection.svelte';
  import { relativeTime } from './pullRequests';

  /** Admin-only GitHub beta panel: sanitized diagnostics and repository selection. */
  export let projects: Project[] = [];
  export let defaultProjectId = '';

  const states: Record<string, { label: string; help: string }> = {
    not_configured: { label: 'Not configured', help: 'Helm has no GitHub token. An operator enables HELM_GITHUB_ENABLED and provides a read-only fine-grained token through HELM_GITHUB_TOKEN_FILE. Tokens are never entered or shown here.' },
    connected: { label: 'Connected', help: 'Helm can read the allowed repositories. Selected repositories are used for PR status and audits.' },
    permission_required: { label: 'Permission required', help: 'The token cannot read one or more allowed repositories. Grant read access to Contents, Pull requests, Checks, Commit statuses and Metadata, or ask an organization owner to approve the token.' },
    unavailable: { label: 'Unavailable', help: 'GitHub could not be reached. Other Helm work is unaffected; test again later.' },
    rate_limited: { label: 'Rate limited', help: 'GitHub is rate limiting this token. Helm backs off automatically; test again later.' }
  };

  let integration: GithubIntegration | null = null;
  let loading = false;
  let checking = false;
  let error = '';
  let projectId = '';

  $: if (!projectId && projects.length) projectId = projects.find((item) => item.id === defaultProjectId)?.id || projects[0].id;
  $: info = integration ? states[integration.state] || { label: integration.state.replace(/_/g, ' '), help: '' } : null;

  onMount(() => { void load(); });

  async function load(): Promise<void> {
    loading = true;
    error = '';
    try {
      integration = await api.getGithubIntegration();
    } catch (failure) {
      error = failure instanceof Error ? failure.message : 'GitHub status could not be loaded.';
    } finally {
      loading = false;
    }
  }

  async function check(): Promise<void> {
    if (checking) return;
    checking = true;
    error = '';
    try {
      integration = await api.checkGithubIntegration();
    } catch (failure) {
      error = failure instanceof Error ? failure.message : 'The connection test could not run.';
    } finally {
      checking = false;
    }
  }
</script>

<section class="settings-section github-settings" data-github-settings aria-labelledby="github-settings-heading">
  <div class="settings-section-heading">
    <div><span class="eyebrow">GitHub beta</span><h2 id="github-settings-heading">GitHub pull requests &amp; audits</h2><p>Read-only access for PR status on cards and server-run audits. GitHub mirrors the canonical repository; findings describe the captured GitHub commit only.</p></div>
    {#if info}<span class="github-state" data-state={integration?.state}>{info.label}</span>{/if}
  </div>
  {#if error}<div class="inline-alert error" role="alert"><span>!</span><span>{error}</span><button class="text-button" type="button" on:click={load}>Retry</button></div>{/if}
  {#if loading && !integration}
    <div class="list-skeleton"><div></div></div>
  {:else if integration && info}
    <div class="github-status">
      <p>{info.help}</p>
      {#if integration.message}<p class="github-message">{integration.message}</p>{/if}
      <p class="github-meta">Last checked {relativeTime(integration.checked_at)} · {integration.allowed_repositories.length} allowed {integration.allowed_repositories.length === 1 ? 'repository' : 'repositories'}</p>
      <button class="button quiet-button compact-button" type="button" disabled={checking || !integration.configured} on:click={() => void check()}>{#if checking}<span class="button-spinner"></span>{/if}Test connection</button>
    </div>
    {#if integration.configured && projects.length}
      <div class="github-project">
        <label>Project<select bind:value={projectId}>{#each projects as project (project.id)}<option value={project.id}>{project.name}</option>{/each}</select></label>
        {#if projectId}<GitHubRepositorySelection {projectId} allowedRepositories={integration.allowed_repositories} />{/if}
      </div>
    {/if}
  {/if}
</section>

<style>
  .github-settings { display: grid; gap: 14px; }
  .github-state { flex-shrink: 0; padding: 3px 10px; border: 1px solid var(--border-strong); border-radius: 999px; color: var(--ink-soft); font-size: var(--text-xs); font-weight: 600; }
  .github-state[data-state='connected'] { border-color: var(--green); color: var(--semantic-green); }
  .github-state[data-state='permission_required'], .github-state[data-state='rate_limited'] { border-color: var(--amber); color: var(--semantic-amber); }
  .github-state[data-state='unavailable'] { border-color: var(--red); color: var(--semantic-red); }
  .github-state[data-state='not_configured'] { border-style: dashed; }
  .github-status { display: grid; justify-items: start; gap: 6px; }
  .github-status p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; }
  .github-status .github-meta, .github-status .github-message { color: var(--muted); font-size: var(--text-xs); }
  .github-project { display: grid; gap: 12px; padding-top: 12px; border-top: 1px solid var(--border); }
  .github-project > label { display: grid; gap: 4px; max-width: 320px; }
</style>
