<svelte:options runes={true} />

<script lang="ts">
  // Human membership administration, shown only to active owners/admins.
  // Agents and their tokens are managed separately. Unmounting clears the
  // private directory and fences any response still in flight.
  import { onDestroy, onMount } from 'svelte';
  import type { Actor, AuthStatus, Member, Project } from '../../types';
  import { accessSummary, formatMoment, roleLabel } from './access';
  import MemberCreateForm from './MemberCreateForm.svelte';
  import MemberEditor from './MemberEditor.svelte';
  import { loadMembers, membersAdmin, resetMembershipState } from './membersState.svelte';
  import type { ConfirmAction } from './types';
  import './membership.css';

  let { authStatus, currentUser, projects, confirmAction, onSelfChanged }: {
    authStatus: AuthStatus | null;
    currentUser: Actor;
    /** Every workspace project; administrators see all of them. */
    projects: Project[];
    confirmAction: ConfirmAction;
    onSelfChanged: (actor: Actor) => void | Promise<void>;
  } = $props();

  let creating = $state(false);
  let notice = $state('');
  let expanded = $state<string[]>([]);
  const counts = $derived({
    pending: membersAdmin.members.filter((member) => !member.workspace_role).length,
    active: membersAdmin.members.filter((member) => member.workspace_role && !member.disabled_at).length,
    disabled: membersAdmin.members.filter((member) => member.workspace_role && member.disabled_at).length
  });

  function toggle(id: string) {
    expanded = expanded.includes(id) ? expanded.filter((item) => item !== id) : [...expanded, id];
  }

  function stateOf(member: Member): { label: string; tone: string } {
    if (!member.workspace_role) return { label: 'Awaiting admission', tone: 'pending' };
    return member.disabled_at ? { label: 'Disabled', tone: 'disabled' } : { label: 'Active', tone: 'active' };
  }

  const initials = (name: string) => name.split(/\s+/).map((part) => part.charAt(0)).join('').slice(0, 2).toUpperCase();

  onMount(() => { void loadMembers(); });
  onDestroy(resetMembershipState);
</script>

<section class="settings-section mship-section" aria-labelledby="mship-members-heading">
  <div class="settings-section-heading">
    <div><span class="eyebrow">People</span><h2 id="mship-members-heading">Members</h2><p>Owners and administrators manage the whole workspace. Members open only the projects you grant. Agents are managed separately below.</p></div>
    <button class="button primary" type="button" aria-expanded={creating} onclick={() => { creating = !creating; notice = ''; }}>＋ Add member</button>
  </div>
  {#if creating}
    <MemberCreateForm {authStatus} {projects} onDone={(message) => { creating = false; notice = message || ''; }} />
  {/if}
  {#if notice}<p class="mship-status" role="status">{notice}</p>{/if}
  {#if membersAdmin.error}
    <div class="inline-alert error" role="alert"><span>!</span>{membersAdmin.error}<button class="text-button" type="button" onclick={loadMembers}>Retry</button></div>
  {/if}
  {#if membersAdmin.loading && !membersAdmin.loaded}
    <div class="list-skeleton" aria-label="Loading members">{#each [1, 2] as item (item)}<div></div>{/each}</div>
  {:else if membersAdmin.loaded}
    <p class="mship-note">{counts.active} active{counts.pending ? ` · ${counts.pending} awaiting admission` : ''}{counts.disabled ? ` · ${counts.disabled} disabled` : ''}</p>
    <ul class="mship-list">
      {#each membersAdmin.members as member (member.id)}
        {@const status = stateOf(member)}
        <li class="mship-member" class:is-disabled={Boolean(member.disabled_at)}>
          <div class="mship-member-summary">
            <div class="mship-identity">
              <span class="mship-avatar" aria-hidden="true">{initials(member.name)}</span>
              <div><strong>{member.name}{member.id === currentUser.id ? ' (you)' : ''}</strong>{#if member.email}<small>{member.email}</small>{/if}</div>
            </div>
            <div class="mship-member-meta">
              <div class="mship-badges"><span class={`mship-badge role-${member.workspace_role || 'none'}`}>{roleLabel(member)}</span><span class={`mship-badge state-${status.tone}`}>{status.label}</span></div>
              <span>{accessSummary(member)} · Last active {formatMoment(member.last_seen_at)}</span>
            </div>
            <button class="button quiet-button compact-button" type="button" aria-expanded={expanded.includes(member.id)} aria-label={`Manage ${member.name}`} onclick={() => toggle(member.id)}>{expanded.includes(member.id) ? 'Close' : member.workspace_role ? 'Manage' : 'Review'}</button>
          </div>
          {#if expanded.includes(member.id)}
            <MemberEditor {member} {authStatus} {currentUser} {projects} {confirmAction} {onSelfChanged} />
          {/if}
        </li>
      {:else}
        <li class="mship-note">No human members yet.</li>
      {/each}
    </ul>
  {/if}
</section>
