<script module lang="ts">
  // Re-exported for App.test.ts.
  export * from './app/appHelpers';
</script>

<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { retainSelectedTaskSnapshots } from './app/bulk';
  import { openWorkTask } from './app/drawer/open';
  import { readExploreZoom, syncExploreFromLocation } from './app/explore';
  import { toast } from './app/feedback';
  import { syncIssueViewURL } from './app/issues';
  import { handleKeydown } from './app/keyboard';
  import { loadSidebarCounts } from './app/myWork';
  import { closeMobileNav, handleProjectSwitcherPointerDown } from './app/nav/sidebar';
  import { clearSavedBoards, enterOffline, reconnectOffline } from './app/offline';
  import { beforePwaUpdate, handleBeforeUnload } from './app/pwaGuard';
  import { handlePopState, navigateAudit, setView } from './app/routing';
  import { bootstrap } from './app/session';
  import { board } from './app/state/board.svelte';
  import { bugModal } from './app/state/bugModal.svelte';
  import { bulk } from './app/state/bulk.svelte';
  import { creation } from './app/state/creation.svelte';
  import { drawer } from './app/state/drawer.svelte';
  import { explore } from './app/state/explore.svelte';
  import { issues } from './app/state/issues.svelte';
  import { live } from './app/state/live.svelte';
  import { myWork } from './app/state/myWork.svelte';
  import { nav } from './app/state/nav.svelte';
  import { palette } from './app/state/palette.svelte';
  import { releasePlan } from './app/state/releasePlan.svelte';
  import { session } from './app/state/session.svelte';
  import { settings } from './app/state/settings.svelte';
  import { taskModal } from './app/state/taskModal.svelte';
  import { workspace } from './app/state/workspace.svelte';
  import { replaceTask } from './app/taskSync';
  import { applyTheme, browserPlatform, systemTheme } from './app/theme';
  import BoardView from './app/views/board/BoardView.svelte';
  import TaskDrawer from './app/views/drawer/TaskDrawer.svelte';
  import BugModal from './app/views/overlays/BugModal.svelte';
  import BulkModal from './app/views/overlays/BulkModal.svelte';
  import CommandPalette from './app/views/overlays/CommandPalette.svelte';
  import Overlays from './app/views/overlays/Overlays.svelte';
  import PortablePreview from './app/views/overlays/PortablePreview.svelte';
  import ProjectModal from './app/views/overlays/ProjectModal.svelte';
  import ReleaseModals from './app/views/overlays/ReleaseModals.svelte';
  import TaskModal from './app/views/overlays/TaskModal.svelte';
  import IssuesView from './app/views/pages/IssuesView.svelte';
  import MyWorkView from './app/views/pages/MyWorkView.svelte';
  import ReleasesView from './app/views/pages/ReleasesView.svelte';
  import RoadmapView from './app/views/pages/RoadmapView.svelte';
  import SearchView from './app/views/pages/SearchView.svelte';
  import SettingsView from './app/views/settings/SettingsView.svelte';
  import AuthPage from './app/views/shell/AuthPage.svelte';
  import MobileNav from './app/views/shell/MobileNav.svelte';
  import Sidebar from './app/views/shell/Sidebar.svelte';
  import Topbar from './app/views/shell/Topbar.svelte';
  import AdminMetrics from './lib/components/AdminMetrics.svelte';
  import AuditReview from './lib/components/AuditReview.svelte';
  import HelmMark from './lib/components/HelmMark.svelte';
  import OfflineBoard from './lib/components/OfflineBoard.svelte';
  import PwaStatus from './lib/components/PwaStatus.svelte';
  import TicketsWorkspace from './lib/components/TicketsWorkspace.svelte';
  import AdmissionRequired from './lib/components/membership/AdmissionRequired.svelte';
  import { offlineReadOnly } from './lib/connectivity';
  import { readSidebarCollapsed } from './lib/navigation';
  import { helmStorageKeys, legacyRoadmapStorageKeys, loadRecentProjects, readMigratedStorage } from './lib/state';
  import { platformShortcut } from './lib/ui';

  $effect.pre(() => {
    const ids = bulk.selectedTaskIds;
    const loadedTasks = board.tasks;
    bulk.selectedTasks = untrack(() => retainSelectedTaskSnapshots(ids, loadedTasks));
  });
  $effect.pre(() => { if (workspace.view === 'issues') syncIssueViewURL(issues.issueFilters, issues.issueProjectFilter); });
  $effect.pre(() => { if (!nav.sidebarRail) nav.railTip = null; });
  // Explore mode follows the URL whenever the view changes (route restore,
  // returning from a task, leaving the roadmap).
  $effect.pre(() => { void workspace.view; untrack(syncExploreFromLocation); });
  const exploring = $derived(explore.active && workspace.view === 'roadmap');
  $effect.pre(() => { if (palette.commandIndex >= palette.commandChoices.length) palette.commandIndex = Math.max(0, palette.commandChoices.length - 1); });

  // Keep clean pages eligible for the browser back/forward cache. The
  // listener only exists for the short period in which a drawer draft is
  // actually at risk of being discarded.
  $effect.pre(() => { if (typeof window !== 'undefined') {
    if ((session.protectedDrafts || session.updateBlocked) && !session.beforeUnloadAttached) {
      window.addEventListener('beforeunload', handleBeforeUnload);
      session.beforeUnloadAttached = true;
    } else if (!session.protectedDrafts && !session.updateBlocked && session.beforeUnloadAttached) {
      window.removeEventListener('beforeunload', handleBeforeUnload);
      session.beforeUnloadAttached = false;
    }
  } });

  onMount(() => {
    const storedTheme = readMigratedStorage(localStorage, helmStorageKeys.theme, legacyRoadmapStorageKeys.theme);
    workspace.theme = storedTheme === 'light' || storedTheme === 'dark' ? storedTheme : systemTheme();
    palette.commandShortcut = platformShortcut(browserPlatform());
    applyTheme();
    readExploreZoom(localStorage);
    workspace.recentProjectIds = loadRecentProjects(localStorage);
    const storedProjectOrder = localStorage.getItem(helmStorageKeys.projectOrder);
    workspace.projectOrderMode = storedProjectOrder === 'recent' || storedProjectOrder === 'alphabetical' ? storedProjectOrder : 'smart';
    board.boardOffline = !navigator.onLine;
    const onlineHandler = () => {
      if ($offlineReadOnly) void reconnectOffline();
    };
    const offlineHandler = () => {
      board.boardOffline = true;
      if (!$offlineReadOnly) void enterOffline();
    };
    const clearHandler = () => { board.offlineReadRevision += 1; board.offlineBoards = []; };
    window.addEventListener('online', onlineHandler);
    window.addEventListener('offline', offlineHandler);
    window.addEventListener('helm:network-unavailable', offlineHandler);
    window.addEventListener('helm:offline-cleared', clearHandler);
    window.addEventListener('helm:auth-invalidated', clearHandler);
    const cleanup = () => {
      if (live.pollTimer) window.clearInterval(live.pollTimer);
      if (live.pulseTimer) window.clearInterval(live.pulseTimer);
      if (live.livenessRefreshTimer) window.clearInterval(live.livenessRefreshTimer);
      if (live.announcementTimer) window.clearTimeout(live.announcementTimer);
      if (board.boardFilterTimer) window.clearTimeout(board.boardFilterTimer);
      window.removeEventListener('online', onlineHandler);
      window.removeEventListener('offline', offlineHandler);
      window.removeEventListener('helm:network-unavailable', offlineHandler);
      window.removeEventListener('helm:offline-cleared', clearHandler);
      window.removeEventListener('helm:auth-invalidated', clearHandler);
      if (session.beforeUnloadAttached) {
        window.removeEventListener('beforeunload', handleBeforeUnload);
        session.beforeUnloadAttached = false;
      }
      session.bootstrapController?.abort();
      workspace.projectIntelligenceController?.abort();
    };
    if ($offlineReadOnly || navigator.onLine === false) void enterOffline();
    else void bootstrap();
    const keyHandler = (event: KeyboardEvent) => handleKeydown(event);
    window.addEventListener('keydown', keyHandler);
    window.addEventListener('pointerdown', handleProjectSwitcherPointerDown);
    window.addEventListener('popstate', handlePopState);
    window.addEventListener('popstate', syncExploreFromLocation);
    nav.sidebarCollapsed = readSidebarCollapsed(localStorage);
    const narrowQuery = typeof window.matchMedia === 'function' ? window.matchMedia('(max-width: 820px)') : null;
    const syncNarrow = () => {
      nav.narrowSidebar = Boolean(narrowQuery?.matches);
      if (!window.matchMedia?.('(max-width: 600px)').matches) nav.mobileNavOpen = false;
    };
    syncNarrow();
    narrowQuery?.addEventListener('change', syncNarrow);
    return () => {
      cleanup();
      window.removeEventListener('keydown', keyHandler);
      window.removeEventListener('pointerdown', handleProjectSwitcherPointerDown);
      window.removeEventListener('popstate', handlePopState);
      window.removeEventListener('popstate', syncExploreFromLocation);
      narrowQuery?.removeEventListener('change', syncNarrow);
    };
  });
