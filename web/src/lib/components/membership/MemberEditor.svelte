<svelte:options runes={true} />

<script lang="ts">
  // Administrator editor for one human identity: admission, profile, role,
  // explicit grants, status, local password reset and sessions. Only fields
  // that differ from the saved member are sent; the saved response replaces
  // the draft, so the panel always reflects what the server accepted.
  import { api } from '../../api';
  import type { Actor, AuthStatus, Member, MemberPatchInput, Project, WorkspaceRole } from '../../types';
  import { isAdminRole, minimumPasswordError, roleLabels, sameIds, usesLocalCredentials } from './access';
  import { accessGeneration, describeError, isUncertain, refreshMembers, updateMember } from './membersState.svelte';
  import ProjectGrants from './ProjectGrants.svelte';
  import SessionList from './SessionList.svelte';
  import type { ConfirmAction } from './types';

  let { member, authStatus, currentUser, projects, confirmAction, onSelfChanged }: {
    member: Member;
    authStatus: AuthStatus | null;
    currentUser: Actor;
    projects: Project[];
    confirmAction: ConfirmAction;
    onSelfChanged: (actor: Actor) => void | Promise<void>;
  } = $props();

  const roleOptions: WorkspaceRole[] = ['member', 'admin', 'owner'];
  let name = $state('');
  let description = $state('');
  let role = $state<WorkspaceRole>('member');
  let selected = $state<string[]>([]);
  let saving = $state('');
  let error = $state('');
  let notice = $state('');
  let retry = $state<MemberPatchInput | null>(null);
  let resetPassword = $state('');
  let resetConfirmation = $state('');
  let sessionsToken = $state(0);
  let synced = '';

  const pending = $derived(!member.workspace_role);
  const self = $derived(member.id === currentUser.id);
  const disabled = $derived(Boolean(member.disabled_at));
  const local = $derived(usesLocalCredentials(authStatus));
  const known = $derived(new Set(projects.map((project) => project.id)));
  const hidden = $derived((member.project_ids ?? []).filter((id) => !known.has(id)));
  const changes = $derived(buildPatch());

  // Resync before render whenever the saved member changes.
  $effect.pre(() => {
    const stamp = `${member.id}:${member.updated_at}`;
    if (stamp === synced) return;
    synced = stamp;
    name = member.name;
    description = member.description ?? '';
    role = member.workspace_role ?? 'member';
    selected = (member.project_ids ?? []).filter((id) => known.has(id));
  });

  function buildPatch(): MemberPatchInput {
    const patch: MemberPatchInput = {};
    if (name.trim() && name.trim() !== member.name) patch.name = name.trim();
    if (description !== (member.description ?? '')) patch.description = description;
    if (pending || role !== member.workspace_role) patch.role = role;
    if (!isAdminRole(role)) {
      const ids = [...selected, ...hidden];
      if (!sameIds(ids, member.project_ids ?? [])) patch.project_ids = ids;
    }
    return patch;
  }

  async function send(patch: MemberPatchInput, action: string, success: string) {
    saving = action;
    error = '';
    notice = '';
    const outcome = await updateMember(member.id, patch);
    saving = '';
    if (outcome.ok) {
      retry = null;
      notice = success;
      if (self) await onSelfChanged(outcome.value);
      return;
    }
    if (outcome.stale) return;
    retry = outcome.uncertain ? patch : null;
    error = outcome.message;
  }

  function save(event: SubmitEvent) {
    event.preventDefault();
    if (saving || !Object.keys(changes).length) return;
    if (!name.trim()) { error = 'Enter a name.'; return; }
    void send({ ...changes }, 'save', pending ? `${member.name} was admitted.` : 'Changes saved.');
  }

  async function toggleDisabled() {
    const disabling = !disabled;
    const confirmed = await confirmAction(disabling
      ? { title: `Disable ${member.name}?`, message: self ? 'You will be signed out immediately and cannot sign in until another administrator reactivates you.' : `${member.name} is signed out everywhere and cannot sign in. Their history stays intact.`, confirmLabel: 'Disable member' }
      : { title: `Reactivate ${member.name}?`, message: 'They can sign in again with their existing credentials. Sessions ended by disabling stay ended.', confirmLabel: 'Reactivate member' });
    if (!confirmed) return;
    await send({ disabled: disabling }, 'status', disabling ? `${member.name} was disabled and signed out everywhere.` : `${member.name} was reactivated. Previous sessions stay signed out.`);
  }

  async function reset(event: SubmitEvent) {
    event.preventDefault();
    if (saving) return;
    error = minimumPasswordError(resetPassword, resetConfirmation);
    notice = '';
    if (error) return;
    const confirmed = await confirmAction({ title: `Reset ${member.name}'s password?`, message: `${member.name} is signed out of every session. Share the new password with them privately.`, confirmLabel: 'Reset password' });
    if (!confirmed) return;
    const generation = accessGeneration();
    saving = 'reset';
    try {
      await api.resetMemberPassword(member.id, resetPassword);
      if (generation !== accessGeneration()) return;
      resetPassword = ''; resetConfirmation = '';
      notice = `Password reset. Every session for ${member.name} was signed out.`;
      sessionsToken += 1;
    } catch (failure) {
      if (generation !== accessGeneration()) return;
      error = isUncertain(failure) ? "We couldn't confirm the reset. Resetting again with the same password is safe." : describeError(failure, 'The password could not be reset.');
    } finally {
      saving = '';
    }
  }
