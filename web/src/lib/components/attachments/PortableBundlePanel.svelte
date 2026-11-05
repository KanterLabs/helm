<script lang="ts">
  import { onDestroy } from 'svelte';
  import { portableImportReportFromError } from '../../api/client';
  import { key } from '../../api/client';
  import { exportProjectBundle, importProjectBundle } from '../../api/portableBundle';
  import type { PortableImportReport, Project } from '../../types';
  import { requestConfirm, restoreDialogFocus } from '../../../app/dialogs';
  import { portable } from '../../../app/state/portable.svelte';
  import { session } from '../../../app/state/session.svelte';

  export let project: Project;
  let file: File | null = null;
  let picker: HTMLInputElement;
  let report: PortableImportReport | null = null;
  let reviewed = false;
  let busy: '' | 'export' | 'review' | 'import' | 'confirm' = '';
  let error = '';
  let success = '';
  let applyKey = '';
  let alive = true;
  let serial = 0;
  let controller: AbortController | undefined;
  const owner = { actorId: session.user?.id, generation: session.sessionGeneration, projectId: project.id };

  $: counts = report?.counts || {};
  $: tasksToAdd = counts.tasks_created || 0;
  $: commentsToAdd = counts.comments_created || 0;
  $: columnsToAdd = counts.columns_created || 0;
  $: kept = Object.entries(counts).filter(([name]) => name.endsWith('_skipped')).reduce((total, [, count]) => total + count, 0);
  $: canApply = Boolean(file && reviewed && report && !report.errors.length);

  function current(requestId: number) {
    return alive && requestId === serial && project.id === owner.projectId
      && session.sessionGeneration === owner.generation && session.user?.id === owner.actorId;
  }
  function begin(kind: typeof busy) {
    busy = kind;
    portable.portableBusy = true;
    error = '';
    success = '';
    controller = new AbortController();
    return ++serial;
  }
  function finish(requestId: number) {
    if (!current(requestId)) return;
    busy = '';
    portable.portableBusy = false;
    controller = undefined;
  }
  function chooseFile() {
    if (busy || portable.portableBusy) return;
    file = picker.files?.[0] || null;
    report = null;
    reviewed = false;
    error = '';
    success = '';
    applyKey = key();
    if (file && !file.name.toLowerCase().endsWith('.zip')) {
      error = 'Choose a project ZIP exported with images.';
      file = null;
    }
  }
  async function download() {
    if (busy || portable.portableBusy) return;
    const requestId = begin('export');
    try {
      const blob = await exportProjectBundle(owner.projectId, controller?.signal);
      if (!current(requestId)) return;
      const url = URL.createObjectURL(blob);
      try {
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = `helm-${project.slug}-with-images.zip`;
        anchor.click();
      } finally { URL.revokeObjectURL(url); }
      success = 'Project file downloaded.';
    } catch (failure) {
      if (current(requestId)) error = failure instanceof Error ? failure.message : 'The project could not be exported.';
    } finally { finish(requestId); }
  }
  async function review() {
    if (!file || busy || portable.portableBusy) return;
    const selectedFile = file;
    const requestId = begin('review');
    report = null;
    reviewed = false;
    try {
      const result = await importProjectBundle(selectedFile, owner.projectId, true, undefined, controller?.signal);
      if (current(requestId)) { report = result; reviewed = result.dry_run; }
    } catch (failure) {
      if (!current(requestId)) return;
      report = portableImportReportFromError(failure);
      error = failure instanceof Error ? failure.message : 'This project file could not be reviewed. Your selected file is still here.';
    } finally { finish(requestId); }
  }
  async function apply() {
    if (!file || !report || !canApply || busy || portable.portableBusy) return;
    const selectedFile = file;
    const requestId = begin('confirm');
    const confirmed = await requestConfirm({
      title: `Import a copy into ${project.name}?`,
      message: `${selectedFile.name}: add ${tasksToAdd} tasks, ${commentsToAdd} comments and ${columnsToAdd} columns to ${project.name} (${project.key}), including any screenshots in the file. Existing records are kept; conflicting IDs are assigned new IDs.`,
      confirmLabel: 'Import copy', fallbackSelector: '[data-bundle-import]'
    });
    if (!current(requestId)) return;
    if (!confirmed) { finish(requestId); return; }
    busy = 'import';
    try {
      const result = await importProjectBundle(selectedFile, owner.projectId, false, applyKey, controller?.signal);
      if (!current(requestId)) return;
      if (result.errors.length) {
        report = result;
        reviewed = false;
        error = 'The project copy could not be imported. Review the issues below, then review the file again.';
        return;
      }
      success = 'Project copy imported.';
      file = null;
      picker.value = '';
      report = null;
    } catch (failure) {
      if (!current(requestId)) return;
      const failedReport = portableImportReportFromError(failure);
      if (failedReport) report = failedReport;
      error = failure instanceof Error ? failure.message : 'The copy could not be imported. Your file and review are still here for retry.';
    } finally {
      if (current(requestId)) restoreDialogFocus();
      finish(requestId);
    }
  }
  function itemLabel(entity: string) {
    return entity === 'attachment' ? 'Screenshot' : entity.replaceAll('_', ' ');
  }
  onDestroy(() => {
    alive = false;
    serial += 1;
    controller?.abort();
    if (busy) portable.portableBusy = false;
    file = null;
    report = null;
  });
