// Reactive state: Agents, tokens and the Codex account.
import type { Agent, ApiToken, CodexAccountStatus, CodexDeviceLogin } from '../../lib/types';

class SettingsState {
  agents = $state.raw<Agent[]>([]);
  agentsLoading = $state.raw(false);
  agentsError = $state.raw('');
  agentNameDraft = $state.raw('');
  agentSaving = $state.raw(false);
  agentDescriptionDraft = $state.raw('');
  selectedAgentId = $state.raw('');
  tokenNameDraft = $state.raw('');
  tokenScopes = $state.raw<string[]>(['projects:read', 'projects:write', 'tasks:read', 'tasks:write']);
  tokenProjectIds = $state.raw<string[]>([]);
  tokenCreating = $state.raw(false);
  revealedToken = $state.raw<ApiToken | null>(null);
  showAgentForm = $state.raw(false);
  showTokenForm = $state.raw(false);
  codexAccount = $state.raw<CodexAccountStatus | null>(null);
  codexLogin = $state.raw<CodexDeviceLogin | null>(null);
  codexStatusLoading = $state.raw(false);
  codexLoading = $state.raw(false);
  codexError = $state.raw('');
}

export const settings = new SettingsState();
