<script lang="ts">
  import TokenInventory from '../../../lib/components/tokens/TokenInventory.svelte';
  import { session } from '../../state/session.svelte';
</script>

{#if session.workspaceAdmin}<TokenInventory />{/if}
