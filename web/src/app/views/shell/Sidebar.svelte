<svelte:options runes={true} />

<script lang="ts">
  // Primary sidebar: workspace views, project tree, beta switcher and account menu.
  import HelmMark from '../../../lib/components/HelmMark.svelte';
  import NavIcon from '../../../lib/components/NavIcon.svelte';
  import { isProjectNavItemActive, projectNavItems, sidebarProjectLimit } from '../../../lib/navigation';
  import { projectInitials } from '../../../lib/state';
  import type { Project } from '../../../lib/types';
  import { betaBranchLabelValue, betaMenuKeydown, betaShortShaValue, betaSwitchStateLabel, cancelBetaBuildConfirmation, loadBetaBuilds, selectBetaBuild, switchBetaBuild, toggleBetaSwitcher } from '../../beta';
  import { openProjectModal } from '../../creation';
  import { closeMobileNav, closeNavMenus, navMenuKeydown, navTo, openProjectNavItem, projectOrderOptions, showRailTip, sidebarProjectClick, toggleNavMenu, toggleSidebarCollapsed } from '../../nav/sidebar';
  import { openCommandPalette } from '../../palette';
  import { loadProjects, projectAttentionLabel, setProjectOrderMode } from '../../projects';
  import { setView } from '../../routing';
  import { logout } from '../../session';
  import { beta } from '../../state/beta.svelte';
  import { nav } from '../../state/nav.svelte';
  import { palette } from '../../state/palette.svelte';
  import { roadmapData } from '../../state/roadmapData.svelte';
  import { session } from '../../state/session.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { setTheme } from '../../theme';

  // Rendered only while session.user is set (guarded by the parent).
  const signedInUser = $derived(session.user!);
</script>

