<svelte:options runes={true} />

<script lang="ts">
  // Phone bottom navigation.
  import NavIcon from '../../../lib/components/NavIcon.svelte';
  import { toggleMobileNav } from '../../nav/sidebar';
  import { setView } from '../../routing';
  import { nav } from '../../state/nav.svelte';
  import { workspace } from '../../state/workspace.svelte';
</script>

<nav class="mobile-nav" aria-label="Primary navigation">
  <button class:active={workspace.view === 'my-work'} type="button" aria-label="My work" aria-current={workspace.view === 'my-work' ? 'page' : undefined} on:click={() => setView('my-work')}><span class="mobile-nav-icon"><NavIcon name="my-work" size={20} /></span><span>My work</span></button>
  <button class:active={workspace.view === 'issues'} type="button" aria-label="Issues" aria-current={workspace.view === 'issues' ? 'page' : undefined} on:click={() => setView('issues')}><span class="mobile-nav-icon"><NavIcon name="issues" size={20} /></span><span>Issues</span>{#if nav.sidebarCountsStatus === 'known' && nav.sidebarCounts?.issues}<span class="mobile-nav-badge" aria-hidden="true">{nav.sidebarCounts.issues}</span>{/if}</button>
  <button class:active={nav.projectScopedView && !nav.mobileNavOpen} type="button" aria-label="Board" aria-current={workspace.view === 'board' ? 'page' : undefined} on:click={() => setView('board')}><span class="mobile-nav-icon"><NavIcon name="board" size={20} /></span><span>Board</span></button>
  <button class:active={workspace.view === 'tickets'} type="button" aria-label="Tickets" aria-current={workspace.view === 'tickets' ? 'page' : undefined} on:click={() => setView('tickets')}><span class="mobile-nav-icon"><NavIcon name="tickets" size={20} /></span><span>Tickets</span></button>
  <button bind:this={nav.mobileMoreTrigger} class:active={nav.mobileNavOpen} type="button" aria-label="More" aria-expanded={nav.mobileNavOpen} aria-controls="primary-sidebar" on:click={toggleMobileNav}><span class="mobile-nav-icon"><NavIcon name="menu" size={20} /></span><span>More</span></button>
</nav>
