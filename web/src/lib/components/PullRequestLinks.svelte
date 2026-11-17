<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../api';
  import { ApiError, type PullRequestLink, type Task } from '../types';

  export let task: Task;
  export let urlDraft = '';
  export let pendingWrites = false;
  export let disabled = false;
  export let onTaskUpdated: (updated: Task) => void = () => undefined;
  export let onRefreshTask: () => void | Promise<void> = () => undefined;

  let items: PullRequestLink[] = [];
  let loading = false;
  let loadError = '';
  let mutationError = '';
  let announcement = '';
  let syncedTaskId = '';
  let syncedVersion = 0;
  let scope = 0;
  let readSequence = 0;
  let readController: AbortController | undefined;
  let destroyed = false;

  // Version refreshes never reset a typed URL. Scope checks also protect an
  // A → B → A switch from requests made during the first visit to A.
  $: if (task.id !== syncedTaskId) {
    scope += 1;
    syncedTaskId = task.id;
    syncedVersion = task.version;
    items = [];
    mutationError = '';
    announcement = '';
    void loadLinks();
  }
  $: if (!pendingWrites && task.id === syncedTaskId && task.version !== syncedVersion) {
    syncedVersion = task.version;
    void loadLinks();
  }

  onDestroy(() => {
    destroyed = true;
    scope += 1;
    readController?.abort();
  });

  function current(taskId: string, requestedScope: number): boolean {
    return !destroyed && task.id === taskId && scope === requestedScope;
  }

  function description(link: PullRequestLink): string {
    return `${link.provider === 'github' ? 'GitHub' : 'Gitea'} · ${link.host}/${link.repository} #${link.number}`;
  }

  function message(error: unknown, fallback: string): string {
    if (error instanceof ApiError && (error.status === 409 || error.status === 412)) {
      return 'This task changed elsewhere. Refresh the task and links, then retry. Your URL is still here.';
    }
    return error instanceof Error && error.message.trim() ? error.message : fallback;
  }

  async function loadLinks(): Promise<void> {
    const taskId = task.id;
    const requestedScope = scope;
    const sequence = ++readSequence;
    readController?.abort();
    readController = new AbortController();
    loading = true;
    loadError = '';
    try {
      const result = await api.listTaskPullRequests(taskId, readController.signal);
      if (current(taskId, requestedScope) && sequence === readSequence) items = result.items;
    } catch (error) {
      if (current(taskId, requestedScope) && sequence === readSequence && !(error instanceof DOMException && error.name === 'AbortError')) {
        loadError = message(error, 'Pull request links could not be loaded. Retry loading them.');
      }
    } finally {
      if (current(taskId, requestedScope) && sequence === readSequence) loading = false;
    }
  }

  async function refresh(): Promise<void> {
    const taskId = task.id;
    const requestedScope = scope;
    try {
      await onRefreshTask();
      if (current(taskId, requestedScope)) await loadLinks();
    } catch (error) {
      if (current(taskId, requestedScope)) loadError = message(error, 'The task could not be refreshed. Retry when connected.');
    }
  }

  async function mutate(link?: PullRequestLink): Promise<void> {
    const submittedUrl = urlDraft.trim();
    if (pendingWrites || disabled || (!link && !submittedUrl)) return;
    const taskId = task.id;
    const requestedScope = scope;
    const draftAtSubmit = urlDraft;
    pendingWrites = true;
    mutationError = '';
    announcement = '';
    // A list fetched before this write must not restore a removed link or
    // hide a newly saved one after the mutation completes.
    readSequence += 1;
    readController?.abort();
    loading = false;
    try {
      if (link) {
        const result = await api.unlinkTaskPullRequest(taskId, link.id, task.version);
        if (!current(taskId, requestedScope)) return;
        items = items.filter((item) => item.id !== link.id);
        onTaskUpdated(result.task);
        announcement = `Removed pull request ${description(link)}.`;
      } else {
        const result = await api.linkTaskPullRequest(taskId, submittedUrl, task.version);
        if (!current(taskId, requestedScope)) return;
        items = [...items.filter((item) => item.id !== result.link.id), result.link];
        onTaskUpdated(result.task);
        if (urlDraft === draftAtSubmit) urlDraft = '';
        announcement = `Linked pull request ${description(result.link)}.`;
      }
    } catch (error) {
      if (current(taskId, requestedScope)) mutationError = message(error, 'The pull request link could not be saved. Check the URL and retry.');
    } finally {
      if (current(taskId, requestedScope)) pendingWrites = false;
    }
  }
