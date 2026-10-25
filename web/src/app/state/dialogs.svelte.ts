// Reactive state: Dialog focus restoration and confirmation prompts.
import type { ConfirmRequest, DialogReturnFocus } from '../types';

class DialogsState {
  dialogReturnFocus = $state.raw<DialogReturnFocus | null>(null);
  dialogFocusRestoreRequest = $state.raw(0);
  confirmRequest = $state.raw<ConfirmRequest | null>(null);
}

export const dialogs = new DialogsState();
