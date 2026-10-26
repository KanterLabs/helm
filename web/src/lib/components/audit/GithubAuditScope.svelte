<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../../api';
  import { key } from '../../api/client';
  import type { GithubReadiness, GithubRepositorySelection, Project, SemanticState } from '../../types';
  import { stateLabels } from './helpers';
  import { conflictAuditId } from './githubAudit';

  /** Run audit scope: what will be read, and whether GitHub and AI are both ready. */
  export let project: Project;
  export let onStarted: (auditId: string) => void = () => undefined;
  export let onOpenExisting: (auditId: string) => void = () => undefined;
  export let onClose: () => void = () => undefined;

  const scopeStates: SemanticState[] = ['backlog', 'ready', 'active', 'blocked'];
  let readiness: GithubReadiness | null = null;
  let loading = false;
  let loadError = '';
  let startError = '';
  let conflictId = '';
  let starting = false;
  let included: Record<string, boolean> = {};
  let states: Record<string, boolean> = { backlog: true, ready: true, active: true, blocked: false };
  let requestKey = '';
  let requestFingerprint = '';
  let loadedProjectId = '';
  let loadRequest = 0;
  let startRequest = 0;
  let destroyed = false;
  let heading: HTMLElement;
  let section: HTMLElement;

  $: repositories = readiness?.repositories || [];
  $: chosen = repositories.filter((item) => included[pairId(item)]);
  $: chosenStates = scopeStates.filter((state) => states[state]);
  $: canStart = Boolean(readiness?.ready) && chosen.length > 0 && chosenStates.length > 0 && !starting && !loading;
  $: if (project.id !== loadedProjectId) {
    loadedProjectId = project.id;
    startRequest += 1;
    readiness = null;
    included = {};
    states = { backlog: true, ready: true, active: true, blocked: false };
    starting = false;
    startError = '';
    conflictId = '';
    requestKey = '';
    requestFingerprint = '';
    void load(project.id);
  }

  onMount(() => {
    void tick().then(() => heading?.focus());
  });
  onDestroy(() => { destroyed = true; loadRequest += 1; startRequest += 1; });

  const pairId = (item: GithubRepositorySelection) => `${item.repository}@${item.ref}`;

  async function load(projectId = project.id): Promise<void> {
    const requestId = ++loadRequest;
    const current = () => !destroyed && project.id === projectId && requestId === loadRequest;
    loading = true;
    loadError = '';
    try {
      const result = await api.getGithubReadiness(projectId);
      if (!current()) return;
      readiness = result;
      included = Object.fromEntries((readiness.repositories || []).map((item) => [pairId(item), true]));
    } catch (failure) {
      if (current()) loadError = failure instanceof Error ? failure.message : 'Audit readiness could not be checked.';
    } finally {
      if (current()) loading = false;
    }
  }

  async function start(): Promise<void> {
    if (starting || !canStart) return;
    const projectId = project.id;
    const requestId = ++startRequest;
    const current = () => !destroyed && project.id === projectId && requestId === startRequest;
    // States are the scope; task_ids is the alternative and is never sent alongside.
    const body = { repositories: chosen.map(({ repository, ref }) => ({ repository, ref })), states: chosenStates };
    const fingerprint = JSON.stringify({ projectId, ...body });
    // Replays and double clicks of the same scope reuse one idempotency key.
    if (fingerprint !== requestFingerprint) {
      requestFingerprint = fingerprint;
      requestKey = key();
    }
    starting = true;
    startError = '';
    conflictId = '';
    try {
      const result = await api.startGithubAudit(projectId, body, requestKey);
      if (!current()) return;
      onStarted(result.audit?.id || result.job.audit_id);
    } catch (failure) {
      if (!current()) return;
      conflictId = conflictAuditId(failure);
      startError = failure instanceof Error ? failure.message : 'The audit could not be started.';
      starting = false;
    }
  }

  function keydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && !starting && section?.contains(document.activeElement)) {
      event.stopPropagation();
      onClose();
    }
  }
</script>

<svelte:window on:keydown={keydown} />

