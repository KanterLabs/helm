// Session bootstrap, sign-in and sign-out.
import { API_PREFIX, api, unwrapActor } from '../lib/api';
import { offlineReadOnly } from '../lib/connectivity';
import { readOfflineBoards, setOfflineOwner } from '../lib/offlineBoards';
import { readMigratedStorage } from '../lib/state';
import { ApiError } from '../lib/types';
import { clearBetaState, loadBetaBuilds } from './beta';
import { invalidateBulkRequest } from './bulk';
import { hasPendingCreation, resetCreationSession } from './creation';
import { closeDrawer } from './drawer/drafts';
import { clearAnnouncement, friendlyError, toast } from './feedback';
import { startPolling } from './live/polling';
import { clearSavedBoards, enterOffline } from './offline';
import { loadProjects } from './projects';
import { admin } from './state/admin.svelte';
import { beta } from './state/beta.svelte';
import { board } from './state/board.svelte';
import { bulk } from './state/bulk.svelte';
import { drawer } from './state/drawer.svelte';
import { issues } from './state/issues.svelte';
import { live } from './state/live.svelte';
import { myWork } from './state/myWork.svelte';
import { nav } from './state/nav.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { roadmapData } from './state/roadmapData.svelte';
import { search } from './state/search.svelte';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { taskModal } from './state/taskModal.svelte';
import { timeline } from './state/timeline.svelte';
import { workspace } from './state/workspace.svelte';
import { taskMutations } from './taskSync';

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
  if (session.authenticatedActorId && session.authenticatedActorId !== session.user?.id) resetCreationSession();
  session.authenticatedActorId = session.user?.id || '';

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
    } else if (session.authView === 'login') {
      if (!session.loginEmail.trim() || !session.loginPassword) throw new Error('Enter your email and password.');
      const result = await api.authLogin({ email: session.loginEmail.trim(), password: session.loginPassword });
      session.user = unwrapActor(result);
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
  resetCreationSession();
  // Invalidate reads before awaiting the network logout. Otherwise a poll
  // or list request can still resolve while the logout request is pending,
  // and its response could repopulate the session being cleared below.
  clearAnnouncement();
  session.sessionGeneration += 1;
  session.user = null;
  await clearSavedBoards();
  board.boardMutationRequest += 1;
  drawer.taskActionLoading = '';
  workspace.projectListRequest += 1;
  board.boardRequest += 1;
  roadmapData.roadmapRequest += 1;
  roadmapData.roadmapLiveRequest += 1;
  roadmapData.roadmapActivityRequest += 1;
  drawer.drawerTimelineRequest += 1;
  timeline.boardTimelineRequest += 1;
  taskModal.taskModalColumnsRequest += 1;
  issues.issueRequest += 1;
  if (drawer.drawerTask) closeDrawer(true);
  workspace.activeProjectSlug = '';
  workspace.auditIdFromRoute = '';
  roadmapData.roadmapProjectId = undefined;
  workspace.projects = [];
  board.columns = [];
  board.tasks = [];
  bulk.selectedTaskIds = new Set();
  invalidateBulkRequest();
  bulk.bulkReviewTasks = [];
  bulk.showBulkModal = false;
  bulk.bulkResult = null;
  board.labels = [];
  releasePlan.releases = [];
  releasePlan.releasesByProject = {};
  releasePlan.releaseSelectedId = '';
  releasePlan.releaseQueue = null;
  releasePlan.releaseQueueError = '';
  issues.issueTasks = [];
  issues.issueColumns = [];
  issues.issueMetricsData = null;
  issues.issueMetricsStatus = 'unknown';
  issues.issueMetricsRequest += 1;
  myWork.myWorkTasks = [];
  myWork.myWorkColumnsByProject = {};
  nav.sidebarCounts = null;
  nav.sidebarCountsStatus = 'unknown';
  nav.sidebarCountsRequest += 1;
  search.searchTasks = [];
  search.searchColumnsByProject = {};
  search.searchNextCursor = '';
  search.searchSavedViews = [];
  search.searchViewId = '';
  search.searchError = '';
  roadmapData.roadmap = null;
  roadmapData.roadmapLiveTasks = [];
  roadmapData.roadmapLiveColumnsByProject = {};
  roadmapData.roadmapLiveError = '';
  roadmapData.roadmapActivityTasks = {};
  roadmapData.roadmapActivityError = '';
  timeline.boardTimelineItems = [];
  timeline.boardTimelineNextCursor = '';
  timeline.boardTimelineError = '';
  timeline.boardTimelineProjectId = '';
  roadmapData.roadmapActors = {};
  roadmapData.roadmapActorsLoaded = false;
  roadmapData.roadmapActorsLoading = false;
  live.events = [];
  live.eventsCursor = undefined;
  settings.codexAccount = null;
  settings.codexLogin = null;
  settings.codexStatusLoading = false;
  settings.codexLoading = false;
  settings.codexError = '';
  session.authView = session.authStatus?.mode === 'tailnet' ? 'tailnet' : 'login';
  admin.adminProjects = [];
  admin.adminProjectId = '';
  admin.adminColumns = [];
  admin.adminError = '';
  admin.adminConfirmation = null;
  beta.betaBuildRequest += 1;
  beta.betaSwitchRequest += 1;
  clearBetaState();
  if (live.pollTimer) window.clearInterval(live.pollTimer);
  if (live.pulseTimer) window.clearInterval(live.pulseTimer);
  if (live.livenessRefreshTimer) window.clearInterval(live.livenessRefreshTimer);
  live.pollTimer = undefined;
  live.pulseTimer = undefined;
  live.livenessRefreshTimer = undefined;
  live.pollInFlight = null;
  live.boardLivenessRequest += 1;
  live.myWorkLivenessRequest += 1;
  live.drawerLivenessRequest += 1;
  drawer.drawerTimelineItems = [];
  drawer.drawerTimelineNextCursor = '';
  drawer.drawerTimelineError = '';
  drawer.drawerTimelineTaskId = '';
  live.boardLivenessInFlight = null;
  live.boardBackgroundInFlight = null;
  live.myWorkLivenessInFlight = null;
  live.drawerLivenessInFlight = null;
  board.taskMutationRevision = 0;
  Object.values(taskMutations).forEach((records) => records.clear());
  live.workTransitionSnapshot = new Map();
  try {
    await api.authLogout();
  } catch {
    // Clearing the local session is still the least surprising UI result.
  }
}
