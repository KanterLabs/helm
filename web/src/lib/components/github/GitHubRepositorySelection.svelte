<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type GithubRepositorySelection } from '../../types';

  /** Per-project repository selection from the server allowlist. No token is ever shown. */
  export let projectId: string;
  /** Pass the admin integration allowlist when the parent already loaded it. */
  export let allowedRepositories: string[] | null = null;
  export let onSaved: (repositories: GithubRepositorySelection[]) => void = () => undefined;

  const MAX = 10;
  let version = 0;
  let saved: GithubRepositorySelection[] = [];
  let allowed: string[] = [];
  let checked: Record<string, boolean> = {};
  let refs: Record<string, string[]> = {};
  let readOnly = false;
  let loading = false;
  let saving = false;
  let error = '';
  let status = '';
  let loadedKey = '';
  let request = 0;
  let saveRequest = 0;
  let destroyed = false;

  $: if (projectId && `${projectId}:${allowedRepositories?.join(',') ?? ''}` !== loadedKey) {
    loadedKey = `${projectId}:${allowedRepositories?.join(',') ?? ''}`;
    // The editor must never submit a previous project's selection or version.
    saveRequest += 1;
    saving = false;
    version = 0;
    saved = [];
    allowed = [];
    checked = {};
    refs = {};
    readOnly = false;
    status = '';
    void load();
  }
  $: selectedCount = draft(allowed, checked, refs).length;
  $: missingRef = allowed.filter((repo) => checked[repo] && branches(repo, refs).some((ref) => !ref.trim()));
  $: duplicateRef = allowed.filter((repo) => checked[repo] && new Set(branches(repo, refs).map((ref) => ref.trim())).size !== branches(repo, refs).length);
  $: retired = saved.filter((item) => !allowed.includes(item.repository));
  $: dirty = !readOnly && fingerprint(draft(allowed, checked, refs)) !== fingerprint(saved.filter((item) => allowed.includes(item.repository)));

  onDestroy(() => { destroyed = true; request += 1; saveRequest += 1; });

  function branches(repository: string, values = refs): string[] {
    return values[repository]?.length ? values[repository] : [''];
  }

  function fingerprint(items: GithubRepositorySelection[]): string {
    return JSON.stringify(items.map(({ repository, ref }) => [repository, ref]).sort(([repoA, refA], [repoB, refB]) => repoA.localeCompare(repoB) || refA.localeCompare(refB)));
  }

  function draft(repos = allowed, picked = checked, values = refs): GithubRepositorySelection[] {
    return repos.filter((repo) => picked[repo]).flatMap((repository) => branches(repository, values).map((ref) => ({ repository, ref: ref.trim() })));
  }

  function changeBranch(repository: string, index: number, value: string): void {
    refs = { ...refs, [repository]: branches(repository).map((ref, i) => i === index ? value : ref) };
  }

  function addBranch(repository: string): void {
    if (selectedCount >= MAX) return;
    refs = { ...refs, [repository]: [...branches(repository), ''] };
  }

  function removeBranch(repository: string, index: number): void {
    refs = { ...refs, [repository]: branches(repository).filter((_ref, i) => i !== index) };
  }

  function apply(selection: { version: number; repositories: GithubRepositorySelection[] }): void {
    version = selection.version;
    saved = selection.repositories || [];
    checked = Object.fromEntries(saved.map((item) => [item.repository, true]));
    const grouped: Record<string, string[]> = {};
    for (const item of saved) (grouped[item.repository] ||= []).push(item.ref || '');
    refs = grouped;
  }

  async function load(): Promise<void> {
    const requestId = ++request;
    const requestedProject = projectId;
    const current = () => !destroyed && requestId === request && projectId === requestedProject;
    loading = true;
    error = '';
    try {
      const [selection, list] = await Promise.all([
        api.getProjectGithub(requestedProject),
        allowedRepositories ? Promise.resolve(allowedRepositories) : api.getGithubIntegration().then(
          (integration) => integration.allowed_repositories || [],
          (failure) => {
            if (failure instanceof ApiError && failure.status === 403) return null;
            throw failure;
          }
        )
      ]);
      if (!current()) return;
      readOnly = list === null;
      allowed = list === null ? (selection.repositories || []).map((item) => item.repository) : list;
      apply(selection);
    } catch (failure) {
      if (current()) error = failure instanceof Error ? failure.message : 'Repository selection could not be loaded.';
    } finally {
      if (current()) loading = false;
    }
  }

  async function save(): Promise<void> {
    if (loading || saving || readOnly || missingRef.length || duplicateRef.length || selectedCount > MAX) return;
    const requestedProject = projectId;
    const requestId = ++saveRequest;
    // A project switch during the PUT must not apply this result to the new project.
    const stale = () => destroyed || requestId !== saveRequest || projectId !== requestedProject;
    saving = true;
    error = '';
    status = '';
    try {
      const result = await api.putProjectGithub(requestedProject, draft(), version);
      if (stale()) return;
      apply(result);
      status = 'Repositories saved. Cached GitHub details for removed repositories are hidden immediately.';
      onSaved(result.repositories);
    } catch (failure) {
      if (stale()) return;
      if (failure instanceof ApiError && (failure.status === 412 || failure.status === 409)) {
        error = 'The selection changed elsewhere. The latest selection is shown; review it and save again.';
        await load();
      } else {
        error = failure instanceof Error ? failure.message : 'Repositories could not be saved.';
      }
    } finally {
      if (!stale()) saving = false;
    }
  }
