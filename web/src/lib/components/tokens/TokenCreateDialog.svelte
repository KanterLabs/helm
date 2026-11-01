<script lang="ts">
  import { tokens } from '../../../app/tokens.svelte';
  import { dialogs } from '../../../app/state/dialogs.svelte';
  import { focusTrap } from '../../../app/dialogs';
  import { exactTime, tokenPresets, tokenScopes } from '../../tokenMetadata';
  const form = $derived(tokens);
  const duplicate = $derived(form.agent?.tokens?.some((token) => token.name.toLowerCase() === form.name.trim().toLowerCase()));
</script>

{#if form.agent && !dialogs.confirmRequest}
  <div class="modal-backdrop credential-backdrop" role="presentation"></div>
  <div class="modal credential-dialog" role="dialog" aria-modal="true" aria-label={`New token for ${form.agent.name}`} use:focusTrap>
    <header class="credential-dialog-header"><span class="eyebrow">Agent access</span><h2>New token for {form.agent.name}</h2><p>One token per machine or installation makes future changes easier.</p></header>
    <form onsubmit={(event) => { event.preventDefault(); void form.issue(); }}>
      <div class="credential-form-body">
        <label class="credential-label">Token name<input aria-label="Token name" data-dialog-initial-focus bind:value={form.name} maxlength="200" placeholder="Codex on work laptop" required /></label>
        {#if duplicate}<p class="credential-warning">A token already uses this name. Add its machine or purpose to tell them apart.</p>{/if}
        <fieldset><legend>Access</legend><div class="credential-presets">
          {#each Object.entries(tokenPresets) as [key, preset]}<label class:chosen={form.preset === key}><input type="radio" name="token-preset" aria-label={preset.label} checked={form.preset === key} onchange={() => form.selectPreset(key)} /><span><strong>{preset.label}</strong><small>{preset.description}</small></span></label>{/each}
          <label class:chosen={form.preset === 'custom'}><input type="radio" name="token-preset" aria-label="Custom" checked={form.preset === 'custom'} onchange={() => form.selectPreset('custom')} /><span><strong>Custom</strong><small>Choose exact permissions</small></span></label>
        </div>
        {#if form.preset === 'custom'}<div class="credential-scope-grid">{#each tokenScopes as { scope, description }}<label class="check-label"><input type="checkbox" aria-label={scope} checked={form.scopes.includes(scope)} onchange={() => form.toggleScope(scope)} /><span><code>{scope}</code><small>{description}</small></span></label>{/each}</div><p class="credential-hint" data-scope-dependency-note>Some workflows also need tasks:read or notifications:read. These are separate permissions and are never added automatically.</p>{/if}
        </fieldset>
        <fieldset><legend>Projects</legend>
          <div class="credential-choice"><label><input type="radio" name="project-mode" aria-label="Selected projects" checked={form.projectMode === 'selected'} onchange={() => form.projectMode = 'selected'} /> Selected projects</label><label><input type="radio" name="project-mode" aria-label="All agent projects" checked={form.projectMode === 'all'} onchange={() => form.projectMode = 'all'} /> All agent projects</label></div>
          {#if form.projectMode === 'selected'}<div class="credential-project-picker">{#each form.availableProjects as project}<label class="check-label"><input type="checkbox" aria-label={project.name} checked={form.projectIds.includes(project.id)} onchange={() => form.toggleProject(project.id)} /><span>{project.name}</span></label>{/each}</div>{/if}
          <p class="credential-hint" data-token-project-coverage>{form.coverage}</p>
          {#if form.accessChange}<p class="credential-warning" data-token-access-change>Access changed: {form.accessChange} is outside this agent’s current ceiling and will be excluded from the replacement.</p>{/if}
        </fieldset>
        <fieldset><legend>Expiry</legend><div class="credential-expiry-options">{#each [['7', '7 days'], ['30', '30 days'], ['90', '90 days'], ['365', '1 year'], ['custom', 'Custom date'], ['none', 'No expiry']] as [value, label]}<label class:chosen={form.expiry === value}><input type="radio" name="token-expiry" aria-label={value === 'custom' ? 'Choose expiry date' : label} checked={form.expiry === value} onchange={() => form.expiry = value} />{label}</label>{/each}</div>
          {#if form.expiry === 'custom'}<label class="credential-label">Custom expiry date<input type="date" bind:value={form.customDate} /></label>{/if}
          <p class="credential-hint" class:credential-warning={form.expiry === 'none'} data-token-expiry-preview>{form.expiryPreview}</p>
        </fieldset>
        <div class="credential-review" data-token-issue-summary><strong>{form.name.trim() || 'Your new token'}</strong><p>{tokenPresets[form.preset]?.label || 'Custom'}: {tokenPresets[form.preset]?.description || `${form.scopes.length} exact permissions.`} In {form.projectMode === 'selected' && !form.projectIds.length ? 'your selected projects' : form.coverage}. {form.expiresAt ? `Until ${exactTime(form.expiresAt)}.` : form.expiresAt === null ? 'No expiry.' : ''}</p></div>
        {#if form.issueError}<div class="inline-alert warning" role="alert">{form.issueError}</div>{/if}
        {#if form.uncertain && !form.candidates.length}<button class="button quiet-button" type="button" onclick={() => void form.recoverUncertain()}>Check inventory again</button>{/if}
        {#each form.candidates as token}<div class="credential-review" data-issuance-candidate={token.id}><strong>{token.name}</strong><p>{token.id.slice(0, 8)} · Created {exactTime(token.created_at)}</p><button class="text-button credential-revoke" type="button" aria-label={`Revoke possible token ${token.name}`} onclick={() => form.agent && void form.revoke(form.agent, token)}>Revoke possible token</button></div>{/each}
      </div>
      <footer class="modal-actions credential-dialog-footer"><button class="text-button" type="button" disabled={form.creating} onclick={form.closeCreate}>Cancel</button><button class="button primary" type="submit" disabled={!form.valid || form.creating || form.uncertain}>{form.creating ? 'Generating…' : 'Generate token'}</button></footer>
    </form>
  </div>
{/if}
