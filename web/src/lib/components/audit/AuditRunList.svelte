<script lang="ts">
  import type { Project, AuditRun } from '../../types';
  import { auditStatusLabel, auditFindingCount } from './presentation';
  import type { AuditHelpers } from './helpers';
  export let helpers: AuditHelpers;
  export let project: Project;
  export let audits: AuditRun[];
  export let listLoading: boolean;
  export let creating: boolean;
  export let listError: string;
  export let refresh: () => Promise<void>;
  export let createAudit: () => Promise<void>;
  export let openAudit: (audit: AuditRun) => void;
</script>

    <section class="page-heading audit-heading">
      <div>
        <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>{project.key}</span></div>
        <div class="heading-title-row"><span class="audit-heading-icon" aria-hidden="true">◎</span><h1 id="audit-heading">Board audits</h1></div>
        <p>Review the board as it was captured, understand recommendations, and decide deliberately what should change.</p>
      </div>
      <div class="heading-actions"><button class="button quiet-button" type="button" on:click={refresh} disabled={listLoading}>↻ Refresh</button><button class="button primary" type="button" on:click={() => void createAudit()} disabled={creating}>{#if creating}<span class="button-spinner"></span>{/if}Run board audit</button></div>
    </section>
    {#if listError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{listError}</span><button class="text-button" type="button" on:click={refresh}>Retry</button></div>{/if}
    {#if listLoading && !audits.length}<div class="audit-loading" aria-label="Loading audits"><div></div><div></div><div></div></div>{:else if !audits.length}<div class="empty-state audit-empty"><div class="empty-icon">◎</div><h2>No board audits yet</h2><p>Run an audit to capture a read-only snapshot of this project. Recommendations will wait for your review.</p><button class="button primary" type="button" on:click={() => void createAudit()} disabled={creating}>Run first audit</button></div>{:else}<section class="audit-list" aria-label={`${project.name} board audits`}>
      {#each audits as audit (audit.id)}<button class="audit-run-row" type="button" on:click={() => openAudit(audit)}><span class={`audit-run-icon ${helpers.statusClass(audit.status)}`} aria-hidden="true">{helpers.statusClass(audit.status) === 'failed' ? '!' : helpers.statusClass(audit.status) === 'running' ? '◌' : '✓'}</span><span class="audit-run-main"><span class="audit-run-top"><span class="task-key">{audit.id}</span><span class={`audit-status ${helpers.statusClass(audit.status)}`}>{auditStatusLabel(audit.status)}</span></span><strong>{audit.scope || 'Board audit'}</strong><small>{helpers.formatDate(audit.started_at || audit.created_at)} · {helpers.ageLabel(audit)}</small></span><span class="audit-run-count"><strong>{auditFindingCount(audit)}</strong><small>findings</small></span><span class="row-arrow">→</span></button>{/each}
    </section>{/if}
