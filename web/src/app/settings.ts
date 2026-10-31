// Agents, scoped tokens and the Codex account connection.
import { api } from '../lib/api';
import type { Agent, ApiToken } from '../lib/types';
import { rememberDialogFocus, requestConfirm, restoreDialogFocus } from './dialogs';
import { friendlyError, toast } from './feedback';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { taskModal } from './state/taskModal.svelte';
import { assistTaskWithLuna } from './taskModal';

export async function loadAgents() {
  if (!session.workspaceAdmin) {
    settings.agents = [];
    settings.agentsError = '';
    return;
  }
  settings.agentsLoading = true;
  settings.agentsError = '';
  const generation = session.sessionGeneration;
  const actorId = session.user?.id;
  const current = () => generation === session.sessionGeneration && actorId === session.user?.id && session.workspaceAdmin;
  try {
    const result = await api.listAllAgents();
    if (!current()) return;
    settings.agents = result.data;
    if (!settings.selectedAgentId && settings.agents[0]) settings.selectedAgentId = settings.agents[0].id;
  } catch (error) {
    if (!current()) return;
    settings.agentsError = friendlyError(error, 'Agent settings could not be loaded.');
  } finally {
    if (current()) settings.agentsLoading = false;
  }
}

export async function loadCodexAccount(refresh = false) {
  settings.codexStatusLoading = true;
  settings.codexError = '';
  try {
    settings.codexAccount = await api.codexAccount(refresh);
    if (settings.codexAccount.connected) settings.codexLogin = null;
  } catch (error) {
    settings.codexError = friendlyError(error, 'Codex connection status could not be loaded.');
  } finally {
    settings.codexStatusLoading = false;
  }
}

export async function connectCodex() {
  settings.codexLoading = true;
  settings.codexError = '';
  try {
    settings.codexLogin = await api.startCodexLogin();
    const loginId = settings.codexLogin.login_id;
    void pollCodexLogin(loginId);
  } catch (error) {
    settings.codexError = friendlyError(error, 'Codex device login could not be started. Your ChatGPT workspace may have disabled device-code login.');
    if (taskModal.showTaskModal && taskModal.taskModalNeedsCodex) taskModal.taskModalError = settings.codexError;
  } finally {
    settings.codexLoading = false;
  }
}

export async function pollCodexLogin(loginId: string) {
  for (let attempt = 0; attempt < 150 && settings.codexLogin?.login_id === loginId && session.user; attempt += 1) {
    await new Promise((resolve) => window.setTimeout(resolve, 2000));
    if (settings.codexLogin?.login_id !== loginId || !session.user) return;
    try {
      const account = await api.codexAccount(true);
      settings.codexAccount = account;
      if (account.connected) {
        settings.codexLogin = null;
        toast('success', 'Codex connected to your ChatGPT subscription.');
        if (taskModal.showTaskModal && taskModal.taskModalNeedsCodex) void assistTaskWithLuna();
        return;
      }
    } catch {
      // Keep the device-code panel usable through transient network errors.
    }
  }
  if (settings.codexLogin?.login_id === loginId) settings.codexError = 'The Codex login is still pending. Cancel it and start a new code if this one expired.';
}

export async function cancelCodexLogin() {
  const loginId = settings.codexLogin?.login_id;
  if (!loginId) return;
  settings.codexLoading = true;
  settings.codexError = '';
  try {
    await api.cancelCodexLogin(loginId);
    settings.codexLogin = null;
  } catch (error) {
    settings.codexError = friendlyError(error, 'The pending Codex login could not be canceled.');
    if (taskModal.showTaskModal) taskModal.taskModalError = settings.codexError;
  } finally {
    settings.codexLoading = false;
  }
}

export async function disconnectCodex() {
  settings.codexLoading = true;
  settings.codexError = '';
  try {
    if (settings.codexLogin) await api.cancelCodexLogin(settings.codexLogin.login_id).catch(() => undefined);
    await api.logoutCodex();
    settings.codexLogin = null;
    settings.codexAccount = { connected: false, requires_openai_auth: true };
    toast('success', 'Codex disconnected from Helm.');
  } catch (error) {
    settings.codexError = friendlyError(error, 'Codex could not be disconnected.');
  } finally {
    settings.codexLoading = false;
  }
}

