<svelte:options runes={true} />

<script lang="ts">
  // Explore mode controls: what is shown, zoom, full screen and exit.
  import { onMount } from 'svelte';
  import NavIcon from '../../../lib/components/NavIcon.svelte';
  import { exitExplore, exploreZoomLevels, stepExploreZoom, toggleExploreFullscreen } from '../../explore';
  import { explore } from '../../state/explore.svelte';

  let { title }: { title: string } = $props();
  let fullscreen = $state(false);
  const canFullscreen = typeof document !== 'undefined' && Boolean(document.fullscreenEnabled);

  onMount(() => {
    const sync = () => { fullscreen = Boolean(document.fullscreenElement); };
    sync();
    document.addEventListener('fullscreenchange', sync);
    return () => document.removeEventListener('fullscreenchange', sync);
  });
</script>

<div class="explore-bar" role="toolbar" aria-label="Roadmap explore controls">
  <div class="explore-title"><span class="explore-badge"><NavIcon name="expand" size={13} />Explore</span><strong>{title}</strong></div>
  <div class="explore-actions">
    <div class="explore-zoom">
      <button class="icon-button" type="button" aria-label="Zoom out" title="Zoom out (−)" disabled={explore.zoom <= exploreZoomLevels[0]} on:click={() => stepExploreZoom(-1)}><NavIcon name="minus" /></button>
      <span data-explore-zoom aria-live="polite">{Math.round(explore.zoom * 100)}%</span>
      <button class="icon-button" type="button" aria-label="Zoom in" title="Zoom in (+)" disabled={explore.zoom >= exploreZoomLevels[exploreZoomLevels.length - 1]} on:click={() => stepExploreZoom(1)}><NavIcon name="plus" /></button>
    </div>
    {#if canFullscreen}<button class="button quiet-button explore-fullscreen" type="button" aria-pressed={fullscreen} on:click={toggleExploreFullscreen}><NavIcon name="expand" />{fullscreen ? 'Leave full screen' : 'Full screen'}</button>{/if}
    <button bind:this={explore.exitButton} class="button quiet-button" type="button" on:click={() => void exitExplore()}><NavIcon name="close" />Exit explore<kbd aria-hidden="true">Esc</kbd></button>
  </div>
</div>
