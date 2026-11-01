// Agents, scoped tokens and the Codex account connection.
import { api } from '../lib/api';
import type { Agent } from '../lib/types';
import { friendlyError, toast } from './feedback';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { taskModal } from './state/taskModal.svelte';
import { assistTaskWithLuna } from './taskModal';

let agentsRequest = 0;
let agentsEpoch = 0;

/** Eagerly drop actor-bound metadata and invalidate pending inventory reads. */
export function resetAgents() {
  agentsRequest++;
  agentsEpoch++;
  settings.agents = [];
  settings.agentsError = '';
  settings.agentsLoading = false;
  settings.agentSaving = false;
  settings.agentNameDraft = '';
  settings.agentDescriptionDraft = '';
  settings.selectedAgentId = '';
  settings.showAgentForm = false;
}

export async function loadAgents() {
  if (!session.workspaceAdmin) { resetAgents(); return; }
  const request = ++agentsRequest;
  settings.agentsLoading = true;
  settings.agentsError = '';
  const generation = session.sessionGeneration;
  const actorId = session.user?.id;
  const current = () => request === agentsRequest && generation === session.sessionGeneration && actorId === session.user?.id && session.workspaceAdmin;
  try {
    const result = await api.listAllAgents(true, { suppressNetworkUnavailable: true });
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

export async function createAgent() {
  if (!session.workspaceAdmin || !settings.agentNameDraft.trim() || settings.agentSaving) return;
  const epoch = agentsEpoch, generation = session.sessionGeneration;
  const current = () => epoch === agentsEpoch && generation === session.sessionGeneration && session.workspaceAdmin;
  settings.agentSaving = true;
  try {
    const agent = await api.createAgent({ name: settings.agentNameDraft.trim(), description: settings.agentDescriptionDraft.trim() });
    if (!current()) return;
    settings.agents = [...settings.agents, agent];
    settings.selectedAgentId = agent.id;
    settings.agentNameDraft = '';
    settings.agentDescriptionDraft = '';
    settings.showAgentForm = false;
    toast('success', `${agent.name} was created.`);
  } catch (error) {
    if (current()) settings.agentsError = friendlyError(error, 'The agent could not be created.');
  } finally {
    if (current()) settings.agentSaving = false;
  }
}

export function agentForToken(agentId: string): Agent | undefined {
  return settings.agents.find((agent) => agent.id === agentId);
}
