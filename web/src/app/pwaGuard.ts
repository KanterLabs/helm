// Protects unsaved work during PWA updates and page unloads.
import { getPwaState } from '../lib/pwa';
import { hasCreationDrafts, hasPendingCreation } from './creation';
import { requestConfirm } from './dialogs';
import { session } from './state/session.svelte';

export async function beforePwaUpdate(): Promise<boolean> {
  if (session.updateBlocked || hasPendingCreation()) return false;
  if (session.protectedDrafts || hasCreationDrafts()) {
    const discard = await requestConfirm({ title: 'Update with unsaved changes?', message: 'Your unsaved edits will be discarded when Helm reloads. Keep editing to save them first.', confirmLabel: 'Discard and update', cancelLabel: 'Keep editing' });
    if (!discard || session.updateBlocked || hasPendingCreation()) return false;
  }
  session.pwaReloadApproved = true;
  return true;
}

export function handleBeforeUnload(event: BeforeUnloadEvent) {
  if (session.pwaReloadApproved && getPwaState().updateApplying) return;
  if (!session.protectedDrafts && !session.updateBlocked) return;
  event.preventDefault();
  // Chromium and Firefox require returnValue to be assigned for the native
  // leave-page warning to appear. Browsers intentionally replace this text
  // with their own localized warning.
  event.returnValue = '';
}
