<svelte:options runes={true} />

<script lang="ts">
  // Import preview dialog.
  import { focusTrap } from '../../dialogs';
  import { closePortablePreview, confirmPortableImport } from '../../portable';
  import { portable } from '../../state/portable.svelte';
  import { workspace } from '../../state/workspace.svelte';
</script>

{#if portable.portablePreview}
  <div class="modal-backdrop" role="presentation" on:click={closePortablePreview}></div>
  <div class="modal portable-preview-modal" role="dialog" aria-modal="true" aria-labelledby="portable-preview-title" use:focusTrap>
    <div class="modal-header"><div><span class="eyebrow">Portable archive</span><h2 id="portable-preview-title">Review import</h2></div><button class="icon-button" type="button" aria-label="Close" on:click={closePortablePreview}>×</button></div>
    <p><strong>{portable.portablePreview.fileName}</strong> will be merged into {workspace.activeProject?.name || 'the current project'}.</p>
    <div class="portable-import-summary"><strong>{portable.portablePreview.report.counts.tasks_created || 0}</strong><span>tasks to create</span><strong>{portable.portablePreview.report.remaps.length}</strong><span>reported remaps</span><strong>{portable.portablePreview.report.warnings.length}</strong><span>warnings</span><strong>{portable.portablePreview.report.errors.length}</strong><span>validation failures</span></div>
    {#if portable.portablePreviewError}<div class="inline-alert error" role="alert"><span>!</span><span>{portable.portablePreviewError}</span></div>{/if}
    {#if portable.portablePreview.report.errors.length}<div class="portable-import-errors" role="alert"><strong>Validation failures</strong><ul>{#each portable.portablePreview.report.errors as issue}<li><code>{issue.entity}{issue.id ? ` · ${issue.id}` : ''}{issue.field ? ` · ${issue.field}` : ''}</code><span>{issue.message}</span></li>{/each}</ul></div>{/if}
    {#if portable.portablePreview.report.remaps.length}<div class="portable-import-remaps"><strong>Remappings</strong><ul>{#each portable.portablePreview.report.remaps as remap}<li><code>{remap.entity}{remap.field ? ` · ${remap.field}` : ''}: {remap.source} → {remap.target}</code><span>{remap.reason}</span></li>{/each}</ul></div>{/if}
    {#if portable.portablePreview.report.warnings.length}<div class="portable-import-warnings"><strong>Review warnings</strong><ul>{#each portable.portablePreview.report.warnings as warning}<li>{warning}</li>{/each}</ul></div>{/if}
    <div class="modal-actions"><button class="text-button" type="button" disabled={portable.portableBusy} on:click={closePortablePreview}>Cancel</button><button class="button primary" type="button" disabled={portable.portableBusy || portable.portablePreview.report.errors.length > 0} on:click={() => void confirmPortableImport()}>{#if portable.portableBusy}<span class="button-spinner"></span>{/if}Import archive</button></div>
  </div>
{/if}