</script>

<section class="project-files" aria-label="Project files">
  <div class="project-files-heading"><h3>Project files</h3><p>Download this project with its screenshots, or review a project ZIP to import a copy here.</p></div>
  <button class="button quiet-button" type="button" disabled={Boolean(busy) || portable.portableBusy} on:click={() => void download()}>{busy === 'export' ? 'Preparing download…' : 'Export project with images'}</button>
  <div class="project-files-import">
    <label>Choose project ZIP<input bind:this={picker} type="file" accept=".zip,application/zip" disabled={Boolean(busy) || portable.portableBusy} on:change={chooseFile} /></label>
    {#if file}<p class="project-file-name">{file.name}</p>{/if}
    <button class="button quiet-button" type="button" disabled={!file || Boolean(busy) || portable.portableBusy} on:click={() => void review()}>{busy === 'review' ? 'Reviewing…' : 'Review import'}</button>
  </div>
  {#if error}<p class="screenshot-error" role="alert">{error}</p>{/if}
  {#if success}<p class="project-files-success" role="status">{success}</p>{/if}
  {#if report}
    <div class="project-copy-review">
      <h4>Review project copy</h4>
      <p>Destination: <strong>{project.name}</strong> <span>({project.key})</span></p>
      <p>This adds a copy of the file’s tasks and included screenshots. Existing records are kept.</p>
      <dl class="project-copy-counts"><div><dt>Tasks to add</dt><dd>{tasksToAdd}</dd></div><div><dt>Comments to add</dt><dd>{commentsToAdd}</dd></div><div><dt>Columns to add</dt><dd>{columnsToAdd}</dd></div><div><dt>Existing records kept</dt><dd>{kept}</dd></div></dl>
      {#if report.errors.length}<div role="alert"><strong>Resolve these issues before importing</strong><ul>{#each report.errors as issue}<li>{itemLabel(issue.entity)}: {issue.message}</li>{/each}</ul></div>{/if}
      {#if report.warnings.length}<div><strong>Review warnings</strong><ul>{#each report.warnings as warning}<li>{warning}</li>{/each}</ul></div>{/if}
      {#if report.remaps.length}<details><summary>{report.remaps.length} ID changes, including any screenshot copies</summary><ul class="project-copy-remaps">{#each report.remaps as remap}<li><strong>{itemLabel(remap.entity)}</strong><span>{remap.source} → {remap.target}</span><small>{remap.reason}</small></li>{/each}</ul></details>{/if}
      <button class="button primary" data-bundle-import type="button" disabled={!canApply || Boolean(busy) || portable.portableBusy} on:click={() => void apply()}>{busy === 'import' ? 'Importing copy…' : 'Import copy into selected project'}</button>
    </div>
  {/if}
</section>

<style>
  .project-files { border: 1px solid var(--border); border-radius: 10px; padding: 16px; margin: 22px 0; display: grid; justify-items: start; gap: 12px; min-width: 0; }
  .project-files-heading h3, .project-copy-review h4 { margin: 0 0 6px; }
  .project-files p { margin: 0; font-size: var(--text-xs); line-height: 1.5; overflow-wrap: anywhere; }
  .project-files-heading p, .project-copy-review p { color: var(--muted); }
  .project-files-import { width: 100%; display: grid; justify-items: start; gap: 8px; }
  .project-files label { display: grid; gap: 8px; max-width: 100%; font-size: var(--text-xs); }
  .project-files input[type=file] { width: 100%; max-width: 100%; min-height: 44px; }
  .project-files button { min-height: 44px; white-space: normal; }
  .project-file-name { max-width: 100%; }
  .project-files-success { color: var(--green); }
  .project-copy-review { display: grid; gap: 12px; width: 100%; min-width: 0; padding-top: 12px; border-top: 1px solid var(--border); font-size: var(--text-xs); }
  .project-copy-review > button { justify-self: start; }
  .project-copy-counts { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin: 0; }
  .project-copy-counts dt { color: var(--muted); }
  .project-copy-counts dd { margin: 3px 0 0; font-weight: 600; font-size: var(--text-lg); }
  .project-copy-review summary { cursor: pointer; min-height: 44px; padding: 12px 0; }
  .project-copy-review ul { padding-left: 20px; margin: 8px 0; overflow-wrap: anywhere; }
  .project-copy-remaps { max-height: 240px; overflow: auto; }
  .project-copy-remaps li { margin-bottom: 10px; }
  .project-copy-remaps span, .project-copy-remaps small { display: block; margin-top: 3px; }
  .project-copy-remaps small { color: var(--muted); }
</style>
