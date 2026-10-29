/**
 * TC-316 audit containment. The browser parks starting, retrying, cancelling,
 * reviewing, correcting and applying audits until TC-311's independent
 * accuracy and human acceptance passes; recorded history stays readable.
 * This is a UI decision only: it changes no API route, contract or permission.
 */
export const auditReviewActionsAvailable = false;

export const auditPausedMessage = 'New audits and finding review are paused while audit accuracy is independently checked. '
  + 'You can still read the runs, findings and evidence you have access to. Recorded findings are unverified history, '
  + 'and opening them never changes the board.';