<!-- svelte-ignore a11y_mouse_events_have_key_events -->
<nav bind:this={nav.sidebarElement} id="primary-sidebar" class="sidebar" class:rail={nav.sidebarRail} class:mobile-open={nav.mobileNavOpen} aria-label="Primary navigation" on:mouseover={showRailTip} on:focusin={showRailTip} on:mouseleave={() => nav.railTip = null} on:focusout={() => nav.railTip = null}>
  <div class="sidebar-top">
    <button class="brand-lockup" type="button" aria-label="Go to current project" data-nav-tip="Go to current project" on:click={() => workspace.activeProject && navTo(() => setView('board'))}>
      <HelmMark size={28} decorative className="brand-mark" />
      <span><strong>Helm</strong></span>
    </button>
    <button class="icon-button tiny sidebar-collapse" type="button" aria-label={nav.sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'} aria-keyshortcuts="[" data-nav-tip={nav.sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'} data-nav-kbd="[" on:click={toggleSidebarCollapsed}><NavIcon name="panel" /></button>
    <button class="icon-button tiny mobile-nav-close" type="button" aria-label="Close navigation" on:click={() => closeMobileNav()}>×</button>
  </div>
  <button class="command-trigger sidebar-search" type="button" aria-label="Search anything" data-command-trigger data-nav-tip="Search anything" data-nav-kbd={palette.commandShortcut} on:click={() => { closeMobileNav(false); openCommandPalette(); }}><NavIcon name="search" /><span class="command-trigger-label">Search or jump to…</span><kbd data-command-shortcut>{palette.commandShortcut}</kbd></button>

  <div class="sidebar-scroll">
    <nav class="nav-links" aria-label="Workspace views">
      <button class:active={workspace.view === 'my-work'} type="button" aria-label="My work" aria-current={workspace.view === 'my-work' ? 'page' : undefined} data-nav-tip="My work" on:click={() => navTo(() => setView('my-work'))}><span class="nav-icon"><NavIcon name="my-work" /></span><span class="nav-label">My work</span>{#if nav.sidebarCountsStatus === 'known' && nav.sidebarCounts}<span class="nav-count" class:zero={!nav.sidebarCounts.my_work}>{nav.sidebarCounts.my_work}</span>{/if}</button>
      <button class:active={workspace.view === 'issues'} type="button" aria-label="Issues" aria-current={workspace.view === 'issues' ? 'page' : undefined} data-nav-tip="Issues" on:click={() => navTo(() => setView('issues'))}><span class="nav-icon"><NavIcon name="issues" /></span><span class="nav-label">Issues</span>{#if nav.sidebarCountsStatus === 'known' && nav.sidebarCounts}<span class="nav-count" class:zero={!nav.sidebarCounts.issues}>{nav.sidebarCounts.issues}</span>{/if}</button>
      <button class:active={workspace.view === 'tickets'} type="button" aria-label="Tickets" aria-current={workspace.view === 'tickets' ? 'page' : undefined} data-nav-tip="Tickets" on:click={() => navTo(() => setView('tickets'))}><span class="nav-icon"><NavIcon name="tickets" /></span><span class="nav-label">Tickets</span></button>
      <button class:active={workspace.view === 'roadmap' && !roadmapData.roadmapProjectId} type="button" aria-label="Roadmap" aria-current={workspace.view === 'roadmap' && !roadmapData.roadmapProjectId ? 'page' : undefined} data-nav-tip="Roadmap" on:click={() => navTo(() => setView('roadmap'))}><span class="nav-icon"><NavIcon name="roadmap" /></span><span class="nav-label">Roadmap</span></button>
    </nav>

    {#snippet projectRow(project: Project)}
      {@const isActive = workspace.activeProjectSlug === project.slug}
      {@const expanded = isActive && !nav.sidebarRail && !nav.projectTreeCollapsed}
      {@const insight = workspace.projectOrderMode === 'smart' ? workspace.projectIntelligenceById.get(project.id) : undefined}
      <div class="project-tree">
        <button class="project-link" class:active={isActive && nav.projectScopedView} class:current={isActive} type="button" aria-label={project.name} aria-expanded={isActive && !nav.sidebarRail ? expanded : undefined} aria-controls={expanded ? 'active-project-views' : undefined} data-nav-tip={project.name} on:click={() => sidebarProjectClick(project)}>
          <span class="project-dot" style={`--project-color: ${project.color || '#6d5efc'}`}>{projectInitials(project)}</span><span class="project-link-name">{project.name}</span>{#if project.favorite}<span class="favorite-star" aria-hidden="true">★</span>{:else if insight}<span class={`attention-dot ${insight.attention}`} title={insight.summary} aria-label={projectAttentionLabel(insight)}></span>{/if}{#if isActive && !nav.sidebarRail}<span class="project-chevron" class:open={expanded} aria-hidden="true"><NavIcon name="chevron" size={12} /></span>{/if}
        </button>
        {#if expanded}
          <div id="active-project-views" class="project-views" role="group" aria-label={`${project.name} views`}>
            {#each projectNavItems as item (item.view)}
              {@const current = isProjectNavItemActive(item.view, workspace.view, Boolean(roadmapData.roadmapProjectId))}
              <button class:active={current} type="button" aria-current={current ? 'page' : undefined} on:click={() => openProjectNavItem(item.view)}><NavIcon name={item.icon} size={15} /><span>{item.label}</span></button>
            {/each}
          </div>
        {/if}
      </div>
    {/snippet}

    <div class="project-nav">
      <div class="section-label">
        <span>Projects</span>
        <span class="section-actions">
          <button bind:this={nav.projectOrderTrigger} class="icon-button tiny" type="button" aria-label="Project order" aria-haspopup="menu" aria-expanded={nav.projectOrderMenuOpen} on:click={() => toggleNavMenu('order')}><NavIcon name="more" size={14} /></button>
          <button class="icon-button tiny new-project-button" type="button" aria-label="New project" data-project-modal-trigger data-nav-tip="New project" on:click={() => { closeMobileNav(false); openProjectModal(); }}><NavIcon name="plus" size={14} /></button>
        </span>
        {#if nav.projectOrderMenuOpen}
          <div bind:this={nav.projectOrderMenuElement} class="nav-menu project-order-menu" role="menu" aria-label="Project order" tabindex="-1" on:keydown={navMenuKeydown}>
            {#each projectOrderOptions as option (option.mode)}
              <button type="button" role="menuitemradio" aria-checked={workspace.projectOrderMode === option.mode} on:click={() => { setProjectOrderMode(option.mode); closeNavMenus(); }}><span>{option.label}</span>{#if workspace.projectOrderMode === option.mode}<span class="nav-menu-check" aria-hidden="true">✓</span>{/if}</button>
            {/each}
          </div>
        {/if}
      </div>
      {#if workspace.projectsLoading}
        <div class="nav-skeleton"></div><div class="nav-skeleton short"></div>
      {:else if workspace.projectsError}
        <button class="nav-error" type="button" on:click={loadProjects}>Couldn’t load projects · Retry</button>
      {:else if !workspace.navProjects.length}
        <div class="nav-empty">No projects yet</div>
      {:else}
        {#if workspace.favoriteProjects.length}
          <div class="project-subsection"><span class="subsection-label">Pinned</span>
            {#each workspace.favoriteProjects as project (project.id)}{@render projectRow(project)}{/each}
          </div>
        {/if}
        {#if nav.projectWindow.visible.length || nav.projectWindow.hiddenCount}
          <div class="project-subsection all-projects"><span class="subsection-label">{workspace.projectOrderMode === 'smart' ? 'Smart order' : workspace.projectOrderMode === 'recent' ? 'Recent' : 'Alphabetical'}{#if workspace.projectOrderMode === 'smart' && workspace.projectIntelligence}<span class={`intelligence-source ${workspace.projectIntelligence.source}`}>{workspace.projectIntelligence.source === 'luna' ? 'Luna' : 'Metrics'}</span>{/if}</span>
            {#each nav.projectWindow.visible as project (project.id)}{@render projectRow(project)}{/each}
            {#if nav.projectWindow.hiddenCount}
              <button class="project-more" type="button" on:click={() => nav.showAllProjects = true}>Show {nav.projectWindow.hiddenCount} more</button>
            {:else if nav.showAllProjects && nav.projectWindow.visible.length > sidebarProjectLimit}
              <button class="project-more" type="button" on:click={() => nav.showAllProjects = false}>Show fewer</button>
            {/if}
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <div class="sidebar-bottom">
    {#if beta.betaEnabled}
      <div class="beta-switcher" data-beta-switcher>
        <button
          bind:this={beta.betaSwitcherTrigger}
          class="beta-switcher-trigger"
          type="button"
          aria-label={`Beta build ${beta.betaCurrentLabel}`}
          aria-expanded={beta.betaSwitcherOpen}
          aria-controls="beta-build-menu"
          aria-haspopup="menu"
          aria-busy={Boolean(beta.betaSwitchingSha)}
          title={`${beta.betaCurrentSubject ? `${beta.betaCurrentSubject}\n` : ''}Full SHA: ${beta.betaCurrentSha || 'unknown'}`}
          on:click={toggleBetaSwitcher}
        >
          <span class="beta-switcher-dot" aria-hidden="true"></span>
          <span class="beta-switcher-label"><span>Beta</span><span class="beta-switcher-separator" aria-hidden="true"> · </span><span class="beta-switcher-branch">{betaBranchLabelValue(beta.betaCurrentRef)}</span><span class="beta-switcher-separator" aria-hidden="true"> · </span><span>{betaShortShaValue(beta.betaCurrentSha)}</span>{#if beta.betaCurrentSubject}<span class="beta-switcher-subject-separator" aria-hidden="true"> — </span><span class="beta-switcher-subject">{beta.betaCurrentSubject}</span>{/if}</span>
          <span class="picker-chevron" aria-hidden="true">⌄</span>
        </button>
        {#if beta.betaSwitcherOpen}
          <div
            bind:this={beta.betaSwitcherMenu}
            id="beta-build-menu"
            class="beta-build-menu"
            role={beta.betaConfirmSha || beta.betaSwitchingSha ? undefined : 'menu'}
            tabindex="-1"
            aria-label="Retained beta builds"
            on:keydown={betaMenuKeydown}
          >
            {#if beta.betaSwitchingSha}
              <div class="beta-switch-status" role="status" aria-live="polite" aria-busy="true">
                <span class="spinner" aria-hidden="true"></span>
                <span>{betaSwitchStateLabel(beta.betaSwitchState)}</span>
              </div>
              {#if beta.betaSwitchError}
                <div class="beta-menu-error" role="alert"><span>{beta.betaSwitchError}</span><button class="text-button" type="button" data-beta-retry on:click={() => void switchBetaBuild()}>Retry</button></div>
              {/if}
            {:else if beta.betaConfirmSha}
              <div class="beta-confirmation" role="group" aria-label="Confirm beta build switch">
                <p>Switch beta to <strong>{beta.betaConfirmBuild ? betaBranchLabelValue(beta.betaConfirmBuild.ref) : 'selected build'}</strong> · <code title={beta.betaConfirmSha}>{betaShortShaValue(beta.betaConfirmSha)}</code>?</p>
                {#if beta.betaConfirmBuild?.subject}<p class="beta-confirm-subject">{beta.betaConfirmBuild.subject}</p>{/if}
                <div class="beta-confirm-actions"><button class="button quiet-button compact-button" type="button" on:click={cancelBetaBuildConfirmation}>Cancel</button><button class="button primary compact-button" type="button" data-beta-confirm on:click={() => void switchBetaBuild()}>Switch beta</button></div>
                {#if beta.betaSwitchError}<div class="beta-menu-error" role="alert"><span>{beta.betaSwitchError}</span><button class="text-button" type="button" data-beta-retry on:click={() => void switchBetaBuild()}>Retry</button></div>{/if}
              </div>
            {:else}
              {#if beta.betaError}
                <div class="beta-menu-error" role="alert"><span>{beta.betaError}</span><button class="text-button" type="button" data-beta-retry on:click={() => void loadBetaBuilds()}>Retry</button></div>
              {/if}
              {#if beta.betaBuilds.length}
                {#each beta.betaBuilds as build (build.sha)}
                  <button class="beta-build-option" class:current={build.sha === beta.betaCurrentSha} type="button" role="menuitem" data-beta-build={build.sha} aria-current={build.sha === beta.betaCurrentSha ? 'true' : undefined} aria-label={`${build.subject ? `${build.subject}, ` : ''}${betaBranchLabelValue(build.ref)} ${betaShortShaValue(build.sha)}${build.sha === beta.betaCurrentSha ? ', current' : ''}`} on:click={() => selectBetaBuild(build)}>
                    <span class="beta-build-copy">
                      <strong class:unavailable={!build.subject}>{build.subject || 'Commit message unavailable'}</strong>
                      <small title={build.sha}><span>{betaBranchLabelValue(build.ref)}</span><span aria-hidden="true"> · </span><code>{betaShortShaValue(build.sha)}</code></small>
                    </span>
                    {#if build.sha === beta.betaCurrentSha}<span class="beta-current-badge">Current</span>{/if}
                  </button>
                {/each}
              {:else}<div class="beta-menu-empty">No retained builds are available.</div>{/if}
            {/if}
          </div>
        {/if}
      </div>
    {/if}
    {#if signedInUser.admin}<button class:active={workspace.view === 'admin'} class="settings-link" type="button" aria-label="Admin metrics" aria-current={workspace.view === 'admin' ? 'page' : undefined} data-nav-tip="Metrics" on:click={() => navTo(() => setView('admin'))}><span class="nav-icon"><NavIcon name="metrics" /></span><span class="nav-label">Metrics</span><span class="nav-count">beta</span></button>{/if}
    <button class:active={workspace.view === 'settings'} class="settings-link" type="button" aria-label="Settings" aria-current={workspace.view === 'settings' ? 'page' : undefined} data-nav-tip="Settings" on:click={() => navTo(() => setView('settings'))}><span class="nav-icon"><NavIcon name="settings" /></span><span class="nav-label">Settings</span></button>
    <div class="account">
      <button bind:this={nav.accountMenuTrigger} class="user-chip" type="button" aria-label="Account menu" aria-haspopup="menu" aria-expanded={nav.accountMenuOpen} data-nav-tip={signedInUser.name} on:click={() => toggleNavMenu('account')}><span class="avatar" class:agent={signedInUser.kind === 'agent'}>{projectInitials({ name: signedInUser.name, key: signedInUser.name })}</span><span class="user-copy"><strong>{signedInUser.name}</strong><small>{signedInUser.email || (signedInUser.kind === 'agent' ? 'Agent' : 'Workspace member')}</small></span><span class="user-chevron" aria-hidden="true"><NavIcon name="updown" size={14} /></span></button>
      {#if nav.accountMenuOpen}
        <div bind:this={nav.accountMenuElement} class="nav-menu account-menu" role="menu" aria-label="Account" tabindex="-1" on:keydown={navMenuKeydown}>
          <div class="theme-segment" role="group" aria-label="Theme">
            <button type="button" role="menuitemradio" aria-checked={workspace.theme === 'light'} aria-label="Light theme" on:click={() => setTheme('light')}><NavIcon name="sun" size={14} /><span>Light</span></button>
            <button type="button" role="menuitemradio" aria-checked={workspace.theme === 'dark'} aria-label="Dark theme" on:click={() => setTheme('dark')}><NavIcon name="moon" size={14} /><span>Dark</span></button>
          </div>
          <button type="button" role="menuitem" on:click={() => { closeNavMenus(false); void logout(); }}><NavIcon name="sign-out" size={15} /><span>Sign out</span></button>
        </div>
      {/if}
    </div>
  </div>
</nav>
