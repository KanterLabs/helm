<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import { ApiError, type Task, type Watch } from '../types';

  export let task: Task;
  /** Changes whenever the authenticated actor/session changes. */
  export let sessionKey = '';
  export let disabled = false;

  let mounted = false;
  let disposed = false;
  let loading = false;
  let saving = false;
  let error = '';
  let sessionInvalidated = false;
  let watch: Watch | null = null;
  let requestSequence = 0;
  let mutationSequence = 0;
  let observedContext = '';
  let observedSessionKey = sessionKey;

  $: contextKey = `${sessionKey}:${task?.project_id || ''}:${task?.id || ''}`;
  $: if (mounted && sessionKey !== observedSessionKey) {
    observedSessionKey = sessionKey;
    sessionInvalidated = false;
  }
  $: if (mounted && contextKey !== observedContext) {
    observedContext = contextKey;
    resetState();
    void loadWatch();
  }

  onMount(() => {
    mounted = true;
    disposed = false;
    observedContext = contextKey;
    observedSessionKey = sessionKey;
    void loadWatch();
    const authInvalidated = () => {
      requestSequence += 1;
      mutationSequence += 1;
      watch = null;
      loading = false;
      saving = false;
      sessionInvalidated = true;
      error = 'Task watching is unavailable until your session is renewed.';
    };
    window.addEventListener('helm:auth-invalidated', authInvalidated);
    return () => {
      disposed = true;
      mounted = false;
      window.removeEventListener('helm:auth-invalidated', authInvalidated);
    };
  });

  function isCurrent(expectedSession: string, expectedContext: string): boolean {
    return mounted && !disposed && !sessionInvalidated && expectedSession === sessionKey && expectedContext === contextKey;
  }

  function resetState(): void {
    requestSequence += 1;
    mutationSequence += 1;
    loading = false;
    saving = false;
    error = '';
    watch = null;
  }

  async function loadWatch(): Promise<void> {
    const expectedSession = sessionKey;
    const expectedContext = contextKey;
    const taskId = task?.id;
    const projectId = task?.project_id;
    const requestId = ++requestSequence;
    if (!isCurrent(expectedSession, expectedContext) || !taskId || !projectId) return;
    loading = true;
    try {
      const result = await api.listWatches({ project: projectId, task: taskId });
      if (!isCurrent(expectedSession, expectedContext) || requestId !== requestSequence) return;
      watch = result.data.find((item) => item.task_id === taskId && item.project_id === projectId) || null;
      error = '';
    } catch (reason) {
      if (!isCurrent(expectedSession, expectedContext) || requestId !== requestSequence) return;
      error = reason instanceof ApiError && reason.status === 403
        ? 'Task watching is unavailable for this account.'
        : friendlyError(reason, 'Task watch status could not be loaded.');
      watch = null;
    } finally {
      if (isCurrent(expectedSession, expectedContext) && requestId === requestSequence) loading = false;
    }
  }

  async function toggleWatch(): Promise<void> {
    const expectedSession = sessionKey;
    const expectedContext = contextKey;
    const taskId = task?.id;
    if (!isCurrent(expectedSession, expectedContext) || !taskId || disabled || loading || saving) return;
    const mutationId = ++mutationSequence;
    const existing = watch;
    requestSequence += 1;
    saving = true;
    try {
      const created = existing ? null : await api.createWatch({ task_id: taskId });
      if (existing) await api.deleteWatch(existing.id);
      if (!isCurrent(expectedSession, expectedContext) || mutationId !== mutationSequence) return;
      watch = created;
      error = '';
    } catch (reason) {
      if (!isCurrent(expectedSession, expectedContext) || mutationId !== mutationSequence) return;
      error = reason instanceof ApiError && reason.status === 403
        ? 'Task watching is unavailable for this account.'
        : friendlyError(reason, 'Task watch could not be updated.');
    } finally {
      if (isCurrent(expectedSession, expectedContext) && mutationId === mutationSequence) saving = false;
    }
  }

  function friendlyError(reason: unknown, fallback: string): string {
    if (reason instanceof ApiError && reason.message) return reason.message;
    if (reason instanceof Error && reason.message) return reason.message;
    return fallback;
  }
</script>

<div class="task-watch-toggle">
  <button
    class="task-watch-button"
    type="button"
    data-task-watch-toggle
    aria-pressed={Boolean(watch)}
    aria-label={watch ? `Unwatch task ${task.key}` : `Watch task ${task.key}`}
    disabled={disabled || loading || saving}
    on:click={() => void toggleWatch()}
  >
    <span aria-hidden="true">{watch ? '◉' : '◌'}</span>
    {watch ? 'Watching' : 'Watch'}
  </button>
  {#if error}<span class="task-watch-error" role="alert">{error}</span>{/if}
</div>

<style>
  .task-watch-toggle { display: flex; align-items: center; gap: .4rem; min-width: 0; }
  .task-watch-button { display: inline-flex; align-items: center; gap: .35rem; min-height: 2rem; padding: .3rem .55rem; border: 1px solid var(--border, #dedbe8); border-radius: .4rem; background: transparent; color: var(--muted, #706b7f); font-size: .7rem; white-space: nowrap; }
  .task-watch-button[aria-pressed="true"] { border-color: color-mix(in srgb, var(--accent, #6558d8) 55%, var(--border, #dedbe8)); background: color-mix(in srgb, var(--accent, #6558d8) 10%, transparent); color: var(--accent, #6558d8); }
  .task-watch-button:disabled { cursor: wait; opacity: .6; }
  .task-watch-error { max-width: 11rem; color: var(--danger, #b33d4b); font-size: .62rem; overflow-wrap: anywhere; }
</style>
