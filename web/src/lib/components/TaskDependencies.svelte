<script context="module" lang="ts">
  export {
    dependencyCandidates,
    dependencyMutationMessage,
    dependencyStateLabel,
    nextDependencyOptionIndex
  } from './dependencyRelations';
</script>

<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api, listAllTasks } from '../api';
  import {
    ApiError,
    type Task,
    type TaskDependencies,
    type TaskReference
  } from '../types';
  import DependencyPrerequisiteSearch from './DependencyPrerequisiteSearch.svelte';
  import DependencyRelationshipLists from './DependencyRelationshipLists.svelte';
  import { dependencyMutationMessage } from './dependencyRelations';

  export let task: Task;
  /** Incremented when an event changes derived readiness without a task version bump. */
  export let refreshToken = 0;
  export let onTaskUpdated: (updated: Task) => void = () => undefined;
  export let onNavigate: (reference: TaskReference) => void | Promise<void> = () => undefined;
  export let onRefreshTask: () => void | Promise<void> = () => undefined;

  let relations: TaskDependencies | null = null;
  let projectTasks: Task[] = [];
  let relationsLoading = false;
  let candidatesLoading = false;
  let relationsError = '';
  let addError = '';
  let removeErrors: Record<string, string> = {};
  let pendingAdd = false;
  let removing: Record<string, boolean> = {};
  let prerequisiteSearch: DependencyPrerequisiteSearch;
  let announcement = '';
  let loadedTaskId = '';
  let loadedRefreshToken = -1;
  let loadedProjectId = '';
  let relationRequest = 0;
  let candidateRequest = 0;
  let destroyed = false;

  $: mutationBusy = pendingAdd || Object.values(removing).some(Boolean);
  $: if (task?.id && (task.id !== loadedTaskId || refreshToken !== loadedRefreshToken)) {
    loadedTaskId = task.id;
    loadedRefreshToken = refreshToken;
    void loadRelations(task.id);
  }
  $: if (task?.project_id && task.project_id !== loadedProjectId) {
    loadedProjectId = task.project_id;
    void loadCandidates(task.project_id);
  }

  onDestroy(() => {
    destroyed = true;
    relationRequest += 1;
    candidateRequest += 1;
  });

  async function loadRelations(taskId: string): Promise<boolean> {
    const requestId = ++relationRequest;
    relationsLoading = true;
    relationsError = '';
    try {
      const loaded = await api.getTaskDependencies(taskId);
      if (destroyed || requestId !== relationRequest || task.id !== taskId) return false;
      relations = {
        prerequisites: loaded.prerequisites || [],
        dependents: loaded.dependents || []
      };
      return true;
    } catch (error) {
      if (!destroyed && requestId === relationRequest && task.id === taskId) {
        relationsError = dependencyMutationMessage(error instanceof ApiError ? error : null, 'Dependencies could not be loaded.');
      }
      return false;
    } finally {
      if (!destroyed && requestId === relationRequest) relationsLoading = false;
    }
  }

  /** Lets the event poller include this independently loaded view in its retry gate. */
  export async function refreshRelationships(): Promise<boolean> {
    return loadRelations(task.id);
  }

  async function loadCandidates(projectId: string): Promise<void> {
    const requestId = ++candidateRequest;
    candidatesLoading = true;
    try {
      const loaded = await listAllTasks(projectId, { limit: 200 });
      if (destroyed || requestId !== candidateRequest || task.project_id !== projectId) return;
      projectTasks = loaded.data || [];
    } catch {
      if (!destroyed && requestId === candidateRequest && task.project_id === projectId) projectTasks = [];
    } finally {
      if (!destroyed && requestId === candidateRequest) candidatesLoading = false;
    }
  }

  function staleTask(error: unknown): Task | null {
    if (!(error instanceof ApiError) || error.code !== 'stale_task') return null;
    const current = error.details.current;
    if (!current || typeof current !== 'object') return null;
    const candidate = current as Partial<Task>;
    return typeof candidate.id === 'string' && typeof candidate.version === 'number' && typeof candidate.title === 'string'
      ? candidate as Task
      : null;
  }

  async function recoverAfterMutationError(error: unknown, taskId: string): Promise<void> {
    const current = staleTask(error);
    if (current) onTaskUpdated(current);
    if (error instanceof ApiError && error.code === 'stale_task') await onRefreshTask();
    await loadRelations(taskId);
  }

  async function addPrerequisite(candidate: Task): Promise<boolean> {
    if (mutationBusy || task.id === candidate.id) return false;
    const taskId = task.id;
    const version = task.version;
    pendingAdd = true;
    addError = '';
    try {
      const updated = await api.addTaskDependency(taskId, candidate.id, version);
      if (destroyed || task.id !== taskId) return false;
      onTaskUpdated(updated);
      await loadRelations(taskId);
      announcement = `${candidate.key} added as a prerequisite.`;
      // Re-enable the control before the search restores focus to itself.
      pendingAdd = false;
      return true;
    } catch (error) {
      if (destroyed || task.id !== taskId) return false;
      addError = dependencyMutationMessage(error instanceof ApiError ? error : null, 'The prerequisite could not be added.');
      announcement = addError;
      await recoverAfterMutationError(error, taskId);
      return false;
    } finally {
      if (!destroyed && task.id === taskId) pendingAdd = false;
    }
  }

  async function removePrerequisite(reference: TaskReference): Promise<void> {
    if (mutationBusy) return;
    const taskId = task.id;
    const version = task.version;
    removing = { ...removing, [reference.id]: true };
    removeErrors = { ...removeErrors, [reference.id]: '' };
    try {
      const updated = await api.removeTaskDependency(taskId, reference.id, version);
      if (destroyed || task.id !== taskId) return;
      onTaskUpdated(updated);
      await loadRelations(taskId);
      announcement = `${reference.key} removed as a prerequisite.`;
      await tick();
      prerequisiteSearch?.focus();
    } catch (error) {
      if (destroyed || task.id !== taskId) return;
      const message = dependencyMutationMessage(error instanceof ApiError ? error : null, 'The prerequisite could not be removed.');
      removeErrors = { ...removeErrors, [reference.id]: message };
      announcement = message;
      await recoverAfterMutationError(error, taskId);
    } finally {
      if (!destroyed && task.id === taskId) {
        const next = { ...removing };
        delete next[reference.id];
        removing = next;
      }
    }
  }
