// Reactive state: Beta build switcher.
import type { BetaBuild } from '../../lib/types';
import { betaBranchLabelValue, betaShortShaValue } from '../beta';

class BetaState {
  betaEnabled = $state.raw(false);
  betaCurrentSha = $state.raw('');
  betaBuilds = $state.raw<BetaBuild[]>([]);
  betaBuildsLoading = $state.raw(false);
  betaError = $state.raw('');
  betaSwitcherOpen = $state.raw(false);
  betaConfirmSha = $state.raw('');
  betaSwitchingSha = $state.raw('');
  betaSwitchError = $state.raw('');
  betaSwitchJobId = $state.raw('');
  betaSwitchState = $state.raw('');
  betaSwitchTargetSha = $state.raw('');
  betaSwitchIdempotencyKey = $state.raw('');
  betaBuildRequest = $state.raw(0);
  betaSwitchRequest = $state.raw(0);
  betaSwitcherTrigger = $state.raw<HTMLButtonElement | null>(null);
  betaSwitcherMenu = $state.raw<HTMLDivElement | null>(null);
  betaSwitcherReturnFocus = $state.raw<HTMLElement | null>(null);

  // Derived values
  betaCurrentBuild: BetaBuild | undefined = $derived(beta.betaBuilds.find((build) => build.sha === beta.betaCurrentSha)
    || beta.betaBuilds.find((build) => build.current));
  betaCurrentRef = $derived(beta.betaCurrentBuild?.ref || 'beta');
  betaCurrentSubject = $derived(beta.betaCurrentBuild?.subject || '');
  betaCurrentLabel = $derived(`${betaBranchLabelValue(beta.betaCurrentRef)} · ${betaShortShaValue(beta.betaCurrentSha)}${beta.betaCurrentSubject ? ` — ${beta.betaCurrentSubject}` : ''}`);
  betaConfirmBuild = $derived(beta.betaBuilds.find((build) => build.sha === beta.betaConfirmSha));
}

export const beta = new BetaState();
