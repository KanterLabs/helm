// Global keyboard shortcuts.
import { get as readStore } from 'svelte/store';
import { offlineReadOnly } from '../lib/connectivity';
import { isEditableTarget } from '../lib/ui';
import { cancelAdminConfirmation } from './admin';
import { closeBetaSwitcher } from './beta';
import { closeBugModal } from './bugModal';
import { closeBulkModal } from './bulk';
import { closeProjectModal } from './creation';
import { settleConfirm } from './dialogs';
import { closeDrawer } from './drawer/drafts';
import { closeMobileNav, closeNavMenus, toggleSidebarCollapsed } from './nav/sidebar';
import { closeCommandPalette, openCommandPalette } from './palette';
import { closeReleaseModal, closeReleaseReopen } from './releases/edit';
import { closeTokenReveal } from './settings';
import { admin } from './state/admin.svelte';
import { beta } from './state/beta.svelte';
import { board } from './state/board.svelte';
import { bugModal } from './state/bugModal.svelte';
import { bulk } from './state/bulk.svelte';
import { creation } from './state/creation.svelte';
import { dialogs } from './state/dialogs.svelte';
import { drawer } from './state/drawer.svelte';
import { issues } from './state/issues.svelte';
import { nav } from './state/nav.svelte';
import { palette } from './state/palette.svelte';
import { releasePlan } from './state/releasePlan.svelte';
import { settings } from './state/settings.svelte';
import { taskModal } from './state/taskModal.svelte';
import { workspace } from './state/workspace.svelte';
import { closeTaskModal } from './taskModal';

export function handleKeydown(event: KeyboardEvent) {
  if (readStore(offlineReadOnly)) return;
  // A confirmation is the top-most modal. Do not let global shortcuts or a
  // second Escape handler act on the dialog's underlying drawer/view.
  if (dialogs.confirmRequest) {
    if (event.key === 'Escape') {
      event.preventDefault();
      settleConfirm(false);
    }
    return;
  }
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    openCommandPalette();
  } else if (
    event.key === '/'
    && !event.metaKey
    && !event.ctrlKey
    && !event.altKey
    && !isEditableTarget(event.target)
    && !palette.commandOpen
    && !nav.projectSwitcherOpen
    && !beta.betaSwitcherOpen
    && !nav.accountMenuOpen
    && !nav.createMenuOpen
    && !nav.mobileNavOpen
    && !creation.showProjectModal
    && !taskModal.showTaskModal
    && !bugModal.showBugModal
    && !releasePlan.showReleaseModal
    && !releasePlan.releaseReopenTarget
    && !settings.revealedToken
    && !drawer.drawerTask
  ) {
    const searchInput = workspace.view === 'issues' ? issues.issueSearchInput : workspace.view === 'board' ? board.boardSearchInput : workspace.view === 'tickets' ? workspace.ticketsWorkspace?.searchElement() || null : null;
    if (searchInput) {
      event.preventDefault();
      searchInput.focus();
    }
  } else if (
    event.key === '['
    && !event.metaKey
    && !event.ctrlKey
    && !event.altKey
    && !isEditableTarget(event.target)
    && !nav.narrowSidebar
    && !palette.commandOpen
    && !creation.showProjectModal
    && !taskModal.showTaskModal
    && !bugModal.showBugModal
    && !releasePlan.showReleaseModal
    && !bulk.showBulkModal
    && !drawer.drawerTask
  ) {
    event.preventDefault();
    toggleSidebarCollapsed();
  } else if (event.key === 'Escape') {
    if (dialogs.confirmRequest) settleConfirm(false);
    else if (palette.commandOpen) closeCommandPalette();
    else if (nav.accountMenuOpen || nav.createMenuOpen || nav.projectOrderMenuOpen) closeNavMenus();
    else if (nav.mobileNavOpen) closeMobileNav();
    else if (nav.projectSwitcherOpen) nav.projectSwitcherOpen = false;
    else if (beta.betaSwitcherOpen) closeBetaSwitcher();
    else if (admin.adminConfirmation) cancelAdminConfirmation();
    else if (creation.showProjectModal) closeProjectModal();
    else if (taskModal.showTaskModal) closeTaskModal();
    else if (bugModal.showBugModal) closeBugModal();
    else if (releasePlan.showReleaseModal) closeReleaseModal();
    else if (releasePlan.releaseReopenTarget) closeReleaseReopen();
    else if (bulk.showBulkModal) closeBulkModal();
    else if (settings.revealedToken) closeTokenReveal();
    else if (drawer.drawerTask) closeDrawer();
  }
}
