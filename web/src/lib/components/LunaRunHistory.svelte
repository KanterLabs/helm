<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../api';
  import type { LunaRun } from '../types';

  let runs: LunaRun[] = [];
  let loading = true;
  let error = '';
  let expanded = new Set<string>();
  let refreshing = false;

  const featureLabels: Record<LunaRun['feature'], string> = {
    task_draft: 'Task draft',
    project_intelligence: 'Project analysis'
  };

  const outcomeLabels: Record<LunaRun['outcome'], string> = {
    running: 'Running',
    succeeded: 'Succeeded',
    invalid_output: 'Invalid output',
    incomplete: 'Incomplete',
    limit_reached: 'Usage limit',
    timed_out: 'Timed out',
    canceled: 'Canceled',
    unavailable: 'Unavailable'
  };

  const stepLabels: Record<string, string> = {
    requested: 'Request received',
    started: 'Run started',
    thread_started: 'Thread started',
    turn_started: 'Turn started',
    response_started: 'Luna began responding',
    response_completed: 'Luna finished responding',
    response_generated: 'Luna generated a response',
    validating: 'Checking result',
    validation: 'Checking result',
    outcome: 'Outcome recorded',
    completed: 'Run completed',
    failed: 'Run failed'
  };

  async function load(showLoading = false): Promise<void> {
    if (refreshing) return;
    refreshing = true;
    if (showLoading) loading = true;
    try {
      runs = (await api.listLunaRuns(50)).data;
      error = '';
    } catch (caught) {
      error = caught instanceof Error ? caught.message : 'Luna history could not be loaded.';
    } finally {
      loading = false;
      refreshing = false;
    }
  }

  function toggle(id: string): void {
    const next = new Set(expanded);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    expanded = next;
  }

  function timestamp(value: string): string {
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? value : new Intl.DateTimeFormat(undefined, {
      dateStyle: 'medium', timeStyle: 'short'
    }).format(parsed);
  }

  function duration(ms?: number): string {
    if (ms === undefined) return 'In progress';
    if (ms < 1000) return `${ms} ms`;
    return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
  }

  function bytes(value?: number): string {
    if (value === undefined) return '—';
    if (value < 1024) return `${value} B`;
    return `${(value / 1024).toFixed(1)} KB`;
  }

  function stepLabel(kind: string): string {
    return stepLabels[kind] ?? kind.replaceAll('_', ' ');
  }

  function elapsed(started: string, at: string): string {
    const milliseconds = new Date(at).getTime() - new Date(started).getTime();
    if (!Number.isFinite(milliseconds) || milliseconds < 0) return timestamp(at);
    return `+${duration(milliseconds)}`;
  }

  onMount(() => {
    void load(true);
    const interval = window.setInterval(() => {
      if (document.visibilityState === 'visible') void load();
    }, 2000);
    return () => window.clearInterval(interval);
  });
</script>

<section class="luna-history" aria-labelledby="luna-history-heading">
  <div class="luna-history-heading">
    <div>
      <h3 id="luna-history-heading">Recent Luna work</h3>
      <p>Private execution metadata for troubleshooting. Prompts and model responses are not stored.</p>
    </div>
    <button class="icon-button tiny" type="button" aria-label="Refresh Luna history" disabled={refreshing} on:click={() => load(true)}>↻</button>
  </div>

  {#if error}
    <div class="inline-alert error" role="alert"><span>!</span><span>{error}</span><button class="text-button" type="button" on:click={() => load(true)}>Retry</button></div>
  {:else if loading && !runs.length}
    <div class="list-skeleton" aria-label="Loading Luna history"><div></div><div></div></div>
  {:else if !runs.length}
    <div class="luna-history-empty"><span>✦</span><div><strong>No Luna runs yet</strong><p>Task drafts and project analyses will appear here after they run.</p></div></div>
  {:else}
    <div class="luna-run-list">
      {#each runs as run (run.id)}
        <article class="luna-run" class:expanded={expanded.has(run.id)}>
          <button class="luna-run-summary" type="button" aria-expanded={expanded.has(run.id)} on:click={() => toggle(run.id)}>
            <span class="luna-run-mark" aria-hidden="true">✦</span>
            <span class="luna-run-main"><strong>{featureLabels[run.feature]}</strong><small>{run.project_key ? `${run.project_key} · ` : ''}{timestamp(run.started_at)}</small></span>
            <span class={`luna-run-outcome ${run.outcome}`}>{outcomeLabels[run.outcome]}</span>
            <span class="luna-run-duration">{duration(run.duration_ms)}</span>
            <span class="luna-run-chevron" aria-hidden="true">⌄</span>
          </button>
          {#if expanded.has(run.id)}
            <div class="luna-run-detail">
              <div class="luna-debug-heading"><strong>Step by step</strong><span>{run.outcome === 'running' ? 'Updates while Luna runs' : 'Execution trace'}</span></div>
              {#if run.steps?.length}
                <ol class="luna-debug-steps" aria-label="Luna execution steps">
                  {#each run.steps as step (step.sequence)}
                    <li><span class="luna-debug-dot" aria-hidden="true"></span><span>{stepLabel(step.kind)}</span><time datetime={step.at}>{elapsed(run.started_at, step.at)}</time></li>
                  {/each}
                </ol>
              {:else}
                <p class="luna-debug-empty">Detailed steps were not recorded for this run.</p>
              {/if}
              <dl>
                <div><dt>Model</dt><dd>{run.model}</dd></div>
                <div><dt>Effort</dt><dd>{run.effort}</dd></div>
                <div><dt>Output</dt><dd>{bytes(run.output_bytes)}</dd></div>
                <div><dt>Run ID</dt><dd><code>{run.id}</code></dd></div>
                {#if run.thread_id}<div><dt>Thread ID</dt><dd><code>{run.thread_id}</code></dd></div>{/if}
                {#if run.turn_id}<div><dt>Turn ID</dt><dd><code>{run.turn_id}</code></dd></div>{/if}
              </dl>
              {#if run.detail}<div class="luna-run-diagnostic"><strong>Diagnostic</strong><code>{run.detail}</code></div>{/if}
            </div>
          {/if}
        </article>
      {/each}
    </div>
  {/if}
</section>
