<script lang="ts">
  import type { AuditDetail } from '../../types';
  import type { AuditHelpers } from './helpers';
  import { auditStatusLabel } from './presentation';

  export let helpers: AuditHelpers;
  export let selectedAudit: AuditDetail;
  export let selectedAuditId: string;
  export let detailLoading: boolean;
  export let listLoading: boolean;
  export let detailError: string;
  export let refresh: () => Promise<void>;
  export let showList: () => void;
  export let loadAuditDetail: (auditId: string) => Promise<void>;
  /** Server-run audits explain progress in their job panel instead. */
  export let serverRun = false;
  /** Column audits (v2) name themselves; legacy runs keep "Board audit". */
  export let title = 'Board audit';
  /** Column audits report outcome in their results; unread unrelated files are not "Partial". */
  export let statusLabel = '';
</script>

    <section class="page-heading audit-heading">
      <div>
        <div class="breadcrumbs"><button class="text-button audit-back-link" type="button" on:click={showList}>Audits</button><span>/</span><span>{statusLabel || auditStatusLabel(selectedAudit.status)}</span></div>
        <div class="heading-title-row"><span class="audit-heading-icon" aria-hidden="true">◎</span><h1 id="audit-heading">{title}</h1><span class={`audit-status ${statusLabel ? 'complete' : helpers.statusClass(selectedAudit.status)}`}>{statusLabel || auditStatusLabel(selectedAudit.status)}</span></div>
        <p>{title === 'Board audit' ? 'Review captured board recommendations before any task is reconciled.' : 'Check each card\'s column against committed work.'} Opening and refreshing this audit never changes the board.</p>
      </div>
      <div class="heading-actions"><button class="button quiet-button" type="button" on:click={refresh} disabled={detailLoading || listLoading}>↻ Refresh</button><button class="button quiet-button" type="button" on:click={showList}>All audits</button></div>
    </section>

    {#if detailError}<div class="inline-alert error content-alert" role="alert"><span>!</span><span>{detailError}</span><button class="text-button" type="button" on:click={() => loadAuditDetail(selectedAuditId)}>Retry</button></div>{/if}
    {#if !serverRun && !helpers.auditReadyForReview()}<div class="inline-alert content-alert" role="status"><span>…</span><span>{selectedAudit.status === 'failed' ? 'This audit failed. Run a fresh audit before reviewing or applying findings.' : 'An agent is still processing this audit. Review and apply unlock only after it finishes.'}</span></div>{/if}
