<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../../../api';
  import { key } from '../../../api/client';
  import { AUDIT_SCOPE_LIMIT, type ScopeCount } from '../../../api/columnAudit';
  import type { Column, GithubAuditStartInput, GithubReadiness, Project } from '../../../types';
  import { conflictAuditId } from '../githubAudit';
  import { readIntent, writeIntent, type ScopeIntent } from './scopeIntent';

  /** Audit columns: baseline, server-counted scope and readiness, then one start. */
  export let project: Project;
  export let columns: Column[] = [];
  export let onStarted: (auditId: string) => void = () => undefined;
  export let onOpenExisting: (auditId: string) => void = () => undefined;
  export let onEditRepositories: () => void = () => undefined;
  export let onClose: () => void = () => undefined;

  let readiness: GithubReadiness | null = null;
  let count: ScopeCount | null = null;
  let intent: ScopeIntent = readIntent(project.id);
  let loading = false;
  let loadError = '';
  let startError = '';
  let conflictId = '';
  let starting = false;
  let requestKey = '';
  let requestFingerprint = '';
  let loadedProjectId = '';
  let loadRequest = 0;
  let startRequest = 0;
  let destroyed = false;
  let heading: HTMLElement;
  let section: HTMLElement;

  $: if (project.id !== loadedProjectId) {
    loadedProjectId = project.id;
    intent = readIntent(project.id);
    readiness = null;
    count = null;
    // A start still pending for the previous project can no longer affect this one.
    startRequest += 1;
    starting = false;
    requestKey = '';
    requestFingerprint = '';
    startError = '';
    conflictId = '';
    void load();
  }
  $: writeIntent(project.id, { ...intent, open: false });
  $: activeColumns = columns.filter((column) => !column.archived_at);
  $: cards = count?.cards || [];
  $: byColumn = cards.reduce<Record<string, number>>((all, card) => ({ ...all, [card.column_id]: (all[card.column_id] || 0) + 1 }), {});
  $: stateCount = (state: string) => activeColumns.filter((column) => column.semantic_state === state).reduce((sum, column) => sum + (byColumn[column.id] || 0), 0);
  $: known = new Set(cards.map((card) => card.id));
  $: selectedIds = intent.taskIds.filter((id) => known.has(id));
  $: scopedIds = intent.mode === 'all'
    ? cards.map((card) => card.id)
    : intent.mode === 'columns'
      ? cards.filter((card) => intent.columnIds.includes(card.column_id)).map((card) => card.id)
      : selectedIds;
  $: repositories = readiness?.repositories || [];
  $: prChoice = repositories.map((repo) => ({ repo, choice: intent.baselines[repo.repository] })).find((entry) => entry.choice?.kind === 'pr');
  $: prNumber = prChoice ? Number(prChoice.choice.pullRequest) : 0;
  $: prInvalid = Boolean(prChoice) && !(Number.isInteger(prNumber) && prNumber > 0);
  $: overLimit = scopedIds.length > AUDIT_SCOPE_LIMIT || (intent.mode === 'all' && Boolean(count?.overLimit));
  $: canStart = Boolean(readiness?.ready) && repositories.length > 0 && scopedIds.length > 0 && !overLimit && !prInvalid && !starting && !loading;

  onMount(() => { void tick().then(() => heading?.focus()); });
  onDestroy(() => { destroyed = true; loadRequest += 1; startRequest += 1; });

  async function load(): Promise<void> {
    const projectId = project.id;
    const requestId = ++loadRequest;
    const current = () => !destroyed && project.id === projectId && requestId === loadRequest;
    loading = true;
    loadError = '';
    try {
      const [nextReadiness, nextCount] = await Promise.all([api.getGithubReadiness(projectId), api.countAuditScope(projectId)]);
      if (!current()) return;
      readiness = nextReadiness;
      count = nextCount;
    } catch (failure) {
      if (current()) loadError = failure instanceof Error ? failure.message : 'Audit readiness could not be checked.';
    } finally {
      if (current()) loading = false;
    }
  }

  function setBaseline(repository: string, kind: 'saved' | 'pr'): void {
    const baselines: ScopeIntent['baselines'] = {};
    // One explicit PR head per run; every other repository keeps its saved ref.
    for (const repo of repositories) baselines[repo.repository] = { kind: 'saved', pullRequest: intent.baselines[repo.repository]?.pullRequest || '' };
    baselines[repository] = { kind, pullRequest: intent.baselines[repository]?.pullRequest || '' };
    intent = { ...intent, baselines };
  }

  function setPullRequest(repository: string, value: string): void {
    intent = { ...intent, baselines: { ...intent.baselines, [repository]: { kind: 'pr', pullRequest: value.trim() } } };
  }

  function toggleColumn(id: string, checked: boolean): void {
    intent = { ...intent, columnIds: checked ? [...new Set([...intent.columnIds, id])] : intent.columnIds.filter((item) => item !== id) };
  }

  async function start(): Promise<void> {
    if (!canStart) return;
    const projectId = project.id;
    const requestId = ++startRequest;
    const current = () => !destroyed && project.id === projectId && requestId === startRequest;
    const body: GithubAuditStartInput = intent.mode === 'all' ? {} : { task_ids: scopedIds };
    if (prChoice) body.baseline = { repository: prChoice.repo.repository, pull_request: prNumber };
    const fingerprint = JSON.stringify({ projectId, body });
    // Double clicks and replays of the same scope reuse one idempotency key.
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

  const cardWord = (n: number) => `${n} ${n === 1 ? 'card' : 'cards'}`;
</script>

<svelte:window on:keydown={keydown} />

<section class="ca-scope" data-column-audit-scope aria-labelledby="ca-scope-heading" bind:this={section}>
  <div><span class="eyebrow">Column audit</span><h2 id="ca-scope-heading" tabindex="-1" bind:this={heading}>Audit columns</h2>
    <p>Check whether each card sits in the right column for the work actually committed. Nothing moves until you review and apply corrections.</p></div>
  {#if loadError}
    <div class="inline-alert error" role="alert"><span>!</span><span>{loadError}</span><button class="text-button" type="button" on:click={() => void load()}>Retry</button></div>
  {:else if loading && !readiness}
    <div class="list-skeleton" aria-label="Counting cards and checking readiness"><div></div><div></div></div>
  {:else if readiness}
    <fieldset class="ca-scope-block">
      <legend>Baseline</legend>
      <p class="ca-hint">Cards are judged against committed work here.</p>
      {#if repositories.length}
        {#each repositories as repo (repo.repository)}
          {@const choice = intent.baselines[repo.repository]}
          <div class="ca-baseline-row">
            <span class="ca-mono">{repo.repository}</span>
            <label class="ca-field"><span class="sr-only">Baseline for {repo.repository}</span>
              <select aria-label={`Baseline for ${repo.repository}`} value={choice?.kind === 'pr' ? 'pr' : 'saved'} disabled={starting}
                on:change={(event) => setBaseline(repo.repository, event.currentTarget.value === 'pr' ? 'pr' : 'saved')}>
                <option value="saved">{repo.ref} (saved branch)</option>
                <option value="pr">Pull request head…</option>
              </select></label>
            {#if choice?.kind === 'pr'}
              <label class="ca-field ca-pr">Pull request number<input inputmode="numeric" value={choice.pullRequest} disabled={starting}
                aria-invalid={prInvalid} on:input={(event) => setPullRequest(repo.repository, event.currentTarget.value)} /></label>
            {/if}
          </div>
        {/each}
        {#if prChoice}<p class="ca-hint">The PR head becomes this run's baseline. Linked PRs on other cards stay separate evidence.</p>
        {:else}<p class="ca-hint">Linked pull requests are shown as separate evidence, never as the baseline.</p>{/if}
      {:else}
        <p class="ca-problem">No repository is selected for {project.name}. Choose a repository and branch first.</p>
      {/if}
      <button class="text-button ca-inline-action" type="button" on:click={onEditRepositories}>Edit saved branch</button>
    </fieldset>

    <fieldset class="ca-scope-block">
      <legend>Cards to audit</legend>
      <label class="ca-choice"><input type="radio" name="ca-mode" value="all" checked={intent.mode === 'all'} disabled={starting} on:change={() => { intent = { ...intent, mode: 'all' }; }} />
        <span><strong>All cards</strong> <span class="ca-hint">{cardWord(cards.length)} · Includes {stateCount('completed')} Done and {stateCount('blocked')} Blocked</span></span></label>
      <label class="ca-choice"><input type="radio" name="ca-mode" value="columns" checked={intent.mode === 'columns'} disabled={starting} on:change={() => { intent = { ...intent, mode: 'columns' }; }} />
        <span><strong>Specific columns</strong></span></label>
      {#if intent.mode === 'columns'}
        <div class="ca-columns">
          {#each activeColumns as column (column.id)}
            <label class="check-label"><input type="checkbox" checked={intent.columnIds.includes(column.id)} disabled={starting}
              on:change={(event) => toggleColumn(column.id, event.currentTarget.checked)} /><span>{column.name} ({byColumn[column.id] || 0})</span></label>
          {/each}
        </div>
      {/if}
      {#if intent.taskIds.length}
        <label class="ca-choice"><input type="radio" name="ca-mode" value="selected" checked={intent.mode === 'selected'} disabled={starting} on:change={() => { intent = { ...intent, mode: 'selected' }; }} />
          <span><strong>Selected cards ({selectedIds.length})</strong>{#if selectedIds.length < intent.taskIds.length} <span class="ca-hint">{intent.taskIds.length - selectedIds.length} no longer available</span>{/if}</span></label>
      {/if}
      <p class="ca-hint">Counted across every page on the server, up to {AUDIT_SCOPE_LIMIT.toLocaleString()} cards per audit.</p>
      {#if overLimit}<p class="ca-problem" role="alert">This scope has more than {AUDIT_SCOPE_LIMIT.toLocaleString()} cards. Choose specific columns or selected cards.</p>{/if}
    </fieldset>

    <div class="ca-ready" aria-label="Readiness">
      <p data-ready={readiness.github.state === 'connected'}><strong>GitHub</strong> <span>{readiness.github.message || readiness.github.state}</span>
        {#if readiness.github.state !== 'connected'}<button class="text-button" type="button" on:click={onEditRepositories}>Check repositories</button>{/if}</p>
      <p data-ready={readiness.ai.state === 'connected'}><strong>Your AI</strong>
        <span>{readiness.ai.state === 'connected' ? readiness.ai.message : 'Connect your AI account in Settings to run column audits.'}</span>
        {#if readiness.ai.state !== 'connected'}<a href="/settings">Open Settings</a>{/if}</p>
      {#if !readiness.ready}<button class="text-button" type="button" disabled={loading} on:click={() => void load()}>Check again</button>{/if}
    </div>
    {#if startError}
      <div class="inline-alert error" role="alert"><span>!</span><span>{startError}</span>{#if conflictId}<button class="text-button" type="button" on:click={() => onOpenExisting(conflictId)}>Open running audit</button>{/if}</div>
    {/if}
    <div class="ca-actions">
      <button class="text-button" type="button" disabled={starting} on:click={onClose}>Cancel</button>
      <button class="button primary" type="button" disabled={!canStart} on:click={() => void start()}>{#if starting}<span class="button-spinner"></span>{/if}Audit {cardWord(scopedIds.length)}</button>
    </div>
  {/if}
</section>
