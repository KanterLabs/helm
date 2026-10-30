<svelte:options runes={true} />

<script lang="ts">
  // The signed-in human's own account: identity, role, granted projects and,
  // for local authentication only, password and session controls.
  import type { Actor, AuthStatus, Project } from '../../types';
  import { isWorkspaceAdmin, projectLabel, roleLabel, usesLocalCredentials } from './access';
  import PasswordChange from './PasswordChange.svelte';
  import SessionList from './SessionList.svelte';
  import type { ConfirmAction } from './types';
  import './membership.css';

  let { user, authStatus, projects, confirmAction, onSignedOut }: {
    user: Actor;
    authStatus: AuthStatus | null;
    /** Projects the server already filtered to this viewer's grants. */
    projects: Project[];
    confirmAction: ConfirmAction;
    onSignedOut: () => void | Promise<void>;
  } = $props();

  let sessionsToken = $state(0);
  const admin = $derived(isWorkspaceAdmin(authStatus, user));
  const local = $derived(usesLocalCredentials(authStatus));
  const granted = $derived(projects.filter((project) => (user.project_ids ?? []).includes(project.id)));
  const initials = $derived(user.name.split(/\s+/).map((part) => part.charAt(0)).join('').slice(0, 2).toUpperCase());
  const managedBy = $derived(authStatus?.mode === 'cloudflare'
    ? 'Sign-in for this workspace is managed by Cloudflare Access. Helm keeps no local password for your account, so there is nothing to change here.'
    : authStatus?.mode === 'tailnet'
      ? 'Sign-in for this workspace is managed by your private Tailnet. Helm keeps no local password for your account, so there is nothing to change here.'
      : 'Authentication is disabled in this development workspace, so there is no password or session to manage.');
</script>

<section class="settings-section mship-section" aria-labelledby="mship-account-heading">
  <div class="settings-section-heading"><div><span class="eyebrow">Account &amp; security</span><h2 id="mship-account-heading">Your account</h2><p>How you sign in and what you can reach in this workspace.</p></div></div>
  <div class="mship-grid">
    <div class="mship-card">
      <div class="mship-identity">
        <span class="mship-avatar" aria-hidden="true">{initials}</span>
        <div><strong>{user.name}</strong>{#if user.email}<small>{user.email}</small>{/if}</div>
      </div>
      <div class="mship-badges"><span class={`mship-badge role-${user.workspace_role || 'member'}`}>{authStatus?.mode === 'disabled' ? 'Development access' : roleLabel(user)}</span></div>
      {#if admin}
        <p>You can open every project and manage members, agents and workspace settings.</p>
      {:else if granted.length}
        <p>You can open {granted.length === 1 ? 'this project' : `these ${granted.length} projects`}:</p>
        <ul class="mship-access">{#each granted as project (project.id)}<li>{projectLabel(project)}</li>{/each}</ul>
      {:else}
        <p>No projects granted yet. Ask a workspace owner or administrator to give you access.</p>
      {/if}
      {#if !local}<p class="mship-note">{managedBy}</p>{/if}
    </div>
    {#if local}<PasswordChange onChanged={() => { sessionsToken += 1; }} />{/if}
  </div>
  {#if local}
    <SessionList label="Signed-in sessions" autoLoad refreshToken={sessionsToken} {confirmAction} onSelfRevoked={onSignedOut} />
  {/if}
</section>
