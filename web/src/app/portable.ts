// Project export and import.
import { api, portableImportReportFromError } from '../lib/api';
import type { PortableArchive } from '../lib/types';
import { loadBoard } from './board/load';
import { friendlyError, toast } from './feedback';
import { loadProjects } from './projects';
import { portable } from './state/portable.svelte';
import { workspace } from './state/workspace.svelte';

export async function exportActiveProject() {
  if (!workspace.activeProject || portable.portableBusy) return;
  portable.portableBusy = true;
  try {
    const archive = await api.exportProject(workspace.activeProject.id);
    const blob = new Blob([JSON.stringify(archive, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `helm-${workspace.activeProject.slug}-portable-v${archive.version}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
    toast('success', `Exported ${workspace.activeProject.name}.`);
  } catch (error) {
    toast('error', friendlyError(error, 'The project could not be exported.'));
  } finally {
    portable.portableBusy = false;
  }
}

export async function handlePortableFile(event: Event) {
  const input = event.currentTarget as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file || !workspace.activeProject || portable.portableBusy) return;
  portable.portableBusy = true;
  portable.portablePreviewError = '';
  let archive: PortableArchive | null = null;
  try {
    archive = JSON.parse(await file.text()) as PortableArchive;
    const report = await api.importPortable(archive, { targetProject: workspace.activeProject.id, dryRun: true });
    portable.portablePreview = { archive, fileName: file.name, report };
  } catch (error) {
    const report = portableImportReportFromError(error);
    if (archive && report) {
      // Validation failures still carry the complete structured report in
      // ApiError.details. Keep the review dialog open so users can inspect
      // every issue and remap before choosing another file or cancelling.
      portable.portablePreview = { archive, fileName: file.name, report };
    } else {
      portable.portablePreviewError = friendlyError(error, 'The portable archive could not be previewed.');
      toast('error', portable.portablePreviewError);
    }
  } finally {
    portable.portableBusy = false;
  }
}

export function closePortablePreview() {
  portable.portablePreview = null;
  portable.portablePreviewError = '';
}

export async function confirmPortableImport() {
  if (!portable.portablePreview || !workspace.activeProject || portable.portableBusy) return;
  portable.portableBusy = true;
  try {
    const report = await api.importPortable(portable.portablePreview.archive, { targetProject: workspace.activeProject.id });
    closePortablePreview();
    const created = report.counts.tasks_created || 0;
    toast('success', `Imported ${created} task${created === 1 ? '' : 's'} into ${workspace.activeProject.name}.`);
    await loadProjects();
    await loadBoard();
  } catch (error) {
    const report = portableImportReportFromError(error);
    if (report) {
      portable.portablePreview = { ...portable.portablePreview, report };
    } else {
      toast('error', friendlyError(error, 'The portable archive could not be imported.'));
    }
  } finally {
    portable.portableBusy = false;
  }
}
