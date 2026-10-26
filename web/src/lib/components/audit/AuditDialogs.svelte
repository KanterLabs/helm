<script lang="ts">
  import type { AuditFinding } from '../../types';
  import { focusTrap } from './focus';
  import type { AuditHelpers } from './helpers';
  export let helpers: AuditHelpers;
  export let previewFinding: AuditFinding | null;
  export let applyFinding: AuditFinding | null;
  export let applyError: string;
  export let applying: boolean;
  export let closePreview: () => void;
  export let cancelApply: () => void;
  export let openApplyConfirmation: (finding: AuditFinding) => void;
  export let applyApprovedFinding: () => Promise<void>;
</script>

{#if previewFinding}
  <div class="modal-backdrop audit-modal-backdrop" role="presentation" on:click|self={closePreview}></div>
  <div class="modal audit-preview-modal" role="dialog" aria-modal="true" aria-labelledby="audit-preview-title" tabindex="-1" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">No task changes yet</span><h2 id="audit-preview-title">Preview approved finding</h2></div><button class="icon-button" type="button" aria-label="Close preview" on:click={closePreview}>×</button></div>
    <div class="audit-preview-route"><span>{helpers.sourceLabel(previewFinding)}</span><span aria-hidden="true">→</span><strong>{helpers.destinationLabel(previewFinding)}</strong></div>
    <p class="audit-preview-copy">{helpers.findingTask(previewFinding)?.title || previewFinding.task_id}</p>
    <dl class="audit-preview-details"><div><dt>Current column</dt><dd>{helpers.currentLabel(previewFinding)}</dd></div><div><dt>Captured version</dt><dd>{helpers.findingVersionLabel(previewFinding)}</dd></div><div><dt>Current version</dt><dd>{helpers.currentVersionLabel(previewFinding)}</dd></div><div><dt>Confidence</dt><dd>{helpers.confidenceLabel(previewFinding.confidence)}</dd></div></dl>
    <p class="audit-preview-reason">{previewFinding.reason}</p>
    {#if previewFinding.verdict === 'move_proposed'}<p class="audit-preview-note">Applying will call the guarded task move API. The move will only happen after the separate confirmation below.</p>{:else}<p class="audit-preview-note">This finding is approved for review only; it has no move to apply.</p>{/if}
    <div class="modal-actions"><button class="text-button" type="button" on:click={closePreview}>Close</button>{#if previewFinding.verdict === 'move_proposed'}<button class="button primary" type="button" data-audit-preview-initial-focus disabled={!helpers.canApply(previewFinding)} on:click={() => openApplyConfirmation(previewFinding as AuditFinding)}>Continue to apply</button>{:else}<button class="button primary" type="button" data-audit-preview-initial-focus on:click={closePreview}>Done</button>{/if}</div>
  </div>
{/if}

{#if applyFinding}
  <div class="modal-backdrop audit-modal-backdrop" role="presentation"></div>
  <div class="modal audit-apply-modal" role="alertdialog" aria-modal="true" aria-labelledby="audit-apply-title" tabindex="-1" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">Explicit confirmation</span><h2 id="audit-apply-title">Apply this move?</h2></div><button class="icon-button" type="button" aria-label="Close apply confirmation" on:click={cancelApply}>×</button></div>
    <p>Move <strong>{helpers.taskLabel(applyFinding)}</strong> from <strong>{helpers.currentLabel(applyFinding)}</strong> to <strong>{helpers.destinationLabel(applyFinding)}</strong>?</p>
    <div class="audit-confirm-warning"><span aria-hidden="true">!</span><span>This is the only step that changes the task. The captured version is {helpers.findingVersionLabel(applyFinding)} and the current version is {helpers.currentVersionLabel(applyFinding)}.</span></div>
    {#if applyError}<div class="inline-alert error" role="alert"><span>!</span>{applyError}</div>{/if}
    <div class="modal-actions"><button class="text-button" type="button" on:click={cancelApply}>Cancel</button><button class="button primary" type="button" data-audit-apply-initial-focus disabled={applying || !helpers.canApply(applyFinding)} on:click={() => void applyApprovedFinding()}>{#if applying}<span class="button-spinner"></span>{/if}Apply move</button></div>
  </div>
{/if}