<section class="gh-scope" data-github-audit-scope aria-labelledby="gh-scope-heading" bind:this={section}>
  <div class="gh-scope-top">
    <div><span class="eyebrow">Run audit</span><h2 id="gh-scope-heading" tabindex="-1" bind:this={heading}>Check tasks against GitHub</h2><p>Helm reads the selected branches and linked PRs on the server, then analyzes them with your own AI connection. Findings wait for your review; nothing moves automatically.</p></div>
  </div>
  {#if loadError}
    <div class="inline-alert error" role="alert"><span>!</span><span>{loadError}</span><button class="text-button" type="button" on:click={() => void load()}>Retry</button></div>
  {:else if loading && !readiness}
    <div class="list-skeleton" aria-label="Checking readiness"><div></div><div></div></div>
  {:else if readiness}
    <div class="gh-scope-grid">
      <fieldset>
        <legend>Repositories</legend>
        {#if repositories.length}
          {#each repositories as item (pairId(item))}
            <label class="check-label"><input type="checkbox" bind:checked={included[pairId(item)]} disabled={starting} /><span><span class="gh-mono">{item.repository}</span> <span class="gh-ref">@ {item.ref}</span></span></label>
          {/each}
          <p class="gh-scope-hint">PRs linked to in-scope tasks are included. A branch is audited even when no PR is linked.</p>
        {:else}
          <p class="gh-scope-hint">No repositories are selected for {project.name}. Use GitHub repositories to choose some.</p>
        {/if}
      </fieldset>
      <fieldset>
        <legend>Tasks</legend>
        {#each scopeStates as state}
          <label class="check-label"><input type="checkbox" bind:checked={states[state]} disabled={starting} /><span>{stateLabels[state]}</span></label>
        {/each}
        <p class="gh-scope-hint">Every matching task is captured up to the server limit, not only the loaded board page. The run reports its measured count.</p>
      </fieldset>
      <div class="gh-scope-ready" aria-label="Readiness">
        <p data-ready={readiness.github.state === 'connected'}><strong>GitHub</strong> <span>{readiness.github.message || readiness.github.state}</span></p>
        <p data-ready={readiness.ai.state === 'connected'}><strong>Your AI</strong> <span>{readiness.ai.message || readiness.ai.state}</span>{#if readiness.ai.state !== 'connected'} <a href="/settings">Open Settings</a>{/if}</p>
      </div>
    </div>
    {#if startError}
      <div class="inline-alert error" role="alert"><span>!</span><span>{startError}</span>{#if conflictId}<button class="text-button" type="button" on:click={() => onOpenExisting(conflictId)}>Open running audit</button>{/if}</div>
    {/if}
    <div class="gh-scope-actions">
      <button class="text-button" type="button" disabled={starting} on:click={onClose}>Cancel</button>
      <button class="button primary" type="button" disabled={!canStart} on:click={() => void start()}>{#if starting}<span class="button-spinner"></span>{/if}Start audit</button>
    </div>
  {/if}
</section>

<style>
  .gh-scope { display: grid; gap: 14px; margin-bottom: 18px; padding: 18px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); box-shadow: var(--shadow-sm); }
  .gh-scope h2 { margin: 2px 0 4px; font-size: var(--text-lg); }
  .gh-scope h2:focus-visible { outline: 2px solid var(--purple); outline-offset: 3px; border-radius: 4px; }
  .gh-scope p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; }
  .gh-scope-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 16px; }
  .gh-scope fieldset { display: grid; align-content: start; gap: 6px; min-width: 0; margin: 0; padding: 0; border: 0; }
  .gh-scope legend { margin-bottom: 6px; color: var(--ink); font-size: var(--text-sm); font-weight: 600; }
  .gh-mono { font-family: var(--font-mono); font-size: var(--text-xs); overflow-wrap: anywhere; }
  .gh-ref { color: var(--muted); font-family: var(--font-mono); font-size: var(--text-xs); }
  .gh-scope .gh-scope-hint { color: var(--muted); font-size: var(--text-xs); }
  .gh-scope-ready { display: grid; align-content: start; gap: 8px; }
  .gh-scope-ready p { padding: 8px 10px; border-left: 3px solid var(--red); border-radius: 6px; background: var(--surface-muted); font-size: var(--text-xs); }
  .gh-scope-ready p[data-ready='true'] { border-left-color: var(--green); }
  .gh-scope-ready a { color: var(--purple); }
  .gh-scope-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 10px; }
</style>
