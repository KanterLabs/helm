// Reactive state: Bug report dialog.
import type { BugSeverity, Column, Priority } from '../../lib/types';

class BugModalState {
  bugModalColumnsRequest = $state.raw(0);
  showBugModal = $state.raw(false);
  bugModalLoading = $state.raw(false);
  bugModalCreating = $state.raw(false);
  bugModalError = $state.raw('');
  bugModalProjectId = $state.raw('');
  bugModalColumnId = $state.raw('');
  bugModalColumns = $state.raw<Column[]>([]);
  bugModalTitle = $state.raw('');
  bugModalDescription = $state.raw('');
  bugModalActual = $state.raw('');
  bugModalExpected = $state.raw('');
  bugModalReproduction = $state.raw('');
  bugModalEnvironment = $state.raw('');
  bugModalVersion = $state.raw('');
  bugModalLabels = $state.raw('');
  bugModalSeverity = $state.raw<BugSeverity | ''>('');
  bugModalPriority = $state.raw<Priority>('normal');
  bugModalReleaseId = $state.raw('');
}

export const bugModal = new BugModalState();
