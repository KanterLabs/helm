<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type PullRequestArtifact, type Task } from '../../types';
  import { invalidateArtifactSummary } from './artifactSummaries';
  import PullRequestRow from './PullRequestRow.svelte';
  import { consumePullRequestFocus, parsePullRequestUrl, samePullRequest, urlDrafts } from './pullRequests';

  /** Task drawer Pull requests section: explicit Link, Unlink and Refresh. */
  export let task: Task;
  export let columnName = '';
  export let onTaskUpdated: (updated: Task) => void = () => undefined;
  export let onRefreshTask: () => void | Promise<void> = () => undefined;

  let artifacts: PullRequestArtifact[] = [];
  let loading = false;
  let loadError = '';
  let url = '';
  let formError = '';
  let linking = false;
  let verifying = new Set<string>();
  let unlinking = new Set<string>();
  let rowErrors: Record<string, string> = {};
  let announcement = '';
  let loadedTaskId = '';
  let listRequest = 0;
  let destroyed = false;
  let input: HTMLInputElement;
  let heading: HTMLElement;
  let section: HTMLElement;

  $: if (task?.id && task.id !== loadedTaskId) switchTask(task.id);
  $: parsed = url.trim() ? parsePullRequestUrl(url) : null;
  $: preview = parsed && 'repository' in parsed ? `${parsed.repository} #${parsed.number}` : '';
  $: verified = artifacts.filter((item) => item.verification === 'verified' && item.snapshot);
  $: merged = verified.filter((item) => item.snapshot?.state === 'merged').length;

  onMount(() => { void focusHeadingIfRequested(); });
  onDestroy(() => {
    destroyed = true;
    listRequest += 1;
  });

  function switchTask(taskId: string): void {
    if (loadedTaskId) urlDrafts.set(loadedTaskId, url);
    loadedTaskId = taskId;
    artifacts = [];
    rowErrors = {};
    verifying = new Set();
    unlinking = new Set();
    formError = '';
    url = urlDrafts.get(taskId);
    void load(taskId);
    void focusHeadingIfRequested();
  }

  async function focusHeadingIfRequested(): Promise<void> {
    if (!task?.id || !consumePullRequestFocus(task.id)) return;
    await tick();
    section?.scrollIntoView({ block: 'start' });
    heading?.focus();
  }

  const current = (taskId: string) => !destroyed && task?.id === taskId;

  async function load(taskId: string): Promise<void> {
    const requestId = ++listRequest;
    loading = true;
    loadError = '';
    try {
      const result = await api.listTaskArtifacts(taskId);
      if (!current(taskId) || requestId !== listRequest) return;
      artifacts = (result?.data || []).filter((item) => item && item.kind === 'pull_request');
    } catch (error) {
      if (current(taskId) && requestId === listRequest) loadError = message(error, 'Pull requests could not be loaded.');
    } finally {
      if (requestId === listRequest) loading = false;
    }
  }

  function message(error: unknown, fallback: string): string {
    if (error instanceof ApiError && error.status === 0) return error.message;
    if (error instanceof ApiError || error instanceof Error) return error.message || fallback;
    return fallback;
  }

  function staleVersion(error: unknown): boolean {
    return error instanceof ApiError && (error.status === 412 || ['stale_task', 'version_conflict', 'precondition_failed'].includes(error.code));
  }

  async function recoverStale(error: unknown): Promise<void> {
    const latest = error instanceof ApiError ? error.details.current as Partial<Task> | undefined : undefined;
    if (latest && typeof latest.id === 'string' && latest.id === task.id && typeof latest.version === 'number') onTaskUpdated(latest as Task);
    await onRefreshTask();
  }

  function taskVersion(taskId: string, version: number): void {
    if (!current(taskId)) return;
    onTaskUpdated({ ...task, version });
    invalidateArtifactSummary(taskId);
    // Pick up the authoritative timestamps/activity without touching drafts.
    void onRefreshTask();
  }

  function onInput(): void {
    formError = '';
    urlDrafts.set(task.id, url);
  }

  async function link(): Promise<void> {
    if (linking) return;
    const result = parsePullRequestUrl(url);
    if ('error' in result) return refuse(result.error);
    if (artifacts.some((item) => samePullRequest(item, result))) return refuse('That pull request is already linked to this task.');
    const taskId = task.id;
    linking = true;
    formError = '';
    try {
      const created = await api.linkTaskArtifact(taskId, result.canonical, task.version);
      urlDrafts.set(taskId, '');
      if (!current(taskId)) return;
      artifacts = [...artifacts.filter((item) => item.id !== created.artifact.id), created.artifact];
      url = '';
      taskVersion(taskId, created.task_version);
      announcement = `Linked ${created.artifact.repository} #${created.artifact.number}.`;
      await tick();
      section?.querySelector<HTMLElement>(`[data-artifact-id="${CSS.escape(created.artifact.id)}"]`)?.focus();
      if (created.artifact.verification !== 'verified' && !created.artifact.error_code) void refresh(created.artifact);
    } catch (error) {
      if (!current(taskId)) return;
      if (staleVersion(error)) {
        formError = 'This task changed elsewhere. Helm reloaded it — select Link pull request again to retry.';
        await recoverStale(error);
      } else {
        formError = message(error, 'The pull request could not be linked.');
      }
      input?.focus();
    } finally {
      linking = false;
    }
  }

  async function refuse(reason: string): Promise<void> {
    formError = reason;
    announcement = reason;
    await tick();
    input?.focus();
  }

  async function refresh(artifact: PullRequestArtifact): Promise<void> {
    const taskId = task.id;
    if (verifying.has(artifact.id)) return;
    verifying = new Set(verifying).add(artifact.id);
    const { [artifact.id]: _cleared, ...others } = rowErrors;
    rowErrors = others;
    try {
      const updated = await api.refreshTaskArtifact(taskId, artifact.id);
      if (!current(taskId)) return;
      artifacts = artifacts.map((item) => item.id === artifact.id ? updated : item);
      invalidateArtifactSummary(taskId);
      announcement = `#${updated.number} refreshed.`;
    } catch (error) {
      if (current(taskId)) rowErrors = { ...rowErrors, [artifact.id]: message(error, 'GitHub could not be checked. Try again later.') };
    } finally {
      const next = new Set(verifying);
      next.delete(artifact.id);
      verifying = next;
    }
  }

  async function unlink(artifact: PullRequestArtifact): Promise<void> {
    const taskId = task.id;
    if (unlinking.has(artifact.id)) return;
    unlinking = new Set(unlinking).add(artifact.id);
    try {
      const result = await api.unlinkTaskArtifact(taskId, artifact.id, task.version);
      if (!current(taskId)) return;
      artifacts = artifacts.filter((item) => item.id !== artifact.id);
      taskVersion(taskId, result.task_version);
      announcement = `Unlinked ${artifact.repository} #${artifact.number}. Audit evidence is kept.`;
      await tick();
      input?.focus();
    } catch (error) {
      if (!current(taskId)) return;
      rowErrors = { ...rowErrors, [artifact.id]: staleVersion(error) ? 'This task changed elsewhere. Helm reloaded it — try Unlink again.' : message(error, 'The pull request could not be unlinked.') };
      if (staleVersion(error)) await recoverStale(error);
    } finally {
      const next = new Set(unlinking);
      next.delete(artifact.id);
      unlinking = next;
    }
  }
