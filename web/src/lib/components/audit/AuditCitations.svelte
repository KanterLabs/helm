<script lang="ts">
  /** Generic audit evidence references remain plain, untrusted text. */
  export let refs: string[] = [];
</script>

{#if refs.length}
  <div class="audit-evidence" role="group" aria-label="Evidence">
    <span class="optional">Evidence</span>
    {#each refs as ref}<span>{ref}</span>{/each}
  </div>
{/if}