</script>

<section class="dependency-panel" aria-labelledby="task-dependencies-heading" aria-busy={relationsLoading}>
  <div class="dependency-heading">
    <div>
      <h2 id="task-dependencies-heading">Dependencies</h2>
      <p>Finish every prerequisite before this task starts or completes.</p>
    </div>
    {#if task.dependency_summary?.blocked}
      <span class="dependency-blocked">Blocked by {task.dependency_summary.unmet_prerequisite_count}</span>
    {:else if task.dependency_summary?.prerequisite_count}
      <span class="dependency-ready">Ready</span>
    {/if}
  </div>

  <p class="sr-only" aria-live="polite" aria-atomic="true">{announcement}</p>

  {#if relationsLoading && !relations}
    <div class="dependency-loading" role="status"><span></span><span>Loading relationships…</span></div>
  {:else if relationsError && !relations}
    <div class="dependency-load-error" role="alert">
      <span>{relationsError}</span>
      <button type="button" on:click={() => loadRelations(task.id)}>Retry</button>
    </div>
  {:else}
    <DependencyRelationshipLists
      {relations}
      {mutationBusy}
      {removing}
      {removeErrors}
      {onNavigate}
      onRemove={removePrerequisite}
    />

    <DependencyPrerequisiteSearch
      bind:this={prerequisiteSearch}
      bind:addError
      {task}
      {relations}
      {projectTasks}
      {pendingAdd}
      {candidatesLoading}
      onSelect={addPrerequisite}
    />
  {/if}
</section>

<style>
  .dependency-panel {
    display: grid;
    gap: 14px;
    padding: 15px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface-raised);
  }
  .dependency-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .dependency-heading h2 { margin: 0; color: var(--ink); font-size: var(--text-xs); }
  .dependency-heading p { margin: 4px 0 0; color: var(--muted); font-size: var(--text-xs); line-height: 1.45; }
  .dependency-blocked, .dependency-ready { flex: 0 0 auto; padding: 4px 7px; border-radius: 6px; font-size: var(--text-xs); font-weight: 600; }
  .dependency-blocked { color: var(--red); background: var(--red-soft); }
  .dependency-ready { color: var(--green); background: var(--green-soft); }
  .dependency-loading, .dependency-load-error { min-height: 62px; display: flex; align-items: center; justify-content: center; gap: 8px; color: var(--muted); font-size: var(--text-xs); }
  .dependency-loading > span:first-child { width: 13px; height: 13px; border: 2px solid var(--border); border-top-color: var(--purple); border-radius: 50%; animation: spin .7s linear infinite; }
  .dependency-load-error { flex-direction: column; color: var(--red); text-align: center; }
  .dependency-load-error button { min-height: 36px; padding: 0 12px; border: 1px solid var(--border); border-radius: 7px; color: var(--ink); background: var(--surface); }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; clip-path: inset(50%); }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (max-width: 520px) {
    .dependency-panel { padding: 13px; }
  }
  @media (prefers-reduced-motion: reduce) {
    .dependency-loading > span:first-child { animation: none; }
  }
</style>
