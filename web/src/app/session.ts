// Session bootstrap, sign-in and sign-out.
import { API_PREFIX, api, unwrapActor } from '../lib/api';
import { offlineReadOnly } from '../lib/connectivity';
import { readOfflineBoards, setOfflineOwner } from '../lib/offlineBoards';
import { resetMembershipState } from '../lib/components/membership/membersState.svelte';
import { sameIds } from '../lib/components/membership/access';
import type { Actor } from '../lib/types';
import { resetSessionWorkspace } from './resetSessionWorkspace';
import { readMigratedStorage } from '../lib/state';
import { ApiError } from '../lib/types';
import { clearBetaState, loadBetaBuilds } from './beta';
import { invalidateBulkRequest } from './bulk';
import { hasPendingCreation, resetCreationSession } from './creation';
import { friendlyError, toast } from './feedback';
import { startPolling } from './live/polling';
import { clearSavedBoards, enterOffline } from './offline';
import { loadProjects } from './projects';
import { admin } from './state/admin.svelte';
import { beta } from './state/beta.svelte';
import { board } from './state/board.svelte';
import { bulk } from './state/bulk.svelte';
import { session } from './state/session.svelte';
import { workspace } from './state/workspace.svelte';

export const accessBootstrapKey = 'helm.cloudflare-access-bootstrap';

export const legacyAccessBootstrapKey = 'roadmap.cloudflare-access-bootstrap';

export const bootstrapTimeoutMs = 12_000;

export async function bootstrap() {
  const requestId = ++session.bootstrapRequest;
  session.bootstrapController?.abort();
  const controller = new AbortController();
  session.bootstrapController = controller;
  const timeout = window.setTimeout(() => controller.abort(), bootstrapTimeoutMs);
  session.booting = true;
  session.authError = '';
  session.authBootstrapFailed = false;
  let authStatusLoaded = false;
  try {
    session.authStatus = await api.authStatus(controller.signal);
    if (requestId !== session.bootstrapRequest) return;
    authStatusLoaded = true;
    sessionStorage.removeItem(accessBootstrapKey);
    sessionStorage.removeItem(legacyAccessBootstrapKey);
    if (session.authStatus.setup_required || session.authStatus.needs_setup) {
      await clearSavedBoards();
      session.user = null;
      offlineReadOnly.set(false);
      session.authView = 'setup';
      session.booting = false;
      return;
    }
    if (session.authStatus.authenticated === false) {
      await clearSavedBoards();
      session.user = null;
      offlineReadOnly.set(false);
      // Tailnet identity is established by the private edge and the
      // request-bound assertion. Never offer a local password form in this
      // mode, including after an explicit logout.
      session.authView = session.authStatus.mode === 'tailnet' ? 'tailnet' : 'login';
      session.booting = false;
      return;
    }
    try {
      session.user = session.authStatus.user || session.authStatus.actor || (await api.authMe(controller.signal));
      if (requestId !== session.bootstrapRequest) return;
    } catch (error) {
      // Development's disabled mode intentionally has no session actor.
      if (session.authStatus.mode === 'disabled') {
        session.user = { id: 'local', kind: 'human', name: 'Local user', admin: true };
      } else if (error instanceof ApiError && error.status === 401) {
        await clearSavedBoards();
        session.user = null;
        offlineReadOnly.set(false);
        session.authView = session.authStatus.mode === 'tailnet' ? 'tailnet' : 'login';
        session.booting = false;
        return;
      } else {
        throw error;
      }
    }
    await finishAuthentication();
  } catch (error) {
    if (requestId !== session.bootstrapRequest) return;
    if (navigator.onLine === false) {
      await enterOffline();
      return;
    }
    // Cloudflare Access protects the browser UI and API as distinct
    // applications so agents can use Service Auth on /api/v1/*. A browser
    // therefore needs one top-level API navigation to receive the API-path
    // authorization cookie; an XHR cannot complete that cross-origin login
    // redirect. The status endpoint sends HTML navigations back here after
    // Access has issued the cookie.
    if (
      !authStatusLoaded &&
      error instanceof TypeError &&
      window.location.protocol === 'https:' &&
      readMigratedStorage(sessionStorage, accessBootstrapKey, legacyAccessBootstrapKey) !== window.location.origin
    ) {
      sessionStorage.setItem(accessBootstrapKey, window.location.origin);
      window.location.assign(`${API_PREFIX}/auth/status`);
      return;
    }
    if ((error instanceof TypeError || controller.signal.aborted) && (await readOfflineBoards()).length > 0) {
      await enterOffline();
      return;
    }
    session.authBootstrapFailed = true;
    session.authError = controller.signal.aborted
      ? 'Helm took too long to respond. Check your connection and try again.'
      : friendlyError(error, 'Helm could not connect to the server.');
  } finally {
    window.clearTimeout(timeout);
    if (requestId === session.bootstrapRequest) {
      session.bootstrapController = undefined;
      session.booting = false;
    }
  }
}

