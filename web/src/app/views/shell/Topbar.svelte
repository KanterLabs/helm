<svelte:options runes={true} />

<script lang="ts">
  // Top bar: breadcrumb project picker, search, notifications and the New menu.
  import NavIcon from '../../../lib/components/NavIcon.svelte';
  import NotificationsInbox from '../../../lib/components/NotificationsInbox.svelte';
  import { projectInitials } from '../../../lib/state';
  import { closeBetaSwitcher } from '../../beta';
  import { openProjectModal } from '../../creation';
  import { openNotification } from '../../drawer/open';
  import { closeNavMenus, createFromMenu, navMenuKeydown, toggleNavMenu } from '../../nav/sidebar';
  import { closeCommandPalette, openCommandPalette } from '../../palette';
  import { selectProject } from '../../routing';
  import { nav } from '../../state/nav.svelte';
  import { session } from '../../state/session.svelte';
  import { workspace } from '../../state/workspace.svelte';

  // Rendered only while session.user is set (guarded by the parent).
  const signedInUser = $derived(session.user!);
</script>

<header class="topbar">
  <nav class="topbar-project" class:workspace-crumbs={!(nav.projectScopedView && workspace.activeProject)} aria-label="Breadcrumb">
    {#if nav.projectScopedView && workspace.activeProject}
      <button bind:this={nav.projectPickerTrigger} class="project-picker" type="button" data-project-picker-trigger aria-label={`Switch project, current ${workspace.activeProject.name}`} aria-expanded={nav.projectSwitcherOpen} on:click={() => { nav.projectSwitcherOpen = !nav.projectSwitcherOpen; closeBetaSwitcher(false); closeNavMenus(false); closeCommandPalette(); }}>
        <span class="project-dot" style={`--project-color: ${workspace.activeProject.color || '#6d5efc'}`}>{projectInitials(workspace.activeProject)}</span><span>{workspace.activeProject.name}</span><span class="picker-chevron" aria-hidden="true"><NavIcon name="chevron-down" size={13} /></span>
      </button>
    {:else}
      <span class="crumb-root">Workspace</span>
    {/if}
    <span class="crumb-separator" aria-hidden="true">/</span>
    <span class="crumb-current" aria-current="page">{nav.currentViewLabel}</span>
    {#if nav.projectSwitcherOpen}
      <div bind:this={nav.projectSwitcherPopover} class="popover project-popover" data-project-switcher-popover>
        <div class="popover-search"><span aria-hidden="true">⌕</span><input bind:value={nav.projectSwitcherQuery} placeholder="Find a project…" /></div>
        <div class="popover-list">
          {#if nav.filteredSwitcherProjects.length}
            {#each nav.filteredSwitcherProjects as project}
              <button class="popover-project" type="button" aria-label={project.name} on:click={() => selectProject(project)}><span class="project-dot" style={`--project-color: ${project.color || '#6d5efc'}`}>{projectInitials(project)}</span><span><strong>{project.name}</strong><small>{project.key}</small></span>{#if project.favorite}<span class="favorite-star">★</span>{/if}</button>
            {/each}
          {:else}<div class="popover-empty">No matching projects</div>{/if}
        </div>
        <button class="popover-create" type="button" data-project-modal-trigger on:click={openProjectModal}>＋ Create a project</button>
      </div>
    {/if}
  </nav>
  <div class="topbar-actions">
    <button class="icon-button mobile-search-trigger" type="button" aria-label="Search anything" data-command-trigger on:click={openCommandPalette}><NavIcon name="search" size={18} /></button>
    <NotificationsInbox
      sessionKey={`${signedInUser.id}:${session.sessionGeneration}`}
      activeProject={workspace.activeProject}
      onOpenNotification={openNotification}
    />
    <div class="create-menu">
      <button bind:this={nav.createMenuTrigger} class="button primary compact-button create-trigger" type="button" aria-label="Create new" aria-haspopup="menu" aria-expanded={nav.createMenuOpen} on:click={() => toggleNavMenu('create')}><NavIcon name="plus" size={14} /><span class="create-trigger-label">New</span><span class="create-trigger-chevron" aria-hidden="true"><NavIcon name="chevron-down" size={12} /></span></button>
      {#if nav.createMenuOpen}
        <div bind:this={nav.createMenuElement} class="nav-menu create-menu-panel" role="menu" aria-label="Create" tabindex="-1" on:keydown={navMenuKeydown}>
          <button type="button" role="menuitem" disabled={!workspace.activeProject} title={workspace.activeProject ? `New task in ${workspace.activeProject.name}` : 'Select a project first'} on:click={() => createFromMenu('task')}><NavIcon name="board" size={15} /><span>Task</span></button>
          <button type="button" role="menuitem" on:click={() => createFromMenu('bug')}><NavIcon name="bug" size={15} /><span>Bug report</span></button>
          <button type="button" role="menuitem" on:click={() => createFromMenu('project')}><NavIcon name="plus" size={15} /><span>Project</span></button>
        </div>
      {/if}
    </div>
  </div>
</header>