export async function copyCodexCode() {
  if (!settings.codexLogin) return;
  try {
    await navigator.clipboard.writeText(settings.codexLogin.user_code);
    toast('success', 'Device code copied.');
  } catch {
    toast('error', 'Device code could not be copied. Select it manually.');
  }
}

export function closeTokenReveal() {
  settings.revealedToken = null;
  restoreDialogFocus();
}

export async function createAgent() {
  if (!settings.agentNameDraft.trim() || settings.agentSaving) return;
  settings.agentSaving = true;
  try {
    const agent = await api.createAgent({ name: settings.agentNameDraft.trim(), description: settings.agentDescriptionDraft.trim() });
    settings.agents = [...settings.agents, agent];
    settings.selectedAgentId = agent.id;
    settings.agentNameDraft = '';
    settings.agentDescriptionDraft = '';
    settings.showAgentForm = false;
    toast('success', `${agent.name} was created.`);
  } catch (error) {
    settings.agentsError = friendlyError(error, 'The agent could not be created.');
  } finally {
    settings.agentSaving = false;
  }
}

export function toggleScope(scope: string) {
  settings.tokenScopes = settings.tokenScopes.includes(scope) ? settings.tokenScopes.filter((item) => item !== scope) : [...settings.tokenScopes, scope];
}

export function toggleTokenProject(projectId: string) {
  settings.tokenProjectIds = settings.tokenProjectIds.includes(projectId) ? settings.tokenProjectIds.filter((id) => id !== projectId) : [...settings.tokenProjectIds, projectId];
}

export async function createToken() {
  if (!settings.selectedAgentId || !settings.tokenNameDraft.trim() || !settings.tokenScopes.length) return;
  settings.tokenCreating = true;
  try {
    const token = await api.createToken(settings.selectedAgentId, {
      name: settings.tokenNameDraft.trim(),
      scopes: settings.tokenScopes,
      project_ids: settings.tokenProjectIds.length ? settings.tokenProjectIds : undefined
    });
    rememberDialogFocus('[data-token-trigger]');
    settings.revealedToken = token;
    settings.tokenNameDraft = '';
    settings.showTokenForm = false;
    await loadAgents();
    toast('success', 'Token created. Copy it now — it will not be shown again.');
  } catch (error) {
    settings.agentsError = friendlyError(error, 'The token could not be created.');
  } finally {
    settings.tokenCreating = false;
  }
}

export async function deleteToken(token: ApiToken) {
  const confirmed = await requestConfirm({
    title: `Revoke token “${token.name}”?`,
    message: 'This token will stop working immediately and cannot be restored.',
    confirmLabel: 'Revoke token',
    fallbackSelector: '[data-destructive-focus-target]'
  });
  if (!confirmed) return;
  try {
    await api.deleteToken(token.id);
    settings.agents = settings.agents.map((agent) => ({ ...agent, tokens: agent.tokens?.filter((item) => item.id !== token.id) }));
    restoreDialogFocus();
    toast('success', `${token.name} was revoked.`);
  } catch (error) {
    restoreDialogFocus();
    toast('error', friendlyError(error, 'The token could not be revoked.'));
  }
}

export async function copyRevealedToken() {
  const value = settings.revealedToken?.plaintext || settings.revealedToken?.token || '';
  if (!value || !navigator.clipboard) {
    toast('error', 'Token could not be copied. Select it manually.');
    return;
  }
  try {
    await navigator.clipboard.writeText(value);
    toast('success', 'Token copied to clipboard.');
  } catch {
    toast('error', 'Token could not be copied. Select it manually.');
  }
}

export function agentForToken(agentId: string): Agent | undefined {
  return settings.agents.find((agent) => agent.id === agentId);
}
