<svelte:options runes={true} />

<script lang="ts">
  // Create a human account directly. Helm sends no invitation email; the
  // administrator shares local credentials privately, or the person signs in
  // through Cloudflare Access with this email.
  import type { AuthStatus, MemberCreateInput, Project, WorkspaceRole } from '../../types';
  import { isAdminRole, minimumPasswordError, roleLabels, usesLocalCredentials } from './access';
  import { createMember, refreshMembers } from './membersState.svelte';
  import ProjectGrants from './ProjectGrants.svelte';

  let { authStatus, projects, onDone }: { authStatus: AuthStatus | null; projects: Project[]; onDone: (message?: string) => void } = $props();

  let email = $state('');
  let name = $state('');
  let description = $state('');
  let role = $state<WorkspaceRole>('member');
  let projectIds = $state<string[]>([]);
  let password = $state('');
  let confirmation = $state('');
  let saving = $state(false);
  let error = $state('');
  let uncertain = $state(false);
  const local = $derived(usesLocalCredentials(authStatus));
  const roleOptions: WorkspaceRole[] = ['member', 'admin', 'owner'];

  function input(): MemberCreateInput {
    const value: MemberCreateInput = { email: email.trim(), name: name.trim(), role };
    if (description.trim()) value.description = description.trim();
    if (!isAdminRole(role) && projectIds.length) value.project_ids = [...projectIds];
    if (local) value.password = password;
    return value;
  }

  async function submit(event?: SubmitEvent) {
    event?.preventDefault();
    if (saving) return;
    error = !email.trim() || !name.trim() ? 'Enter an email address and a name.' : local ? minimumPasswordError(password, confirmation).replace('new passwords', 'passwords') : '';
    if (error) return;
    saving = true;
    const outcome = await createMember(input());
    saving = false;
    if (outcome.ok) {
      password = ''; confirmation = '';
      onDone(`${outcome.value.name} was added.`);
      return;
    }
    if (outcome.stale) return;
    uncertain = outcome.uncertain;
    error = outcome.message;
  }

  function cancel() {
    password = ''; confirmation = '';
    onDone();
  }
</script>

<form class="settings-form mship-form" aria-label="Add member" onsubmit={submit}>
  <div class="mship-row-fields">
    <label>Email<input type="email" bind:value={email} autocomplete="off" maxlength="320" required /></label>
    <label>Full name<input bind:value={name} autocomplete="off" maxlength="200" required /></label>
  </div>
  <label>Description <span class="optional">Optional</span><textarea rows="2" bind:value={description} maxlength="10000"></textarea></label>
  <label>Workspace role
    <select bind:value={role}>{#each roleOptions as option}<option value={option}>{roleLabels[option]}</option>{/each}</select>
  </label>
  {#if isAdminRole(role)}
    <p class="mship-hint">Owners and administrators can open every project and manage the workspace.</p>
  {:else}
    <ProjectGrants {projects} bind:selected={projectIds} />
  {/if}
  {#if local}
    <div class="mship-row-fields">
      <label>Password<input type="password" bind:value={password} autocomplete="new-password" required /></label>
      <label>Confirm password<input type="password" bind:value={confirmation} autocomplete="new-password" required /></label>
    </div>
    <p class="mship-hint">Helm sends no invitation email. Share this password with them privately; they can change it after signing in.</p>
  {:else if authStatus?.mode === 'cloudflare'}
    <p class="mship-hint">Helm sends no invitation email. They sign in through Cloudflare Access with this email address.</p>
  {:else if authStatus?.mode === 'tailnet'}
    <p class="mship-hint">Tailnet sign-in admits only the configured owner, so this person cannot sign in until the workspace uses local or Cloudflare authentication.</p>
  {/if}
  {#if error}
    <div class="inline-alert error" role="alert"><span>!</span>{error}
      {#if uncertain}<button class="text-button" type="button" onclick={() => submit()}>Retry same change</button><button class="text-button" type="button" onclick={() => { uncertain = false; error = ''; void refreshMembers(); }}>Refresh members</button>{/if}
    </div>
  {/if}
  <div class="mship-actions">
    <button class="text-button" type="button" onclick={cancel}>Cancel</button>
    <button class="button primary" type="submit" disabled={saving}>{#if saving}<span class="button-spinner"></span>{/if}Create member</button>
  </div>
</form>
