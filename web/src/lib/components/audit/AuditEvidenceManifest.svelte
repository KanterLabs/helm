<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type GithubAuditEvidence } from '../../types';
  import { safeGithubUrl } from '../github/pullRequests';

  /** Immutable capture manifest: exact pinned commits, coverage and omissions. */
  export let auditId: string;
  /** Load only for job-backed runs once capture has happened. */
  export let enabled = false;
  /** Change to reload, for example when the job finishes. */
  export let reloadKey = '';
  export let onEvidence: (evidence: GithubAuditEvidence | null, error: string) => void = () => undefined;
  export let onUnavailable: (message: string) => void = () => undefined;

  let evidence: GithubAuditEvidence | null = null;
  let error = '';
  let loading = false;
  let loadedKey = '';
  let request = 0;

  $: key = enabled && auditId ? `${auditId}:${reloadKey}` : '';
  $: if (key !== loadedKey) {
    loadedKey = key;
    if (key) void load(auditId);
    else reset();
  }

  function reset(): void {
    request += 1;
    evidence = null;
    error = '';
    onEvidence(null, '');
  }

  async function load(id: string): Promise<void> {
    const requestId = ++request;
    loading = true;
    evidence = null;
    onEvidence(null, '');
    try {
      const result = await api.getAuditEvidence(id);
      if (requestId !== request) return;
      evidence = result;
      error = '';
    } catch (failure) {
      if (requestId !== request) return;
      // Revoked access is a typed error: retained private content is never shown.
      evidence = null;
      error = failure instanceof ApiError && failure.status === 404
        ? 'Evidence appears here once Helm has read GitHub for this run.'
        : failure instanceof Error ? failure.message : 'Evidence could not be loaded.';
      if (!(failure instanceof ApiError && failure.status === 404)) onUnavailable(error);
    } finally {
      if (requestId === request) loading = false;
      if (requestId === request) onEvidence(evidence, error);
    }
  }

  onDestroy(() => { request += 1; });

  function commitUrl(repository: string, sha: string): string {
    return /^[\w.-]+\/[\w.-]+$/.test(repository) && /^[0-9a-f]{7,64}$/i.test(sha)
      ? safeGithubUrl(`https://github.com/${repository}/commit/${sha}`)
      : '';
  }
</script>

{#if enabled}
  <section class="gh-manifest" data-audit-evidence-manifest aria-labelledby="gh-manifest-heading" aria-busy={loading}>
    <h2 id="gh-manifest-heading">Captured evidence</h2>
    {#if evidence}
      <p class="gh-manifest-meta">Captured {new Date(evidence.captured_at).toLocaleString()} · {evidence.items.length} cited {evidence.items.length === 1 ? 'item' : 'items'}{#if evidence.partial}{' · '}<strong>Partial capture</strong>{/if}</p>
      <dl class="gh-manifest-pins">
        {#each evidence.repositories as repository}
          <div>
            <dt>{repository.repository} <span>@ {repository.ref || 'unknown ref'}</span></dt>
            <dd>{#if commitUrl(repository.repository, repository.sha)}<a href={commitUrl(repository.repository, repository.sha)} target="_blank" rel="noreferrer">{repository.sha}</a>{:else}{repository.sha}{/if}</dd>
          </div>
        {/each}
      </dl>
      {#if evidence.omissions.length}
        <details class="gh-manifest-omissions" open={evidence.omissions.length <= 3}>
          <summary>{evidence.omissions.length} {evidence.omissions.length === 1 ? 'omission' : 'omissions'}</summary>
          <ul>{#each evidence.omissions as omission}<li>{omission}</li>{/each}</ul>
        </details>
      {/if}
    {:else if error}
      <p class="gh-manifest-error" role="status">{error}</p>
    {:else if loading}
      <div class="list-skeleton" aria-label="Loading evidence"><div></div></div>
    {/if}
  </section>
{/if}

<style>
  .gh-manifest { display: grid; gap: 8px; margin-bottom: 16px; padding: 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); }
  .gh-manifest h2 { margin: 0; font-size: var(--text-md); }
  .gh-manifest-meta, .gh-manifest-error { margin: 0; color: var(--muted); font-size: var(--text-xs); }
  .gh-manifest-pins { display: grid; gap: 6px; margin: 0; }
  .gh-manifest-pins div { display: grid; gap: 2px; min-width: 0; }
  .gh-manifest-pins dt { color: var(--ink); font: 500 var(--text-xs) var(--font-mono); }
  .gh-manifest-pins dt span { color: var(--muted); }
  .gh-manifest-pins dd { margin: 0; color: var(--ink-soft); font: var(--text-xs) var(--font-mono); overflow-wrap: anywhere; }
  .gh-manifest-pins a { color: var(--purple); }
  .gh-manifest-omissions { font-size: var(--text-xs); }
  .gh-manifest-omissions summary { color: var(--muted); cursor: pointer; }
  .gh-manifest-omissions ul { margin: 6px 0 0; padding-left: 18px; color: var(--ink-soft); }
</style>