export async function finishAuthentication() {
  const actorChanged = session.authenticatedActorId !== (session.user?.id || '');
  if (session.authenticatedActorId && actorChanged) resetCreationSession();
  // Private member/session administration never survives an actor change.
  if (actorChanged) resetMembershipState();
  session.authenticatedActorId = session.user?.id || '';
  if (session.admissionPending) {
    // A verified identity without a reviewed role sees the admission screen.
    // Skip project, board, beta and polling reads the server would refuse.
    session.sessionGeneration += 1;
    await clearSavedBoards();
    offlineReadOnly.set(false);
    session.booting = false;
    return;
  }

  // Every successful authentication starts a fresh client session. Any
  // request left behind by a previous session must fail its generation
  // check even when the browser logs back in as the same actor.
  invalidateBulkRequest();
  bulk.bulkReviewTasks = [];
  bulk.showBulkModal = false;
  bulk.bulkResult = null;
  session.sessionGeneration += 1;
  const requestedSession = session.sessionGeneration;
  if (session.user) await setOfflineOwner(session.user.id);
  if (session.sessionGeneration !== requestedSession || !session.user) return;
  offlineReadOnly.set(false);
  board.boardOffline = false;
  board.offlineReadRevision += 1;
  board.offlineBoards = [];
  // Authentication is enough to reveal the application chrome. Project and
  // board reads can be noticeably slower on a remote self-hosted instance;
  // render their in-context skeleton instead of holding the user on the
  // full-screen bootstrap splash until every request has completed.
  session.booting = false;
  beta.betaBuildRequest += 1;
  beta.betaSwitchRequest += 1;
  clearBetaState();
  // Beta controls are an optional owner-only enhancement. Probe after auth
  // without delaying the normal project/board bootstrap path.
  void loadBetaBuilds({ initial: true });
  await loadProjects();
  if (session.sessionGeneration !== requestedSession || !session.user) return;
  startPolling();
}

export async function submitAuth() {
  session.authSubmitting = true;
  session.authError = '';
  try {
    if (session.authView === 'setup') {
      if (!session.setupName.trim() || !session.setupEmail.trim() || session.setupPassword.length < 12) {
        throw new Error('Enter your name, a valid email, and a password with at least 12 characters.');
      }
      await api.authSetup({ name: session.setupName.trim(), email: session.setupEmail.trim(), password: session.setupPassword });
      // Setup intentionally creates the first administrator without a
      // session. Sign in immediately so first-run onboarding lands in the
      // workspace instead of leaving the UI with an unauthenticated actor.
      const result = await api.authLogin({ email: session.setupEmail.trim(), password: session.setupPassword });
      session.user = unwrapActor(result);
      session.setupPassword = '';
    } else if (session.authView === 'login') {
      if (!session.loginEmail.trim() || !session.loginPassword) throw new Error('Enter your email and password.');
      const result = await api.authLogin({ email: session.loginEmail.trim(), password: session.loginPassword });
      session.user = unwrapActor(result);
      session.loginPassword = '';
    }
    await finishAuthentication();
  } catch (error) {
    session.authError = friendlyError(error, 'We could not sign you in. Check your details and try again.');
  } finally {
    session.authSubmitting = false;
  }
}

export async function logout() {
  if (hasPendingCreation()) {
    toast('info', 'Finish or retry the pending submission before signing out.');
    return;
  }
  await resetSessionWorkspace();
  try {
    await api.authLogout();
  } catch {
    // Clearing the local session is still the least surprising UI result.
  }
}

/**
 * Re-read the signed-in actor after a change to your own membership (role,
 * grants or status). Losing administrator access unmounts the member panel;
 * an ended session returns to sign-in.
 */
export async function refreshSignedInActor(saved?: Actor) {
  const actorId = session.user?.id;
  const requested = session.sessionGeneration;
  // A successful membership response is authoritative even if the status
  // endpoint is unavailable. Do not retain the former administrator while
  // waiting for another read to confirm the already accepted change.
  const next = saved || await api.authMe().catch(() => null);
  if (requested !== session.sessionGeneration || !actorId || next && next.id !== actorId) return;
  if (!next || next.disabled_at) { await endAuthenticatedSession(); return; }
  const previous = session.user!;
  const status = session.authStatus;
  const changed = previous.workspace_role !== next.workspace_role
    || !sameIds(previous.project_ids ?? [], next.project_ids ?? []);
  if (changed) {
    session.booting = true;
    const clearing = resetSessionWorkspace();
    const generation = session.sessionGeneration;
    await clearing;
    if (generation !== session.sessionGeneration) return;
  }
  session.user = next;
  if (status) session.authStatus = { ...status, authenticated: true, user: next, actor: next };
  if (changed) await finishAuthentication();
}

/** A confirmed invalid cookie must clear the UI even if a draft is pending. */
export async function endAuthenticatedSession() {
  await resetSessionWorkspace();
  session.booting = false;
}
