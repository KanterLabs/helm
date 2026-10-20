// Reactive state: Project export and import.
import type { PortableArchive, PortableImportReport } from '../../lib/types';

class PortableState {
  portableFileInput = $state.raw(undefined as unknown as HTMLInputElement);
  portableBusy = $state.raw(false);
  portablePreview = $state.raw<{ archive: PortableArchive; fileName: string; report: PortableImportReport } | null>(null);
  portablePreviewError = $state.raw('');
}

export const portable = new PortableState();