</script>

<div class="gh-repos" data-github-repositories aria-busy={loading}>
  {#if error}<p class="gh-repos-error" role="alert">{error}</p>{/if}
  {#if loading}
    <div class="list-skeleton" aria-label="Loading repositories"><div></div></div>
  {:else if readOnly}
    <p class="gh-repos-note">Only an admin can change repositories for this project.</p>
    {#if saved.length}<ul class="gh-repos-list">{#each saved as item}<li><span class="gh-mono">{item.repository}</span><span class="gh-repos-ref">{item.ref}</span></li>{/each}</ul>{:else}<p class="gh-repos-note">No repositories are selected yet.</p>{/if}
  {:else if !allowed.length}
    <p class="gh-repos-note">The server allowlist is empty. An operator sets HELM_GITHUB_REPOSITORIES in the runtime configuration.</p>
  {:else}
    <fieldset>
      <legend>Repositories audited for this project <span class="optional">{selectedCount} of {MAX} repository/branch pairs max</span></legend>
      <ul class="gh-repos-list">
        {#each allowed as repository (repository)}
          <li>
            <label class="check-label"><input type="checkbox" bind:checked={checked[repository]} disabled={saving || (!checked[repository] && selectedCount >= MAX)} /><span class="gh-mono">{repository}</span></label>
            {#if checked[repository]}
              <div class="gh-repos-branches">
                {#each branches(repository, refs) as ref, index}
                  <div class="gh-repos-branch">
                    <input class="gh-repos-ref-input" aria-label={`Branch for ${repository}${branches(repository, refs).length > 1 ? ` (${index + 1})` : ''}`} placeholder="Branch, e.g. main" required aria-invalid={!ref.trim() || duplicateRef.includes(repository) ? 'true' : undefined} value={ref} on:input={(event) => changeBranch(repository, index, event.currentTarget.value)} disabled={saving} />
                    {#if branches(repository, refs).length > 1}<button class="text-button" type="button" aria-label={`Remove branch ${index + 1} for ${repository}`} disabled={saving} on:click={() => removeBranch(repository, index)}>Remove</button>{/if}
                  </div>
                {/each}
                <button class="text-button" type="button" aria-label={`Add branch for ${repository}`} disabled={saving || selectedCount >= MAX} on:click={() => addBranch(repository)}>Add branch</button>
              </div>
            {/if}
          </li>
        {/each}
      </ul>
    </fieldset>
    {#if retired.length}<p class="gh-repos-note">No longer allowed by the server and removed on save: {retired.map((item) => item.repository).join(', ')}</p>{/if}
    {#if missingRef.length}<p class="gh-repos-error" role="status">Enter a branch for {missingRef.join(', ')}. Helm audits the exact branch you name; it never guesses one.</p>{/if}
    {#if duplicateRef.length}<p class="gh-repos-error" role="status">Each branch must be unique within {duplicateRef.join(', ')}.</p>{/if}
    <div class="gh-repos-actions">
      {#if status}<span class="gh-repos-status" role="status">{status}</span>{/if}
      <button class="button primary compact-button" type="button" disabled={loading || saving || selectedCount > MAX || missingRef.length > 0 || duplicateRef.length > 0 || (!dirty && !retired.length)} on:click={() => void save()}>{#if saving}<span class="button-spinner"></span>{/if}Save repositories</button>
    </div>
  {/if}
</div>

<style>
  .gh-repos { display: grid; gap: 10px; min-width: 0; }
  .gh-repos fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
  .gh-repos legend { margin-bottom: 8px; color: var(--ink); font-size: var(--text-sm); font-weight: 600; }
  .gh-repos-list { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .gh-repos-list li { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; min-height: 40px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  .gh-repos-branches { display: grid; gap: 6px; justify-items: start; min-width: 0; }
  .gh-repos-branch { display: flex; align-items: center; flex-wrap: wrap; gap: 6px; max-width: 100%; }
  .gh-mono { font-family: var(--font-mono); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .gh-repos-ref, .gh-repos-note, .gh-repos-status { color: var(--muted); font-size: var(--text-xs); }
  .gh-repos-note, .gh-repos-error { margin: 0; line-height: 1.45; }
  .gh-repos-error { color: var(--semantic-red); font-size: var(--text-xs); }
  .gh-repos-ref-input { width: min(180px, 100%); min-height: 32px; font-family: var(--font-mono); font-size: var(--text-xs); }
  .gh-repos-actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 10px; }
</style>
