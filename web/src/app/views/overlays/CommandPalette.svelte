<svelte:options runes={true} />

<script lang="ts">
  // Command palette dialog.
  import { commandChoiceId } from '../../../lib/commandPalette';
  import { focusTrap } from '../../dialogs';
  import { closeCommandPalette, commandChoiceIcon, commandInputChanged, commandKeydown, commandOptionKeydown, loadCommandIssues, selectCommand } from '../../palette';
  import { palette } from '../../state/palette.svelte';
</script>

{#if palette.commandOpen}
  <div class="modal-backdrop command-backdrop" role="presentation" on:click={closeCommandPalette}></div>
  <div class="command-menu" role="dialog" aria-modal="true" aria-labelledby="command-dialog-title" use:focusTrap>
    <h2 id="command-dialog-title" class="sr-only">Search Helm</h2>
    <div class="command-input-wrap"><span aria-hidden="true">⌕</span><input bind:this={palette.commandInput} data-dialog-initial-focus bind:value={palette.commandQuery} on:input={() => { palette.commandIndex = 0; commandInputChanged(); }} on:keydown={commandKeydown} placeholder="Search projects, tasks, issues, views, or actions…" aria-label="Search projects and views, tasks, issues, and actions" role="combobox" aria-expanded={palette.commandOpen} aria-controls="command-results" aria-autocomplete="list" aria-activedescendant={palette.activeCommandOptionId || undefined} autocomplete="off" /><kbd>ESC</kbd></div>
    {#if palette.commandIssuesLoading || palette.commandSearchLoading}<div class="command-status" role="status" aria-live="polite">Searching Helm…</div>{/if}
    {#if palette.commandIssuesError}<div class="command-status command-status-error" role="alert"><span>{palette.commandIssuesError}</span><button class="text-button" type="button" on:click={() => void loadCommandIssues()}>Retry issue search</button></div>{/if}
    <div id="command-results" class="command-results" role="listbox" aria-label="Command results" aria-busy={palette.commandIssuesLoading || palette.commandSearchLoading}>
      {#if palette.commandChoices.length}
        {#each palette.commandChoices as choice, index (choice.id)}
          <button id={commandChoiceId(choice)} class:selected={index === palette.commandIndex} class={`command-row ${choice.kind}`} role="option" aria-selected={index === palette.commandIndex} aria-posinset={index + 1} aria-setsize={palette.commandChoices.length} aria-label={`${choice.label}. ${choice.hint}`} tabindex="-1" type="button" on:focus={() => palette.commandIndex = index} on:mouseenter={() => palette.commandIndex = index} on:click={() => void selectCommand(choice)} on:keydown={(event) => commandOptionKeydown(event, choice)} on:mousedown|preventDefault><span class={`command-icon ${choice.kind}`} aria-hidden="true">{commandChoiceIcon(choice)}</span><span><strong>{choice.label}</strong><small>{choice.hint}</small></span><span class="command-enter" aria-hidden="true">↵</span></button>
        {/each}
      {:else if palette.commandIssuesLoading || palette.commandSearchLoading}
        <div class="command-empty" role="status" aria-live="polite">Searching Helm…</div>
      {:else}
        <div class="command-empty" role="status" aria-live="polite">No commands match “{palette.commandQuery}”. Try a different search.</div>
      {/if}
    </div>
    <div class="command-footer"><span><kbd>↑</kbd><kbd>↓</kbd> Navigate</span><span><kbd>↵</kbd> Open</span><span><kbd data-command-shortcut>{palette.commandShortcut}</kbd> Open palette</span><span><kbd>ESC</kbd> Close</span></div>
  </div>
{/if}
