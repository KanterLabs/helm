<script lang="ts">
  import type { PullRequestArtifact } from '../../types';
  import {
    artifactExplanation,
    artifactStateLabel,
    checksSummary,
    relativeTime,
    reviewSummary,
    safeGithubUrl,
    shortSha
  } from './pullRequests';

  export let artifact: PullRequestArtifact;
  export let verifying = false;
  export let unlinking = false;
  export let error = '';
  export let disabled = false;
  export let onRefresh: (artifact: PullRequestArtifact) => void = () => undefined;
  export let onUnlink: (artifact: PullRequestArtifact) => void = () => undefined;

  $: snapshot = artifact.verification === 'verified' ? artifact.snapshot : undefined;
  $: stateLabel = verifying && !snapshot ? 'Verifying' : artifactStateLabel(artifact);
  $: checks = checksSummary(artifact);
  $: prUrl = safeGithubUrl(artifact.url);
  $: checksUrl = prUrl ? `${prUrl.replace(/\/$/, '')}/checks` : '';
  $: problem = error || artifact.error_message || '';
</script>

<li class="pr-row" tabindex="-1" data-pr-row data-artifact-id={artifact.id} aria-busy={verifying}>
  <div class="pr-row-top">
    <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="3.5" r="1.7" /><circle cx="4" cy="12.5" r="1.7" /><circle cx="12" cy="12.5" r="1.7" /><path d="M4 5.2v5.6M12 10.8V6.5a2 2 0 0 0-2-2H7.5M9 2.8 7.3 4.5 9 6.2" /></svg>
    <span class="pr-repo">{artifact.repository}</span>
    <strong>#{artifact.number}</strong>
    <span class="pr-state" data-state={stateLabel.toLowerCase()}>{stateLabel}</span>
  </div>
  {#if snapshot}
    <strong class="pr-title" data-pr-title>{snapshot.title}</strong>
    <div class="pr-chips">
      <span class="pr-chip" data-tone={checks.tone}><span aria-hidden="true">{checks.tone === 'failing' ? '✕' : checks.tone === 'passing' ? '✓' : checks.tone === 'pending' ? '◌' : '?'}</span>{checks.text}</span>
      <span class="pr-chip">{reviewSummary(artifact)}</span>
      {#if snapshot.stale}<span class="pr-chip" data-tone="unknown">Stale</span>{/if}
      {#if snapshot.partial}<span class="pr-chip" data-tone="unknown">Partial — some checks could not be read</span>{/if}
    </div>
  {/if}
  <p class="pr-explain">{artifactExplanation(artifact, verifying && !snapshot)}</p>
  {#if problem}<p class="pr-error" role="status">{problem}</p>{/if}
  {#if snapshot?.checks?.length}
    <details class="pr-checks">
      <summary>{snapshot.checks.length} {snapshot.checks.length === 1 ? 'check' : 'checks'} at <span class="pr-mono">{shortSha(snapshot.head_sha)}</span></summary>
      <ul>
        {#each snapshot.checks as check}
          <li><span>{check.name}</span><span class="pr-check-state">{check.conclusion || check.state || 'unknown'}</span>{#if safeGithubUrl(check.url)}<a href={safeGithubUrl(check.url)} target="_blank" rel="noreferrer">Open ↗</a>{/if}</li>
        {/each}
      </ul>
    </details>
  {/if}
  <div class="pr-row-foot">
    <span class="pr-meta">
      {#if snapshot}{snapshot.stale ? 'Last successful refresh' : 'Verified'} {relativeTime(snapshot.fetched_at)} · head <span class="pr-mono">{shortSha(snapshot.head_sha)}</span>{:else}Not verified{/if}
    </span>
    {#if prUrl}<a class="pr-link" href={prUrl} target="_blank" rel="noreferrer">Open PR ↗</a>{/if}
    {#if prUrl && snapshot}<a class="pr-link" href={checksUrl} target="_blank" rel="noreferrer">Checks ↗</a>{/if}
    <button class="button quiet-button compact-button" type="button" disabled={verifying || disabled} aria-label={`Refresh pull request #${artifact.number}`} on:click={() => onRefresh(artifact)}>{verifying ? 'Refreshing…' : snapshot ? 'Refresh' : 'Retry'}</button>
    <button class="text-button" type="button" disabled={unlinking || disabled} aria-label={`Unlink pull request #${artifact.number}`} on:click={() => onUnlink(artifact)}>{unlinking ? 'Unlinking…' : 'Unlink'}</button>
  </div>
</li>

<style>
  .pr-row { display: grid; gap: 8px; padding: 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .pr-row:focus-visible { outline: 2px solid var(--purple); outline-offset: 2px; }
  .pr-row-top, .pr-chips, .pr-row-foot { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .pr-row-top { color: var(--muted); font-size: var(--text-xs); }
  .pr-repo, .pr-mono { font-family: var(--font-mono); overflow-wrap: anywhere; }
  .pr-row-top strong { color: var(--ink); }
  .pr-state { margin-left: auto; padding: 1px 8px; border: 1px solid var(--border-strong); border-radius: 999px; color: var(--ink-soft); font-size: var(--text-2xs); font-weight: 600; }
  .pr-state[data-state='merged'] { border-color: var(--purple); color: var(--purple); }
  .pr-state[data-state='open'] { border-color: var(--green); color: var(--semantic-green); }
  .pr-state[data-state='unavailable'], .pr-state[data-state='unverified'] { border-style: dashed; }
  .pr-title { color: var(--ink); font-size: var(--text-sm); line-height: 1.4; overflow-wrap: anywhere; }
  .pr-chip { --tone: var(--muted); display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; border-radius: 999px; color: var(--ink-soft); background: color-mix(in srgb, var(--tone) 14%, transparent); font-size: var(--text-xs); font-weight: 500; }
  .pr-chip[data-tone='failing'] { --tone: var(--red); color: var(--semantic-red); }
  .pr-chip[data-tone='passing'] { --tone: var(--green); color: var(--semantic-green); }
  .pr-chip[data-tone='pending'] { --tone: var(--amber); color: var(--semantic-amber); }
  .pr-explain, .pr-error, .pr-meta { margin: 0; font-size: var(--text-xs); line-height: 1.45; }
  .pr-explain { color: var(--ink-soft); }
  .pr-error { color: var(--semantic-red); }
  .pr-meta { flex: 1 1 160px; color: var(--muted); }
  .pr-link { color: var(--purple); font-size: var(--text-xs); font-weight: 500; }
  .pr-checks summary { color: var(--muted); font-size: var(--text-xs); cursor: pointer; }
  .pr-checks ul { display: grid; gap: 4px; margin: 6px 0 0; padding: 0; list-style: none; font-size: var(--text-xs); }
  .pr-checks li { display: flex; flex-wrap: wrap; gap: 8px; }
  .pr-check-state { color: var(--muted); }
</style>
