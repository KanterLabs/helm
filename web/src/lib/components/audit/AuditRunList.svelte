<script lang="ts">
  import type { Project, AuditRun } from '../../types';
  import { auditStatusLabel, auditFindingCount } from './presentation';
  import type { AuditHelpers } from './helpers';
  import GitHubRepositorySelection from '../github/GitHubRepositorySelection.svelte';
  import GithubAuditScope from './GithubAuditScope.svelte';
  import AuditPausedNotice from './AuditPausedNotice.svelte';
  export let helpers: AuditHelpers;
  export let project: Project;
  export let audits: AuditRun[];
  export let listLoading: boolean;
  export let listError: string;
  export let refresh: () => Promise<void>;
  export let openAudit: (audit: AuditRun) => void;
  /** Server-run audits start from the scope panel; external runs stay listed. */
  export let onStarted: (auditId: string) => void;
  export let onOpenExisting: (auditId: string) => void;
  /** TC-316: false parks start and the audit-only repository editor. */
  export let actionsAvailable = false;

  let scopeOpen = false;
  let reposOpen = false;
  let runButton: HTMLButtonElement;

  function closeScope(): void {
    scopeOpen = false;
    runButton?.focus();
  }
</script>

    <section class="page-heading audit-heading">
      <div>
        <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>{project.key}</span></div>
        <div class="heading-title-row"><span class="audit-heading-icon" aria-hidden="true">◎</span><h1 id="audit-heading">Board audits</h1></div>
        <p>{actionsAvailable ? 'Review the board as it was captured, understand recommendations, and decide deliberately what should change.' : 'Read recorded audits of this board as they were captured.'}</p>
      </div>
      <div class="heading-actions"><button class="button quiet-button" type="button" on:click={refresh} disabled={listLoading}>↻ Refresh</button>{#if actionsAvailable}<button class="button quiet-button" type="button" aria-expanded={reposOpen} aria-controls="audit-github-repositories" on:click={() => { reposOpen = !reposOpen; }}>GitHub repositories</button><button class="button primary" type="button" bind:this={runButton} aria-expanded={scopeOpen} on:click={() => { scopeOpen = !scopeOpen; }}>Run audit</button>{/if}</div>
    </section>
    {#if !actionsAvailable}<AuditPausedNotice />{/if}
    {#if actionsAvailable && reposOpen}<section class="audit-github-repos" id="audit-github-repositories" aria-labelledby="audit-github-repos-heading"><h2 id="audit-github-repos-heading">GitHub repositories</h2><p>Choose the repositories and exact branches audits read for {project.name}.</p><GitHubRepositorySelection projectId={project.id} /></section>{/if}
    {#if actionsAvailable && scopeOpen}<GithubAuditScope {project} {onStarted} {onOpenExisting} onClose={closeScope} />{/if}
    {#if listError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{listError}</span><button class="text-button" type="button" on:click={refresh}>Retry</button></div>{/if}
    {#if listLoading && !audits.length}<div class="audit-loading" aria-label="Loading audits"><div></div><div></div><div></div></div>{:else if !audits.length}<div class="empty-state audit-empty"><div class="empty-icon">◎</div>{#if actionsAvailable}<h2>No board audits yet</h2><p>Run an audit to check this project's tasks against GitHub. Recommendations wait for your review.</p><button class="button primary" type="button" on:click={() => { scopeOpen = true; }}>Run first audit</button>{:else}<h2>No recorded board audits</h2><p>Audits recorded for this project appear here.</p>{/if}</div>{:else}<section class="audit-list" aria-label={`${project.name} board audits`}>
      {#each audits as audit (audit.id)}<button class="audit-run-row" type="button" on:click={() => openAudit(audit)}><span class={`audit-run-icon ${helpers.statusClass(audit.status)}`} aria-hidden="true">{helpers.statusClass(audit.status) === 'failed' ? '!' : helpers.statusClass(audit.status) === 'running' ? '◌' : '✓'}</span><span class="audit-run-main"><span class="audit-run-top"><span class="task-key">{audit.id}</span><span class={`audit-status ${helpers.statusClass(audit.status)}`}>{auditStatusLabel(audit.status)}</span></span><strong>{audit.scope || 'Board audit'}</strong><small>{helpers.formatDate(audit.started_at || audit.created_at)} · {helpers.ageLabel(audit)}</small></span><span class="audit-run-count"><strong>{auditFindingCount(audit)}</strong><small>findings</small></span><span class="row-arrow">→</span></button>{/each}
    </section>{/if}

<style>
  .audit-github-repos { display: grid; gap: 10px; margin-bottom: 18px; padding: 18px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface); }
  .audit-github-repos h2 { margin: 0; font-size: var(--text-md); }
  .audit-github-repos p { margin: 0; color: var(--muted); font-size: var(--text-sm); }
</style>