</script>

<section class="drawer-section pr-section" aria-labelledby="task-pr-heading" data-task-pull-requests bind:this={section}>
  <div class="section-heading-inline"><h2 id="task-pr-heading" tabindex="-1" bind:this={heading}>Pull requests · {artifacts.length}</h2><span class="optional">GitHub beta</span></div>
  {#if loadError}
    <div class="inline-alert error" role="alert"><span>!</span><span>{loadError}</span><button class="text-button" type="button" on:click={() => load(task.id)}>Retry</button></div>
  {:else if loading && !artifacts.length}
    <div class="list-skeleton" aria-label="Loading pull requests"><div></div></div>
  {:else if artifacts.length}
    <ul class="pr-list">
      {#each artifacts as artifact (artifact.id)}
        <PullRequestRow {artifact} verifying={verifying.has(artifact.id)} unlinking={unlinking.has(artifact.id)} error={rowErrors[artifact.id] || ''} onRefresh={refresh} onUnlink={unlink} />
      {/each}
    </ul>
  {:else}
    <p class="pr-empty">No pull requests linked. Link one to see its GitHub review and check state here.</p>
  {/if}
  <form class="pr-form" novalidate on:submit|preventDefault={() => void link()}>
    <label for="task-pr-url">Pull request URL</label>
    <div class="pr-form-row">
      <input id="task-pr-url" type="url" inputmode="url" autocomplete="off" spellcheck="false" placeholder="https://github.com/owner/repo/pull/123" bind:this={input} bind:value={url} on:input={onInput} aria-invalid={formError ? 'true' : undefined} aria-describedby={formError ? 'task-pr-error' : preview ? 'task-pr-preview' : undefined} disabled={linking} />
      <button class="button primary compact-button" type="submit" disabled={linking || !url.trim()}>{#if linking}<span class="button-spinner"></span>{/if}Link pull request</button>
    </div>
    {#if formError}<p class="pr-form-error" id="task-pr-error">{formError}</p>{:else if preview}<p class="pr-form-preview">Will link <span id="task-pr-preview" class="pr-mono" data-pr-preview>{preview}</span></p>{/if}
  </form>
  <dl class="pr-facts" data-pr-facts aria-label="Separate delivery facts">
    <div><dt>PRs merged</dt><dd>{merged} of {artifacts.length}{verified.length < artifacts.length ? ` · ${artifacts.length - verified.length} not verified` : ''}</dd></div>
    <div><dt>Deployed</dt><dd>Not tracked here</dd></div>
    <div><dt>Task</dt><dd>{task.completed_at ? 'Completed' : columnName || 'Unknown column'}</dd></div>
  </dl>
  <p class="pr-note">Merging never moves or completes this task. Complete it explicitly after verification.</p>
  <div class="sr-only" aria-live="polite" aria-atomic="true">{announcement}</div>
</section>

<style>
  .pr-section { display: grid; gap: 12px; }
  .pr-section h2:focus-visible { outline: 2px solid var(--purple); outline-offset: 3px; border-radius: 4px; }
  .pr-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .pr-empty, .pr-note, .pr-form-preview, .pr-form-error { margin: 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.45; }
  .pr-form { display: grid; gap: 6px; }
  .pr-form label { color: var(--muted); font-size: var(--text-xs); font-weight: 500; }
  .pr-form-row { display: flex; flex-wrap: wrap; gap: 8px; }
  .pr-form-row input { flex: 1 1 220px; min-width: 0; }
  .pr-form-error { color: var(--semantic-red); }
  .pr-mono { color: var(--ink); font-family: var(--font-mono); }
  .pr-facts { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 6px; margin: 0; }
  .pr-facts div { padding: 10px 12px; border-radius: 8px; background: var(--surface-muted); }
  .pr-facts dt { color: var(--muted); font-size: var(--text-2xs); }
  .pr-facts dd { margin: 2px 0 0; color: var(--ink); font-size: var(--text-xs); font-weight: 600; }
  @media (max-width: 520px) { .pr-facts { grid-template-columns: 1fr; } }
</style>