</script>

<section class="mship-editor" aria-label={`Edit ${member.name}`}>
  {#if pending}
    <p class="mship-callout">{member.email || member.name} verified their identity but has no access yet. Choose a role and, for members, the projects they may open, then admit them.</p>
  {/if}
  <form class="mship-form" onsubmit={save}>
    <div class="mship-row-fields">
      <label>Name<input bind:value={name} maxlength="200" required /></label>
      <label>Workspace role
        <select bind:value={role}>{#each roleOptions as option}<option value={option}>{roleLabels[option]}</option>{/each}</select>
      </label>
    </div>
    <label>Description <span class="optional">Optional</span><textarea rows="2" bind:value={description} maxlength="10000"></textarea></label>
    {#if isAdminRole(role)}
      <p class="mship-hint">Owners and administrators can open every project. Saved project grants are kept for later.</p>
    {:else}
      <ProjectGrants {projects} bind:selected hiddenGrantCount={hidden.length} />
    {/if}
    {#if error}
      <div class="inline-alert error" role="alert"><span>!</span>{error}
        {#if retry}<button class="text-button" type="button" onclick={() => retry && send(retry, 'save', 'Changes saved.')}>Retry same change</button><button class="text-button" type="button" onclick={() => { retry = null; error = ''; void refreshMembers(); }}>Refresh members</button>{/if}
      </div>
    {/if}
    {#if notice}<p class="mship-status" role="status">{notice}</p>{/if}
    <div class="mship-actions">
      {#if !pending}
        <button class="button quiet-button" class:danger-button={!disabled} type="button" disabled={Boolean(saving)} onclick={toggleDisabled}>{#if saving === 'status'}<span class="button-spinner"></span>{/if}{disabled ? 'Reactivate member' : 'Disable member'}</button>
      {/if}
      <button class="button primary" type="submit" disabled={Boolean(saving) || !Object.keys(changes).length}>{#if saving === 'save'}<span class="button-spinner"></span>{/if}{pending ? 'Admit to workspace' : 'Save changes'}</button>
    </div>
  </form>
  {#if local && !self && !pending}
    <form class="mship-subsection mship-form" aria-label={`Reset password for ${member.name}`} onsubmit={reset}>
      <h4>Reset password</h4>
      <div class="mship-row-fields">
        <label>New password for {member.name}<input type="password" bind:value={resetPassword} autocomplete="new-password" required /></label>
        <label>Confirm new password for {member.name}<input type="password" bind:value={resetConfirmation} autocomplete="new-password" required /></label>
      </div>
      <p class="mship-hint">Signs {member.name} out of every session. At least 12 characters.</p>
      <div class="mship-actions"><button class="button quiet-button" type="submit" disabled={Boolean(saving)}>{#if saving === 'reset'}<span class="button-spinner"></span>{/if}Reset password</button></div>
    </form>
    <SessionList label={`Sessions for ${member.name}`} actorId={member.id} actorName={member.name} refreshToken={sessionsToken} {confirmAction} />
  {/if}
</section>
