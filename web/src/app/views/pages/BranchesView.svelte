<svelte:options runes={true} />

<script lang="ts">
  // The per-project branch graph page: heading, states and repository choice.
  import BranchGraph from '../../../lib/components/BranchGraph.svelte';
  import { loadBranches, selectBranchRepository, toggleBranchBucket } from '../../branches';
  import { setView } from '../../routing';
  import { branches } from '../../state/branches.svelte';
  import { session } from '../../state/session.svelte';
  import { workspace } from '../../state/workspace.svelte';

  const report = $derived(branches.report);
  // The server clock decides what is stale, so a skewed browser clock cannot.
  const now = $derived(report?.fetched_at ? Date.parse(report.fetched_at) : Date.now());
  const pending = $derived(report ? report.branches.filter((branch) => !branch.compared).length : 0);
</script>

{#if workspace.activeProject}
  <section class="page-heading">
    <div>
      <h1>Branches</h1>
      <p>{#if report?.state === 'ready' && report.base}Every branch of <code>{report.repository}</code> measured against <code>{report.base.name}</code> on GitHub.{:else}How each branch of this project's GitHub repository compares with its main branch.{/if}</p>
    </div>
    <div class="heading-actions">
      {#if report && report.repositories.length > 1}
        <select aria-label="Repository" value={report.repository} on:change={(event) => selectBranchRepository(event.currentTarget.value)}>
          {#each report.repositories as repository (repository)}<option value={repository}>{repository}</option>{/each}
        </select>
      {/if}
      <button class="button quiet-button" type="button" disabled={branches.loading} on:click={() => loadBranches()}>↻ Refresh</button>
    </div>
  </section>

  {#if branches.error}
    <div class="inline-alert error" role="alert"><span>!</span><span>{branches.error}</span><button class="text-button" type="button" on:click={() => loadBranches()}>Retry</button></div>
  {:else if !report}
    <div class="list-skeleton" role="status" aria-label="Loading branches">{#each [1, 2, 3] as item (item)}<div></div>{/each}</div>
  {:else if report.state === 'no_repository'}
    <div class="empty-state" data-branch-state={report.state}>
      <div class="empty-icon">◌</div>
      <h2>No repository selected</h2>
      <p>Select a GitHub repository for {workspace.activeProject.name} to see how its branches compare.</p>
      {#if session.workspaceAdmin}<button class="button primary" type="button" on:click={() => setView('settings')}>Open settings</button>{/if}
    </div>
  {:else if report.state !== 'ready'}
    <div class="inline-alert warning" role="alert" data-branch-state={report.state}><span>!</span><span>{report.message}</span><button class="text-button" type="button" on:click={() => loadBranches()}>Retry</button></div>
  {:else}
    {#if report.partial}
      <div class="inline-alert info branch-partial" role="status" data-branch-partial>
        <span>i</span>
        <span>{pending ? `${pending} ${pending === 1 ? 'branch has' : 'branches have'} not been compared yet.` : 'GitHub has more branches than Helm reads at once, so this list is incomplete.'}</span>
        {#if pending}<button class="text-button" type="button" on:click={() => loadBranches()}>Continue</button>{/if}
      </div>
    {/if}
    <BranchGraph {report} show={branches.show} {now} onToggle={toggleBranchBucket} />
  {/if}
{:else}
  <div class="empty-state welcome-state"><div class="welcome-orbit"><span>B</span></div><span class="eyebrow">Choose a project</span><h1>Branches live with a project.</h1><p>Select a project first, then see how its repository's branches compare.</p><button class="button primary button-large" type="button" on:click={() => setView('board')}>Browse projects</button></div>
{/if}

<style>
  .page-heading code { font: var(--text-xs) var(--font-mono); color: var(--ink-soft); }
  .heading-actions select { min-height: 36px; max-width: 260px; padding: 0 10px; border: 1px solid var(--border); border-radius: 9px; color: var(--ink-soft); background: var(--surface); font-size: var(--text-xs); }
  .branch-partial { margin-bottom: 12px; }
</style>
