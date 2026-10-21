<script context="module" lang="ts">
  export {
    hierarchyCandidates,
    hierarchyMutationMessage,
    hierarchyReferenceTask,
    nextHierarchyOptionIndex
  } from './hierarchyRelations';
</script>

<script lang="ts">
  import { onDestroy, tick } from 'svelte';
  import { api, listAllTasks } from '../api';
  import { ApiError, type Task, type TaskHierarchy, type TaskHierarchyReference } from '../types';
  import HierarchyParentSearch from './HierarchyParentSearch.svelte';
  import HierarchyRelationships from './HierarchyRelationships.svelte';
  import { hierarchyMutationMessage } from './hierarchyRelations';

  export let task: Task;
  /** Incremented when an event changes hierarchy or derived rollups. */
  export let refreshToken = 0;
  export let onTaskUpdated: (updated: Task) => void = () => undefined;
  export let onNavigate: (reference: TaskHierarchyReference) => void | Promise<void> = () => undefined;
  export let onRefreshTask: () => void | Promise<void> = () => undefined;
  export let onCreateChild: () => void | Promise<void> = () => undefined;

  let hierarchy: TaskHierarchy | null = null;
  let projectTasks: Task[] = [];
  let hierarchyLoading = false;
  let candidatesLoading = false;
  let hierarchyError = '';
  let addError = '';
  let parentRemoving = false;
  let pendingAdd = false;
  let parentSearch: HierarchyParentSearch;
  let relationships: HierarchyRelationships;
  let announcement = '';
  let loadedTaskId = '';
  let loadedRefreshToken = -1;
  let loadedProjectId = '';
  let hierarchyRequest = 0;
  let candidateRequest = 0;
  let destroyed = false;

  $: mutationBusy = pendingAdd || parentRemoving;
  $: if (task?.id && (task.id !== loadedTaskId || refreshToken !== loadedRefreshToken)) {
    loadedTaskId = task.id;
    loadedRefreshToken = refreshToken;
    void loadHierarchy(task.id);
  }
  $: if (task?.project_id && task.project_id !== loadedProjectId) {
    loadedProjectId = task.project_id;
    void loadCandidates(task.project_id);
  }

  onDestroy(() => {
    destroyed = true;
    hierarchyRequest += 1;
    candidateRequest += 1;
  });

  async function loadHierarchy(taskId: string): Promise<boolean> {
    const requestId = ++hierarchyRequest;
    hierarchyLoading = true;
    hierarchyError = '';
    try {
      const loaded = await api.getTaskHierarchy(taskId);
      if (destroyed || requestId !== hierarchyRequest || task.id !== taskId) return false;
      hierarchy = {
        parent: loaded.parent || null,
        children: loaded.children || [],
        ancestors: loaded.ancestors || [],
        descendants: loaded.descendants || [],
        summary: loaded.summary || {
          child_count: 0,
          completed_child_count: 0,
          completion_percent: 0,
          state_counts: {},
          blocked_child_count: 0,
          live_agent_work_count: 0,
          action_needed_count: 0,
          stale_agent_work_count: 0
        }
      };
      return true;
    } catch (error) {
      if (!destroyed && requestId === hierarchyRequest && task.id === taskId) {
        hierarchyError = hierarchyMutationMessage(error instanceof ApiError ? error : null, 'Hierarchy could not be loaded.');
      }
      return false;
    } finally {
      if (!destroyed && requestId === hierarchyRequest) hierarchyLoading = false;
    }
  }

  export async function refreshRelationships(): Promise<boolean> {
    return loadHierarchy(task.id);
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
    await loadHierarchy(taskId);
  }

  async function setParent(candidate: Task): Promise<boolean> {
    if (mutationBusy || task.id === candidate.id) return false;
    const taskId = task.id;
    pendingAdd = true;
    addError = '';
    try {
      const updated = await api.setTaskParent(taskId, candidate.id, task.version);
      if (destroyed || task.id !== taskId) return false;
      onTaskUpdated(updated);
      await loadHierarchy(taskId);
      announcement = `${candidate.key} is now this task’s parent.`;
      // Re-enable the search before it restores focus to itself.
      pendingAdd = false;
      return true;
    } catch (error) {
      if (destroyed || task.id !== taskId) return false;
      addError = hierarchyMutationMessage(error instanceof ApiError ? error : null, 'The parent could not be linked.');
      announcement = addError;
      await recoverAfterMutationError(error, taskId);
      return false;
    } finally {
      if (!destroyed && task.id === taskId) pendingAdd = false;
    }
  }

  async function removeParent(): Promise<void> {
    if (mutationBusy || !hierarchy?.parent) return;
    const taskId = task.id;
    parentRemoving = true;
    addError = '';
    try {
      const updated = await api.clearTaskParent(taskId, task.version);
      if (destroyed || task.id !== taskId) return;
      onTaskUpdated(updated);
      await loadHierarchy(taskId);
      announcement = 'Parent relationship removed.';
      await tick();
      parentSearch?.focus();
    } catch (error) {
      if (destroyed || task.id !== taskId) return;
      addError = hierarchyMutationMessage(error instanceof ApiError ? error : null, 'The parent could not be removed.');
      announcement = addError;
      await recoverAfterMutationError(error, taskId);
    } finally {
      if (!destroyed && task.id === taskId) parentRemoving = false;
    }
  }

  async function removeChild(reference: TaskHierarchyReference): Promise<void> {
    if (mutationBusy) return;
    const taskId = task.id;
    parentRemoving = true;
    addError = '';
    try {
      const updated = await api.removeTaskChild(taskId, reference.id, reference.version);
      if (destroyed || task.id !== taskId) return;
      onTaskUpdated(updated);
      await loadHierarchy(taskId);
      announcement = `${reference.key} removed from this task.`;
      parentRemoving = false;
      await tick();
      relationships?.focusChildren();
    } catch (error) {
      if (destroyed || task.id !== taskId) return;
      addError = hierarchyMutationMessage(error instanceof ApiError ? error : null, 'The child could not be removed.');
      announcement = addError;
      await recoverAfterMutationError(error, taskId);
    } finally {
      if (!destroyed && task.id === taskId) parentRemoving = false;
    }
  }
