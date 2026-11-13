<script lang="ts">
  import { api } from '../../api';
  import type { GiteaRepository } from '../../types';
  import { errorCode, messageFor, type ProductLabel } from './giteaConnection';

  /** "Helm can read N repositories" with an on-demand list and a filter above ten. */
  export let count = 0;
  export let product: ProductLabel = 'Gitea';
  export let announce: (text: string) => void = () => undefined;

  let open = false;
  let loading = false;
  let list: GiteaRepository[] | null = null;
  let truncated = false;
  let failure = '';
  let query = '';

  $: noun = count === 1 ? 'repository' : 'repositories';
  $: needle = query.trim().toLowerCase();
  $: shown = (list || []).filter((item) => !needle || item.full_name.toLowerCase().includes(needle));

  async function load() {
    loading = true;
    failure = '';
    try {
      const result = await api.listGiteaRepositories();
      list = result.repositories;
      truncated = result.truncated;
      announce(`${list.length} ${list.length === 1 ? 'repository' : 'repositories'} listed.`);
    } catch (cause) {
      failure = messageFor(errorCode(cause).code, product).text;
      announce(failure);
    } finally {
      loading = false;
    }
  }

  function toggle() {
    open = !open;
    if (open && !list && !loading) void load();
  }
</script>

<div class="gitea-repos">
  <div class="gitea-repos-head">
    <p>Helm can read <strong>{count}</strong> {noun}</p>
    <button class="button quiet-button compact-button" type="button" aria-expanded={open} aria-controls="gitea-repo-list" on:click={toggle}>{open ? 'Hide' : 'View'}</button>
  </div>
  {#if open}
    <div id="gitea-repo-list" class="gitea-repos-body">
      {#if loading}
        <p class="optional" aria-busy="true">Loading repositories…</p>
      {:else if failure}
        <div class="inline-alert error"><span aria-hidden="true">!</span><span>{failure}</span><button class="text-button" type="button" on:click={load}>Try again</button></div>
      {:else if list}
        {#if list.length > 10}
          <input class="gitea-filter" type="search" aria-label="Filter repositories" placeholder="Filter repositories" autocomplete="off" spellcheck="false" bind:value={query} />
        {/if}
        {#if !list.length}
          <p class="optional">This account cannot read any repositories yet. Give it access on {product}, then look again.</p>
        {:else if !shown.length}
          <p class="optional">No repository matches “{query.trim()}”.</p>
        {:else}
          <ul data-gitea-repositories>
            {#each shown as repo (repo.full_name)}
              <li><span class="gitea-repo-name">{repo.full_name}</span>{#if repo.private}<span class="gitea-tag">Private</span>{/if}{#if repo.archived}<span class="gitea-tag">Archived</span>{/if}</li>
            {/each}
          </ul>
        {/if}
        {#if truncated}<p class="optional">Showing the first {list.length}. {product} has more.</p>{/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .gitea-repos { display: grid; gap: 10px; padding: 12px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-muted); }
  .gitea-repos-head { display: flex; flex-wrap: wrap; gap: 8px 12px; align-items: center; justify-content: space-between; }
  .gitea-repos-head p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); }
  .gitea-repos-body { display: grid; gap: 8px; }
  .gitea-filter { width: 100%; min-width: 0; height: 38px; padding: 0 12px; border: 1px solid var(--border-strong); border-radius: 8px; color: var(--ink); background: var(--surface); }
  ul { display: grid; max-height: 320px; margin: 0; padding: 0; overflow-y: auto; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); list-style: none; }
  li { display: flex; flex-wrap: wrap; gap: 4px 8px; align-items: center; padding: 8px 12px; font-size: var(--text-sm); }
  li + li { border-top: 1px solid var(--border); }
  .gitea-repo-name { min-width: 0; font-family: var(--font-mono); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .gitea-tag { padding: 1px 7px; border: 1px solid var(--border-strong); border-radius: 999px; color: var(--muted); font-size: var(--text-2xs); font-weight: 600; }
  .optional { margin: 0; color: var(--muted); font-size: var(--text-xs); }
  @media (max-width: 640px) {
    .gitea-repos-head .button { width: 100%; min-height: 44px; }
    .gitea-filter { height: 44px; }
  }
</style>
