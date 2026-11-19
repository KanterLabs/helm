<svelte:options runes={true} />

<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../../lib/api';
  import type { TriageSuggestion } from '../../../lib/api/decisions';
  import { ApiError, type Priority, type Task } from '../../../lib/types';
  import { priorityLabels } from '../../constants';
  import { syncAppliedPriority } from '../../drawer/drafts';
  import { refreshDrawerTask } from '../../drawer/open';
  import { friendlyError } from '../../feedback';
  import { setView } from '../../routing';
  import { drawer } from '../../state/drawer.svelte';
  import { session } from '../../state/session.svelte';
  import { replaceTask } from '../../taskSync';

  let { task }: { task: Task } = $props();
  const lifetime = new AbortController();
  const readinessLabels = {
    ready: 'Ready',
    needs_details: 'Needs details',
    needs_decision: 'Needs decision'
  };
  const priorities: Priority[] = ['urgent', 'high', 'normal', 'low'];
  let connected = $state<boolean | null>(null);
  let suggesting = $state(false);
  let loading = $state(false);
  let suggestion = $state<TriageSuggestion | null>(null);
  let selectedPriority = $state<Priority>('normal');
  let error = $state('');
  let status = $state('');
  const blocked = $derived(drawer.drawerLoading || drawer.drawerSaving || drawer.drawerTaskDraftDirty ||
    drawer.checklistPending || drawer.taskActionLoading === task.id);
  const stale = $derived(suggestion !== null && suggestion.task_version !== task.version);

  onMount(() => {
    void load();
    return () => lifetime.abort();
  });

  async function load() {
    loading = true;
    error = '';
    try {
      connected = (await api.openAIConnection(lifetime.signal)).connected;
    } catch (failure) {
      if (!lifetime.signal.aborted) error = friendlyError(failure, 'Could not load the OpenAI connection.');
    } finally {
      loading = false;
    }
  }

  async function suggest() {
    if (!connected || blocked || suggesting) return;
    suggesting = true;
    error = '';
    status = '';
    suggestion = null;
    try {
      const result = await api.suggestTaskTriage(task.id, task.version, lifetime.signal);
      suggestion = result;
      if (result.priority !== 'insufficient_context') selectedPriority = result.priority;
    } catch (failure) {
      if (lifetime.signal.aborted) return;
      if (failure instanceof ApiError && failure.code === 'openai_not_connected') connected = false;
      else {
        error = friendlyError(failure, 'Could not suggest triage.');
        if (failure instanceof ApiError && failure.code === 'stale_version') await refreshDrawerTask(task.id);
      }
    } finally {
      suggesting = false;
    }
  }

  async function apply() {
    if (!suggestion || suggestion.priority === 'insufficient_context' || stale || blocked || suggesting) return;
    const requestedSession = session.sessionGeneration;
    const id = task.id;
    drawer.drawerSaving = true;
    error = '';
    try {
      const updated = await api.patchTask(id, { priority: selectedPriority }, suggestion.task_version);
      if (session.sessionGeneration !== requestedSession) return;
      replaceTask(updated, true);
      if (drawer.drawerTask?.id === id) syncAppliedPriority(drawer.drawerTask);
      suggestion = null;
      status = 'Priority applied.';
    } catch (failure) {
      if (lifetime.signal.aborted) return;
      error = friendlyError(failure, 'Could not apply priority.');
      if (failure instanceof ApiError && failure.status === 409) await refreshDrawerTask(id);
    } finally {
      if (session.sessionGeneration === requestedSession) drawer.drawerSaving = false;
    }
  }
</script>

<section class="drawer-section" aria-labelledby="triage-suggestion-heading">
  <div class="section-heading-inline"><h2 id="triage-suggestion-heading">Triage suggestion</h2></div>
  {#if error}<div class="inline-alert error" role="alert">{error}</div>{/if}
  {#if connected === false}
    <a href="/settings" onclick={(event) => { event.preventDefault(); void setView('settings'); }}>Connect an OpenAI key</a>
  {:else if connected === null}
    {#if loading}<p role="status">Checking connection…</p>
    {:else}<button class="button quiet-button" type="button" onclick={load}>Retry connection check</button>{/if}
  {:else}
    <button class="button quiet-button" type="button" disabled={blocked || suggesting} onclick={suggest}>
      {suggesting ? 'Suggesting triage…' : 'Suggest triage'}
    </button>
    <p class="optional">{drawer.drawerTaskDraftDirty ? 'Save task changes before requesting a suggestion.' : 'Sends the saved title and description to OpenAI.'}</p>
    {#if suggestion}
      <div aria-live="polite">
        <p>Readiness <strong>{readinessLabels[suggestion.readiness]}</strong></p>
        {#if stale}
          <p>The task changed. Suggest triage again.</p>
        {:else if suggestion.priority === 'insufficient_context'}
          <p>Priority <strong>Insufficient context</strong></p>
        {:else}
          <label>Suggested priority
            <select bind:value={selectedPriority} disabled={blocked}>
              {#each priorities as priority}<option value={priority}>{priorityLabels[priority]}</option>{/each}
            </select>
          </label>
          <div class="form-actions">
            <button class="button primary" type="button" disabled={blocked} onclick={apply}>Apply priority</button>
          </div>
        {/if}
      </div>
    {/if}
    {#if status}<p role="status">{status}</p>{/if}
  {/if}
</section>
