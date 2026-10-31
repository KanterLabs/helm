<svelte:options runes={true} />

<script lang="ts">
  // Settings: your account, human members (admins), project administration,
  // Luna, agents and tokens (admins) and appearance.
  import LunaRunHistory from '../../../lib/components/LunaRunHistory.svelte';
  import GitHubIntegrationSettings from '../../../lib/components/github/GitHubIntegrationSettings.svelte';
  import AccountSecurity from '../../../lib/components/membership/AccountSecurity.svelte';
  import MembersAdmin from '../../../lib/components/membership/MembersAdmin.svelte';
  import { helmStorageKeys } from '../../../lib/state';
  import { requestConfirm } from '../../dialogs';
  import { endAuthenticatedSession, refreshSignedInActor } from '../../session';
  import { cancelCodexLogin, connectCodex, copyCodexCode, disconnectCodex, loadCodexAccount } from '../../settings';
  import { admin } from '../../state/admin.svelte';
  import { session } from '../../state/session.svelte';
  import { settings } from '../../state/settings.svelte';
  import { workspace } from '../../state/workspace.svelte';
  import { applyTheme } from '../../theme';
  import AgentsSection from './AgentsSection.svelte';
  import ProjectAdminSection from './ProjectAdminSection.svelte';

  // Destructive account actions return focus to the main content if their
  // trigger disappears (for example a member row re-sorting after disable).
  const confirmAction = (request: { title: string; message: string; confirmLabel: string }) =>
    requestConfirm({ ...request, fallbackSelector: '[data-destructive-focus-target]' });
  // Grants may name archived or system projects; administrators load them all.
  const grantProjects = $derived(admin.adminProjects.length ? admin.adminProjects : workspace.projects);
</script>

<section class="page-heading"><div><div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Preferences</span></div><h1>Settings</h1><p>Your account, the people and agents in this workspace, and how Helm looks on this device.</p></div></section>
{#if session.user}
  <AccountSecurity user={session.user} authStatus={session.authStatus} projects={workspace.projects} {confirmAction} onSignedOut={endAuthenticatedSession} />
{/if}
{#if session.user && session.workspaceAdmin}
  <MembersAdmin authStatus={session.authStatus} currentUser={session.user} projects={grantProjects} {confirmAction} onSelfChanged={refreshSignedInActor} />
{/if}
{#if session.workspaceAdmin}
  <ProjectAdminSection />
  <GitHubIntegrationSettings projects={workspace.projects} defaultProjectId={workspace.activeProject?.id || ''} />
{/if}
<section class="settings-section codex-section">
  <div class="settings-section-heading"><div><span class="eyebrow">Luna assistant</span><h2>Your Codex subscription</h2><p>Connect your own Codex-enabled ChatGPT account. Helm stores it only in your private user runtime and never shares it with another user.</p></div>{#if settings.codexAccount?.connected}<span class="codex-connected">✓ Connected</span>{/if}</div>
  {#if settings.codexError}<div class="inline-alert error" role="alert"><span>!</span>{settings.codexError}</div>{/if}
  {#if settings.codexStatusLoading && !settings.codexAccount && !settings.codexLogin}<div class="list-skeleton"><div></div></div>
  {:else if settings.codexLogin}<div class="codex-device-panel"><div><strong>Finish connecting in ChatGPT</strong><p>Open the secure sign-in page and enter this one-time code.</p></div><div class="codex-code-row"><code>{settings.codexLogin.user_code}</code><button class="button quiet-button compact-button" type="button" on:click={copyCodexCode}>Copy</button></div><div class="form-actions"><button class="text-button" type="button" disabled={settings.codexLoading} on:click={cancelCodexLogin}>Cancel login</button><a class="button primary" href={settings.codexLogin.verification_url} target="_blank" rel="noreferrer">Open ChatGPT ↗</a></div></div>
  {:else if settings.codexAccount?.connected}<div class="codex-account-row"><span class="agent-avatar">✦</span><span><strong>{settings.codexAccount.email || 'ChatGPT account'}</strong><small>{settings.codexAccount.plan_type ? `${settings.codexAccount.plan_type} plan` : 'Codex enabled'}</small></span><div class="form-actions"><button class="button quiet-button compact-button" type="button" disabled={settings.codexLoading || settings.codexStatusLoading} on:click={() => loadCodexAccount(true)}>Check access</button><button class="text-button danger-button" type="button" disabled={settings.codexLoading} on:click={disconnectCodex}>Disconnect</button></div></div>
  {:else}<div class="codex-empty"><p>Luna needs your personal ChatGPT subscription before it can draft tasks. Device-code login works on remote and headless self-hosted Helm installations.</p><button class="button primary" type="button" disabled={settings.codexLoading} on:click={connectCodex}>{#if settings.codexLoading}<span class="button-spinner"></span>{/if}Connect Codex</button></div>{/if}
  <LunaRunHistory />
</section>
<section class="settings-layout"><div class="settings-main">{#if session.workspaceAdmin}<AgentsSection />{/if}<div class="settings-section appearance-section"><div class="settings-section-heading"><div><span class="eyebrow">Workspace</span><h2>Appearance</h2><p>Choose how Helm feels on this device.</p></div></div><div class="theme-options"><button class:chosen={workspace.theme === 'light'} type="button" on:click={() => { workspace.theme = 'light'; localStorage.setItem(helmStorageKeys.theme, workspace.theme); applyTheme(); }}><span class="theme-preview light-preview">☼</span><span><strong>Light</strong><small>Clear and airy</small></span>{#if workspace.theme === 'light'}<span class="theme-check">✓</span>{/if}</button><button class:chosen={workspace.theme === 'dark'} type="button" on:click={() => { workspace.theme = 'dark'; localStorage.setItem(helmStorageKeys.theme, workspace.theme); applyTheme(); }}><span class="theme-preview dark-preview">☾</span><span><strong>Dark</strong><small>Focused and low-glare</small></span>{#if workspace.theme === 'dark'}<span class="theme-check">✓</span>{/if}</button></div></div></div><aside class="settings-aside"><div class="settings-aside-card"><span class="aside-icon">◎</span><h3>Built for safe handoffs</h3><p>Every mutation records its actor. Scoped agent tokens and optimistic versions keep collaboration predictable.</p><span class="aside-rule"></span><span class="aside-caption">Helm v1 · API-connected</span></div></aside></section>