</script>

<svelte:window />

{#if session.user && !$offlineReadOnly}
  {#if !drawer.drawerTask && !taskModal.showTaskModal && !bugModal.showBugModal && !creation.showProjectModal && !releasePlan.showReleaseModal}<PwaStatus showCacheStatus={false} beforeUpdate={beforePwaUpdate} updateBlocked={session.updateBlocked} />{/if}
{/if}

{#if $offlineReadOnly}
  <OfflineBoard boards={board.offlineBoards} reconnect={() => void reconnectOffline()} reconnecting={board.reconnecting} error={board.reconnectError} clear={() => void clearSavedBoards()} />
{:else if session.user && session.admissionPending}
  <!-- Re-checking admission keeps this screen (with its spinner) instead of the splash. -->
  <AdmissionRequired user={session.user} authStatus={session.authStatus} checking={session.booting} error={session.authError} onCheckAgain={() => void bootstrap()} />
{:else if session.booting}
  <div class="splash" aria-live="polite">
    <HelmMark size={46} decorative className="brand-mark brand-mark-large" />
    <div class="splash-copy">
      <strong>Helm</strong>
      <span>Getting your workspace ready…</span>
    </div>
    <span class="spinner" aria-label="Loading"></span>
  </div>
{:else if !session.user}
  <AuthPage />
{:else}
  <div class="app-shell" class:dark-mode={workspace.theme === 'dark'} class:explore-mode={exploring} style:--explore-zoom={exploring ? explore.zoom : undefined}>
    {#if nav.mobileNavOpen}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="mobile-nav-scrim" aria-hidden="true" on:click={() => closeMobileNav()}></div>
    {/if}
    <Sidebar />
    {#if nav.railTip}<div class="rail-tooltip" role="tooltip" style={`top: ${nav.railTip.top}px; left: ${nav.railTip.left}px`}>{nav.railTip.text}{#if nav.railTip.kbd}<kbd>{nav.railTip.kbd}</kbd>{/if}</div>{/if}

    <div class="main-shell">
      <Topbar />

      <MobileNav />

      <main class="content" class:my-work-live={workspace.view === 'my-work' && myWork.myWorkView === 'live'} tabindex="-1" data-destructive-focus-target>
        {#if workspace.view === 'board' || workspace.view === 'timeline'}
          <BoardView />
        {:else if workspace.view === 'releases'}
          <ReleasesView />
        {:else if workspace.view === 'audits'}
          {#if workspace.activeProject}
            <AuditReview
              project={workspace.activeProject}
              columns={board.columns}
              tasks={board.tasks}
              initialAuditId={workspace.auditIdFromRoute}
              sessionGeneration={session.sessionGeneration}
              refreshToken={workspace.auditRefreshToken}
              onNavigate={navigateAudit}
              onNotice={toast}
              onTaskUpdated={(updated) => replaceTask(updated, true)}
            />
          {:else}
            <div class="empty-state welcome-state"><div class="welcome-orbit"><span>R</span></div><span class="eyebrow">Choose a project</span><h1>Audits live with a board.</h1><p>Select a project first, then review its captured board audits.</p><button class="button primary button-large" type="button" on:click={() => setView('board')}>Browse projects</button></div>
          {/if}
        {:else if workspace.view === 'search'}
          <SearchView />
        {:else if workspace.view === 'issues'}
          <IssuesView />
        {:else if workspace.view === 'my-work'}
          <MyWorkView />
        {:else if workspace.view === 'roadmap'}
          <RoadmapView />
        {:else if workspace.view === 'tickets'}
          <TicketsWorkspace bind:this={workspace.ticketsWorkspace} bind:dirty={workspace.ticketsDirty} user={session.user} projects={workspace.projects} actorNames={Object.fromEntries(settings.agents.map((agent) => [agent.id, agent.name]))} onOpenTask={(task) => openWorkTask(task)} onChanged={() => { void loadSidebarCounts(); }} />
        {:else if workspace.view === 'admin'}
          {#if session.user?.admin}<AdminMetrics />{:else}<div class="empty-state"><div class="empty-icon">◎</div><h3>Administrator access required</h3><p>Ask a workspace administrator for access to metrics.</p></div>{/if}
        {:else}
          <SettingsView />
        {/if}
      </main>
    </div>

    <TaskDrawer />

    <CommandPalette />

    <TaskModal />

    <BugModal />

    <ReleaseModals />

    <BulkModal />

    <PortablePreview />

    <ProjectModal />

    <Overlays />
  </div>
{/if}