</script>

<section class="pull-request-panel" aria-labelledby="pull-requests-heading" aria-busy={pendingWrites || loading}>
  <div class="pull-request-heading">
    <h2 id="pull-requests-heading">Pull requests</h2>
    <button class="text-button" type="button" disabled={pendingWrites || loading || disabled} on:click={refresh}>Refresh task and links</button>
  </div>
  <p class="pull-request-help" id="pull-request-help">Save a GitHub or Gitea pull request URL. Links do not include live status.</p>
  <form on:submit|preventDefault={() => mutate()}>
    <label for="pull-request-url">Pull request URL</label>
    <div class="pull-request-form-row">
      <input id="pull-request-url" type="url" bind:value={urlDraft} placeholder="https://github.com/owner/repo/pull/123" aria-describedby="pull-request-help" required disabled={disabled} />
      <button class="button primary compact-button" type="submit" disabled={pendingWrites || disabled || !urlDraft.trim()}>{pendingWrites ? 'Saving…' : 'Link pull request'}</button>
    </div>
  </form>
  {#if mutationError}<p class="pull-request-error" role="alert">{mutationError}</p>{/if}
  {#if loadError}<p class="pull-request-error" role="alert">{loadError} <button class="text-button" type="button" disabled={pendingWrites || disabled} on:click={loadLinks}>Retry loading links</button></p>{/if}
  <p class="pull-request-status" role="status">{pendingWrites ? 'Saving pull request link…' : loading ? 'Loading pull request links…' : announcement}</p>
  {#if items.length}
    <ul>
      {#each items as link (link.id)}
        <li>
          <a href={link.url} target="_blank" rel="noopener noreferrer">{description(link)}</a>
          <button class="text-button danger-button" type="button" aria-label={`Remove pull request ${description(link)}`} disabled={pendingWrites || disabled} on:click={() => mutate(link)}>Remove</button>
        </li>
      {/each}
    </ul>
  {:else if !loading && !loadError}
    <p class="pull-request-empty">No pull requests linked.</p>
  {/if}
</section>

<style>
  .pull-request-panel { display: grid; gap: 10px; margin-top: 22px; padding: 15px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-raised); }
  .pull-request-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  h2 { margin: 0; color: var(--ink); font: 600 var(--text-sm) var(--font-sans); }
  .pull-request-help, .pull-request-empty, .pull-request-status, .pull-request-error { margin: 0; font-size: var(--text-xs); line-height: 1.45; }
  .pull-request-help, .pull-request-empty, .pull-request-status { color: var(--muted); }
  .pull-request-status:empty { display: none; }
  .pull-request-error { color: var(--red); }
  form { display: grid; gap: 6px; }
  label { color: var(--ink-soft); font-size: var(--text-xs); font-weight: 600; }
  .pull-request-form-row { display: flex; gap: 7px; }
  input { flex: 1; min-width: 0; }
  ul { display: grid; gap: 10px; list-style: none; margin: 0; padding: 0; }
  li { display: flex; align-items: center; gap: 10px; min-width: 0; }
  li a { flex: 1; min-width: 0; overflow-wrap: anywhere; color: var(--purple); font-size: var(--text-sm); }
  li button { flex-shrink: 0; }
  @media (max-width: 620px) {
    .pull-request-heading { align-items: flex-start; flex-direction: column; gap: 6px; }
    .pull-request-form-row { flex-direction: column; }
    .pull-request-form-row button { align-self: flex-start; }
  }
</style>