</script>

<section class="hierarchy-panel" aria-labelledby="task-hierarchy-heading" aria-busy={hierarchyLoading}>
  <div class="hierarchy-heading">
    <div>
      <h2 id="task-hierarchy-heading">Hierarchy</h2>
      <p>Organize related work under one project-local parent.</p>
    </div>
    {#if hierarchy?.summary.child_count}
      <span class="hierarchy-progress" aria-label={`${Math.round(hierarchy.summary.completion_percent)} percent of children complete`}>
        {Math.round(hierarchy.summary.completion_percent)}% complete
      </span>
    {/if}
  </div>

  <p class="sr-only" aria-live="polite" aria-atomic="true">{announcement}</p>

  {#if hierarchyLoading && !hierarchy}
    <div class="hierarchy-loading" role="status"><span></span><span>Loading hierarchy…</span></div>
  {:else if hierarchyError && !hierarchy}
    <div class="hierarchy-load-error" role="alert">
      <span>{hierarchyError}</span>
      <button type="button" on:click={() => loadHierarchy(task.id)}>Retry</button>
    </div>
  {:else}
    <HierarchyRelationships
      bind:this={relationships}
      {hierarchy}
      {mutationBusy}
      {parentRemoving}
      {onNavigate}
      {onCreateChild}
      onRemoveParent={removeParent}
      onRemoveChild={removeChild}
    />

    <HierarchyParentSearch
      bind:this={parentSearch}
      bind:addError
      {task}
      {hierarchy}
      {projectTasks}
      {mutationBusy}
      {candidatesLoading}
      onSelect={setParent}
    />

    {#if hierarchy?.summary.child_count}
      <dl class="hierarchy-summary" aria-label="Child rollup">
        <div><dt>Children complete</dt><dd>{hierarchy.summary.completed_child_count}/{hierarchy.summary.child_count}</dd></div>
        <div><dt>Blocked</dt><dd>{hierarchy.summary.blocked_child_count}</dd></div>
        <div><dt>Live work</dt><dd>{hierarchy.summary.live_agent_work_count}</dd></div>
        <div><dt>Action needed</dt><dd>{hierarchy.summary.action_needed_count}</dd></div>
      </dl>
    {/if}
  {/if}
</section>

<style>
  .hierarchy-panel { display: grid; gap: 14px; padding: 15px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface-raised); }
  .hierarchy-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .hierarchy-heading h2 { margin: 0; color: var(--ink); font-size: var(--text-xs); }
  .hierarchy-heading p { margin: 4px 0 0; color: var(--muted); font-size: var(--text-2xs); line-height: 1.45; }
  .hierarchy-progress { flex: 0 0 auto; padding: 4px 7px; border-radius: 6px; color: var(--green); background: var(--green-soft); font-size: var(--text-2xs); font-weight: 600; }
  .hierarchy-loading, .hierarchy-load-error { min-height: 62px; display: flex; align-items: center; justify-content: center; gap: 8px; color: var(--muted); font-size: var(--text-2xs); }
  .hierarchy-loading > span:first-child { width: 13px; height: 13px; border: 2px solid var(--border); border-top-color: var(--purple); border-radius: 50%; animation: hierarchy-spin .7s linear infinite; }
  .hierarchy-load-error { flex-direction: column; color: var(--red); text-align: center; }
  .hierarchy-load-error button { min-height: 36px; padding: 0 12px; border: 1px solid var(--border); border-radius: 7px; color: var(--ink); background: var(--surface); }
  .hierarchy-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px; margin: 0; }
  .hierarchy-summary div { padding: 7px; border: 1px solid var(--border); border-radius: 7px; background: var(--surface); }
  .hierarchy-summary dt { color: var(--muted); font-size: var(--text-2xs); }
  .hierarchy-summary dd { margin: 3px 0 0; color: var(--ink); font-size: var(--text-xs); font-weight: 600; }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; clip-path: inset(50%); }
  @keyframes hierarchy-spin { to { transform: rotate(360deg); } }
  @media (max-width: 520px) { .hierarchy-panel { padding: 13px; } .hierarchy-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (prefers-reduced-motion: reduce) { .hierarchy-loading > span:first-child { animation: none; } }
</style>
