<script lang="ts">
  import { onDestroy } from 'svelte';
  import { api } from '../../api';
  import { ApiError, type GithubAuditJob } from '../../types';
  import { canRetry, coverageParts, jobActive, jobOutcomeHelp, jobStageLabel, jobStages, jobStepState, JOB_POLL_MS } from './githubAudit';

  /** Durable server job progress. Navigation never cancels it; only Cancel does. */
  export let auditId: string;
  /** `null` means this run has no server job: an external-agent run. */
  export let onJob: (job: GithubAuditJob | null) => void = () => undefined;
  export let onFinished: (job: GithubAuditJob) => void = () => undefined;
  export let onRetried: (auditId: string) => void = () => undefined;
  /** TC-316: false parks Cancel and Retry; progress and outcome stay readable. */
  export let actionsAvailable = false;

  let job: GithubAuditJob | null = null;
  let error = '';
  let cancelling = false;
  let retrying = false;
  let loadedId = '';
  let request = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let destroyed = false;

  $: if (auditId && auditId !== loadedId) {
    loadedId = auditId;
    job = null;
    error = '';
    void poll(auditId);
  }

  function schedule(id: string): void {
    clearTimeout(timer);
    timer = setTimeout(() => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') schedule(id);
      else void poll(id);
    }, JOB_POLL_MS);
  }

  function accept(next: GithubAuditJob): void {
    const wasActive = jobActive(job);
    job = next;
    onJob(next);
    if (wasActive && !jobActive(next)) onFinished(next);
  }

  async function poll(id: string): Promise<void> {
    const requestId = ++request;
    try {
      const next = await api.getAuditJob(id);
      if (destroyed || requestId !== request || id !== auditId) return;
      error = '';
      // The first read of an already-finished job still loads its findings once.
      if (!job && next.stage === 'finished') {
        job = next;
        onJob(next);
      } else {
        accept(next);
      }
      if (jobActive(next)) schedule(id);
    } catch (failure) {
      if (destroyed || requestId !== request || id !== auditId) return;
      if (failure instanceof ApiError && failure.status === 404) {
        job = null;
        onJob(null);
        return;
      }
      error = failure instanceof Error ? failure.message : 'Job status could not be loaded.';
      // A transient read failure keeps the last known state and tries again.
      schedule(id);
    }
  }

  async function cancel(): Promise<void> {
    if (!actionsAvailable || !job || cancelling) return;
    cancelling = true;
    error = '';
    try {
      accept(await api.cancelAuditJob(auditId));
    } catch (failure) {
      error = failure instanceof Error ? failure.message : 'The audit could not be cancelled.';
    } finally {
      cancelling = false;
    }
  }

  async function retry(): Promise<void> {
    if (!actionsAvailable || !job || retrying) return;
    retrying = true;
    error = '';
    try {
      const result = await api.retryAuditJob(auditId);
      if (!destroyed) onRetried(result.audit.id || result.job.audit_id);
    } catch (failure) {
      error = failure instanceof Error ? failure.message : 'The audit could not be retried.';
    } finally {
      retrying = false;
    }
  }

  onDestroy(() => {
    destroyed = true;
    request += 1;
    clearTimeout(timer);
  });
</script>

{#if job}
  <section class="gh-job" data-github-audit-job data-stage={job.stage} data-outcome={job.outcome} aria-labelledby="gh-job-heading">
    <div class="gh-job-top">
      <div>
        <span class="eyebrow">Server-run GitHub audit</span>
        <h2 id="gh-job-heading">{jobStageLabel(job)}</h2>
        <p role="status">{jobOutcomeHelp(job)}</p>
      </div>
      {#if actionsAvailable}<div class="gh-job-actions">
        {#if jobActive(job)}<button class="button quiet-button compact-button" type="button" disabled={cancelling} on:click={() => void cancel()}>{#if cancelling}<span class="button-spinner"></span>{/if}Cancel audit</button>{/if}
        {#if canRetry(job)}<button class="button primary compact-button" type="button" disabled={retrying} on:click={() => void retry()}>{#if retrying}<span class="button-spinner"></span>{/if}Retry audit</button>{/if}
      </div>{/if}
    </div>
    {#if jobActive(job)}
      <ol class="gh-job-steps" aria-label="Audit stages">
        {#each jobStages as stage, index}
          <li data-step={jobStepState(job, index)} aria-current={jobStepState(job, index) === 'current' ? 'step' : undefined}><span aria-hidden="true">{jobStepState(job, index) === 'done' ? '✓' : jobStepState(job, index) === 'current' ? '◌' : '·'}</span>{stage.label}</li>
        {/each}
      </ol>
    {/if}
    <p class="gh-job-coverage">{coverageParts(job).join(' · ')}</p>
    {#if job.message}<p class="gh-job-message">{job.message}</p>{/if}
    {#if job.retry_of}<p class="gh-job-meta">Retry of <span class="gh-mono">{job.retry_of}</span></p>{/if}
    {#if error}<p class="gh-job-error" role="alert">{error}</p>{/if}
  </section>
{:else if error}
  <div class="inline-alert error content-alert" role="alert"><span>!</span><span>{error}</span><button class="text-button" type="button" on:click={() => void poll(auditId)}>Retry</button></div>
{/if}

<style>
  .gh-job { display: grid; gap: 10px; margin-bottom: 16px; padding: 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); }
  .gh-job-top { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 12px; }
  .gh-job h2 { margin: 2px 0 4px; font-size: var(--text-lg); }
  .gh-job p { margin: 0; color: var(--ink-soft); font-size: var(--text-sm); line-height: 1.5; }
  .gh-job-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .gh-job-steps { display: flex; flex-wrap: wrap; gap: 6px 16px; margin: 0; padding: 0; list-style: none; font-size: var(--text-xs); }
  .gh-job-steps li { display: inline-flex; align-items: center; gap: 6px; color: var(--muted); }
  .gh-job-steps li[data-step='done'] { color: var(--semantic-green); }
  .gh-job-steps li[data-step='current'] { color: var(--ink); font-weight: 600; }
  .gh-job .gh-job-coverage, .gh-job .gh-job-meta { color: var(--muted); font-size: var(--text-xs); }
  .gh-job .gh-job-message { font-size: var(--text-xs); }
  .gh-job .gh-job-error { color: var(--semantic-red); font-size: var(--text-xs); }
  .gh-mono { font-family: var(--font-mono); }
</style>
