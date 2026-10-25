// The beta build switcher: loading retained builds and switching between them.
import { tick } from 'svelte';
import { api } from '../lib/api';
import { ApiError } from '../lib/types';
import type { BetaBuild, BetaBuildsResponse } from '../lib/types';
import { focusableElements, isFocusableVisible } from './dialogs';
import { announce, friendlyError } from './feedback';
import { closeCommandPaletteWithoutFocus } from './palette';
import { beta } from './state/beta.svelte';
import { nav } from './state/nav.svelte';
import { session } from './state/session.svelte';
import type { BetaBuildLoadOptions } from './types';

export function betaShortShaValue(sha: string): string {
  return sha ? sha.slice(0, 7) : 'unknown';
}

export function betaBranchLabelValue(ref: string): string {
  return ref.replace(/^refs\/(heads|tags)\//, '') || 'beta';
}

export function betaOperationKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `beta-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function normalizedBetaBuild(value: unknown): BetaBuild | null {
  if (!value || typeof value !== 'object') return null;
  const build = value as Partial<BetaBuild>;
  if (
    typeof build.sha !== 'string'
    || !build.sha.trim()
    || !/^[0-9a-f]{40}$/.test(build.sha.trim())
    || build.sha.length > 128
    || typeof build.ref !== 'string'
    || !build.ref.trim()
    || build.ref.length > 256
    || /[\u0000-\u001f\u007f]/.test(build.ref)
    || (build.subject !== undefined && (
      typeof build.subject !== 'string'
      || !build.subject.trim()
      || new TextEncoder().encode(build.subject).length > 160
      || /[\u0000-\u001f\u007f]/.test(build.subject)
    ))
    || (build.current !== undefined && typeof build.current !== 'boolean')
  ) return null;
  const subject = typeof build.subject === 'string' ? build.subject.trim() : '';
  return {
    sha: build.sha.trim(),
    ref: build.ref.trim(),
    ...(subject ? { subject } : {}),
    current: build.current
  };
}

export function normalizedBetaBuildsResponse(value: unknown): BetaBuildsResponse | null {
  if (!value || typeof value !== 'object') return null;
  const response = value as Partial<BetaBuildsResponse>;
  if (typeof response.enabled !== 'boolean' || !Array.isArray(response.builds)) return null;
  const builds = response.builds.map(normalizedBetaBuild).filter((build): build is BetaBuild => Boolean(build));
  const rawCurrentSha = typeof response.current_sha === 'string' ? response.current_sha.trim() : '';
  const currentSha = /^[0-9a-f]{40}$/.test(rawCurrentSha)
    ? rawCurrentSha
    : builds.find((build) => build.current)?.sha || '';
  return {
    enabled: response.enabled,
    current_sha: currentSha || builds.find((build) => build.current)?.sha || '',
    builds
  };
}

export function clearBetaState() {
  beta.betaEnabled = false;
  beta.betaCurrentSha = '';
  beta.betaBuilds = [];
  beta.betaError = '';
  beta.betaSwitcherOpen = false;
  beta.betaConfirmSha = '';
  beta.betaSwitchingSha = '';
  beta.betaSwitchError = '';
  beta.betaSwitchJobId = '';
  beta.betaSwitchState = '';
  beta.betaSwitchTargetSha = '';
  beta.betaSwitchIdempotencyKey = '';
}

export async function loadBetaBuilds(options: BetaBuildLoadOptions = {}): Promise<boolean> {
  const requestId = ++beta.betaBuildRequest;
  const requestedSession = session.sessionGeneration;
  if (!options.silent) beta.betaBuildsLoading = true;
  try {
    const payload = normalizedBetaBuildsResponse(await api.getBetaBuilds());
    if (requestId !== beta.betaBuildRequest || requestedSession !== session.sessionGeneration || !session.user) return false;
    if (!payload || !payload.enabled) {
      if (!options.preserveEnabled) clearBetaState();
      return false;
    }
    beta.betaEnabled = true;
    beta.betaCurrentSha = payload.current_sha;
    beta.betaBuilds = payload.builds;
    beta.betaError = '';
    return true;
  } catch (error) {
    if (requestId !== beta.betaBuildRequest || requestedSession !== session.sessionGeneration || !session.user) return false;
    // The probe is intentionally optional: an unavailable/forbidden beta
    // endpoint must not make the main workspace look offline. Once enabled,
    // keep the button visible and show a localized retry affordance.
    if (!options.initial && !options.silent && beta.betaEnabled) {
      beta.betaError = friendlyError(error, 'Beta builds could not be loaded.');
    } else if (!beta.betaEnabled || options.initial) {
      clearBetaState();
    }
    return false;
  } finally {
    if (requestId === beta.betaBuildRequest && !options.silent) beta.betaBuildsLoading = false;
  }
}

export function rememberBetaSwitcherFocus() {
  const active = typeof document !== 'undefined'
    && document.activeElement instanceof HTMLElement
    && document.activeElement !== document.body
    && isFocusableVisible(document.activeElement)
    ? document.activeElement
    : null;
  beta.betaSwitcherReturnFocus = active;
}

export function restoreBetaSwitcherFocus() {
  const target = beta.betaSwitcherReturnFocus || beta.betaSwitcherTrigger;
  beta.betaSwitcherReturnFocus = null;
  void tick().then(() => {
    // Pointer activation moves focus to the clicked outside element after
    // pointerdown. Defer one task so the restoration wins that handoff as
    // well as the Escape-key path.
    window.setTimeout(() => {
      if (target && isFocusableVisible(target)) target.focus();
    }, 0);
  });
}

export function openBetaSwitcher() {
  if (!beta.betaEnabled || beta.betaSwitchingSha) return;
  rememberBetaSwitcherFocus();
  beta.betaSwitcherOpen = true;
  nav.projectSwitcherOpen = false;
  closeCommandPaletteWithoutFocus();
  void tick().then(() => {
    const first = beta.betaSwitcherMenu?.querySelector<HTMLElement>('[role="menuitem"]');
    first?.focus();
  });
}

export function closeBetaSwitcher(restoreFocus = true) {
  if (!beta.betaSwitcherOpen) return;
  beta.betaSwitcherOpen = false;
  if (restoreFocus) restoreBetaSwitcherFocus();
}

export function toggleBetaSwitcher() {
  if (beta.betaSwitcherOpen) closeBetaSwitcher();
  else openBetaSwitcher();
}

export function betaMenuKeydown(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
  const items = beta.betaSwitcherMenu ? focusableElements(beta.betaSwitcherMenu).filter((element) => element.getAttribute('role') === 'menuitem') : [];
  if (!items.length) return;
  const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const currentIndex = active ? items.indexOf(active) : -1;
  const nextIndex = event.key === 'Home'
    ? 0
    : event.key === 'End'
      ? items.length - 1
      : (currentIndex + (event.key === 'ArrowUp' ? -1 : 1) + items.length) % items.length;
  event.preventDefault();
  items[nextIndex].focus();
}

export function selectBetaBuild(build: BetaBuild) {
  if (!beta.betaEnabled || beta.betaSwitchingSha) return;
  if (build.sha === beta.betaCurrentSha) {
    beta.betaConfirmSha = '';
    beta.betaSwitchError = '';
    return;
  }
  if (beta.betaSwitchTargetSha !== build.sha) {
    beta.betaSwitchTargetSha = build.sha;
    beta.betaSwitchIdempotencyKey = betaOperationKey();
  }
  beta.betaSwitchError = '';
  beta.betaConfirmSha = build.sha;
  void tick().then(() => beta.betaSwitcherMenu?.querySelector<HTMLElement>('[data-beta-confirm]')?.focus());
}

export function cancelBetaBuildConfirmation() {
  const targetSha = beta.betaConfirmSha;
  beta.betaConfirmSha = '';
  beta.betaSwitchError = '';
  void tick().then(() => {
    const target = targetSha
      ? beta.betaSwitcherMenu?.querySelector<HTMLElement>(`[data-beta-build="${targetSha}"]`)
      : null;
    (target || beta.betaSwitcherTrigger)?.focus();
  });
}

export function betaSwitchStateLabel(state: string): string {
  const value = state.toLowerCase();
  return value.includes('restart') || value === 'restarting' || value === 'running' ? 'Restarting beta…' : 'Switching…';
}

export function betaSwitchFailed(state: string): boolean {
  return ['failed', 'error', 'cancelled', 'canceled', 'rejected'].includes(state.toLowerCase());
}

export function betaSwitchFinished(state: string): boolean {
  return ['succeeded', 'success', 'completed', 'complete', 'done'].includes(state.toLowerCase());
}

export function betaSwitchDelay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

export async function waitForBetaSwitch(jobId: string, targetSha: string, requestId: number): Promise<boolean> {
  // A rollback can include health checks and service replacement that take
  // longer than a short request timeout. Keep polling bounded, but allow a
  // full minute for the authoritative build pointer to settle.
  const maxAttempts = 120;
  for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
    if (requestId !== beta.betaSwitchRequest || !session.user) return false;
    if (attempt > 0) await betaSwitchDelay(500);
    try {
      const job = await api.getBetaSwitchJob(jobId);
      if (requestId !== beta.betaSwitchRequest) return false;
      beta.betaSwitchState = job.state || beta.betaSwitchState;
      if (betaSwitchFailed(job.state)) {
        throw new Error(job.error || job.message || 'The beta restart failed.');
      }
    } catch (error) {
      // A restarting beta can close the connection before its job endpoint
      // answers. Continue polling the authoritative builds endpoint; only a
      // structured failure response stops the bounded retry loop.
      if (error instanceof ApiError || (error instanceof Error && !/fetch|network|load failed/i.test(error.message))) {
        if (error instanceof ApiError && (error.status === 404 || error.status === 502 || error.status === 503)) {
          // The job may briefly disappear while the process is replaced.
        } else if (error instanceof ApiError && error.status !== 0) {
          throw error;
        } else if (!(error instanceof ApiError)) {
          throw error;
        }
      }
    }
    const loaded = await loadBetaBuilds({ silent: true, preserveEnabled: true });
    if (loaded && beta.betaCurrentSha === targetSha) return true;
    if (betaSwitchFinished(beta.betaSwitchState) && beta.betaCurrentSha === targetSha) return true;
  }
  return false;
}

export async function switchBetaBuild() {
  const targetSha = beta.betaConfirmSha;
  if (!targetSha || beta.betaSwitchingSha || targetSha === beta.betaCurrentSha) return;
  if (beta.betaSwitchTargetSha !== targetSha || !beta.betaSwitchIdempotencyKey) {
    beta.betaSwitchTargetSha = targetSha;
    beta.betaSwitchIdempotencyKey = betaOperationKey();
  }
  const requestId = ++beta.betaSwitchRequest;
  const idempotencyKey = beta.betaSwitchIdempotencyKey;
  beta.betaSwitchingSha = targetSha;
  beta.betaSwitchState = 'switching';
  beta.betaSwitchError = '';
  announce(`Switching beta to ${betaShortShaValue(targetSha)}.`);
  try {
    const accepted = await api.switchBetaBuild(targetSha, idempotencyKey);
    if (requestId !== beta.betaSwitchRequest) return;
    beta.betaSwitchJobId = accepted.id;
    beta.betaSwitchState = accepted.state || 'switching';
    const complete = await waitForBetaSwitch(accepted.id, targetSha, requestId);
    if (!complete) throw new Error('Beta did not restart on the selected build yet.');
    beta.betaSwitchingSha = '';
    beta.betaSwitchJobId = '';
    beta.betaSwitchState = '';
    beta.betaSwitchError = '';
    beta.betaConfirmSha = '';
    beta.betaSwitchTargetSha = '';
    beta.betaSwitchIdempotencyKey = '';
    closeBetaSwitcher();
    announce(`Beta restarted on ${betaShortShaValue(targetSha)}.`);
  } catch (error) {
    if (requestId !== beta.betaSwitchRequest) return;
    beta.betaSwitchingSha = '';
    beta.betaSwitchJobId = '';
    beta.betaSwitchState = '';
    beta.betaSwitchError = friendlyError(error, 'Beta could not switch builds.');
    announce('Beta build switch failed.');
    void tick().then(() => {
      const retry = beta.betaSwitcherOpen ? beta.betaSwitcherMenu?.querySelector<HTMLElement>('[data-beta-retry]') : null;
      (retry || beta.betaSwitcherTrigger)?.focus();
    });
  }
}
