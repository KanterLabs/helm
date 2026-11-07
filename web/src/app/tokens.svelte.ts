import { onMount, tick } from 'svelte';
import { api } from '../lib/api';
import { ApiError, type Agent, type ApiToken, type Scope } from '../lib/types';
import { DAY, exactTime, projectLabels, tokenPreset, tokenPresets, tokenProjectLabel, usedAgo } from '../lib/tokenMetadata';
import { friendlyError, toast } from './feedback';
import { requestConfirm, restoreDialogFocus, settleConfirm } from './dialogs';
import { dialogs } from './state/dialogs.svelte';
import { session } from './state/session.svelte';
import { settings } from './state/settings.svelte';
import { workspace } from './state/workspace.svelte';
import { loadAgents, resetAgents } from './settings';

/** Token drafts, one-time secrets and explicit credential lifecycle actions. */
class TokensState {
  agent = $state.raw<Agent | null>(null);
  name = $state.raw('');
  scopes = $state.raw<Scope[]>([]);
  preset = $state.raw('worker');
  projectMode = $state.raw('selected');
  projectIds = $state.raw<string[]>([]);
  expiry = $state.raw('90');
  customDate = $state.raw('');
  creationTime = $state.raw(Date.now());
  creating = $state.raw(false);
  issueError = $state.raw('');
  uncertain = $state.raw(false);
  candidates = $state.raw<ApiToken[]>([]);
  reveal = $state.raw<ApiToken | null>(null);
  revealAgent = $state.raw<Agent | null>(null);
  replacement = $state.raw<ApiToken | null>(null);
  accessChange = $state.raw('');
  saved = $state.raw(false);
  copied = $state.raw(false);
  manualCopy = $state.raw(false);
  refreshError = $state.raw('');
  revokePending = $state.raw('');
  /** A token issued but never shown (its session or dialog changed mid-request) that could not be revoked. */
  orphanNotice = $state.raw('');
  #returnFocus: HTMLElement | null = null;
  #returnAgentId = '';
  #generation = 0;
  #issuanceBefore = new Set<string>();
  #issuanceStarted = 0;

  get availableProjects() {
    return workspace.projects.filter((project) => !this.agent?.project_ids?.length || this.agent.project_ids.includes(project.id));
  }
  get expiresAt(): string | null {
    if (this.expiry === 'none') return null;
    if (this.expiry === 'custom') {
      if (!/^\d{4}-\d{2}-\d{2}$/.test(this.customDate)) return '';
      const date = new Date(`${this.customDate}T23:59:59.999`);
      return Number.isFinite(date.getTime()) ? date.toISOString() : '';
    }
    return new Date(this.creationTime + Number(this.expiry) * DAY).toISOString();
  }
  get valid() {
    const ids = new Set(this.availableProjects.map((p) => p.id));
    return Boolean(this.name.trim() && this.name.trim().length <= 200 && this.scopes.length && (this.projectMode === 'all' || this.projectIds.length && this.projectIds.every((id) => ids.has(id))) && (this.expiresAt === null || this.expiresAt && Date.parse(this.expiresAt) > Date.now()));
  }
  get coverage() {
    if (this.projectMode === 'selected') return projectLabels(this.projectIds, workspace.projects) || 'Select at least one project';
    return this.agent?.project_ids?.length ? projectLabels(this.agent.project_ids, workspace.projects) : 'All workspace projects, including future projects';
  }
  get expiryPreview() { return this.expiresAt === null ? 'This token never expires' : this.expiresAt ? `Expires ${exactTime(this.expiresAt)} (${Intl.DateTimeFormat().resolvedOptions().timeZone})` : 'Choose a future date'; }

  open = (agent: Agent, replacement: ApiToken | null = null) => {
    if (!session.workspaceAdmin || agent.intake_source || agent.disabled_at || this.creating || this.reveal) return;
    this.#generation++;
    this.#returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    this.#returnAgentId = agent.id;
    this.agent = agent;
    this.replacement = replacement;
    this.name = replacement ? `${replacement.name.slice(0, 180)} (replacement)` : '';
    this.scopes = [...(replacement?.scopes || tokenPresets.worker.scopes)];
    this.preset = tokenPreset(this.scopes);
    this.projectMode = replacement && !replacement.project_ids.length ? 'all' : 'selected';
    const available = new Set(this.availableProjects.map((p) => p.id));
    this.projectIds = (replacement?.project_ids || []).filter((id) => available.has(id));
    this.accessChange = replacement ? projectLabels(replacement.project_ids.filter((id) => !available.has(id)), workspace.projects) : '';
    this.expiry = '90'; this.customDate = ''; this.creationTime = Date.now();
    this.issueError = ''; this.uncertain = false; this.candidates = []; this.refreshError = '';
  };
  selectPreset = (key: string) => {
    this.preset = key;
    if (tokenPresets[key]) this.scopes = [...tokenPresets[key].scopes];
  };
  toggleScope = (scope: Scope) => {
    this.scopes = this.scopes.includes(scope) ? this.scopes.filter((item) => item !== scope) : [...this.scopes, scope];
    this.preset = 'custom';
  };
  toggleProject = (id: string) => { this.projectIds = this.projectIds.includes(id) ? this.projectIds.filter((item) => item !== id) : [...this.projectIds, id]; };
  restoreFocus = async () => {
    await tick();
    if (this.#returnFocus?.isConnected) this.#returnFocus.focus();
    else (document.querySelector<HTMLElement>(`[data-agent-id="${this.#returnAgentId}"] [data-token-trigger]`) || document.querySelector<HTMLElement>('main.content'))?.focus();
  };
  closeCreate = () => {
    if (this.creating) return;
    this.#generation++; this.agent = null; this.name = ''; this.scopes = []; this.projectIds = []; this.candidates = []; this.replacement = null; this.issueError = ''; this.uncertain = false;
    void this.restoreFocus();
  };
  issue = async () => {
    if (!session.workspaceAdmin || !this.agent || !this.valid || this.creating || this.uncertain) return;
    const agent = this.agent, generation = this.#generation, sessionGeneration = session.sessionGeneration;
    const before = new Set((agent.tokens || []).map((token) => token.id));
    const started = Date.now();
    this.#issuanceBefore = before; this.#issuanceStarted = started;
    this.creating = true; this.issueError = '';
    try {
      const result = await api.createToken(agent.id, { name: this.name.trim(), scopes: [...this.scopes], project_ids: this.projectMode === 'selected' ? [...this.projectIds] : [], expires_at: this.expiresAt }, { suppressNetworkUnavailable: true });
      if (generation !== this.#generation || sessionGeneration !== session.sessionGeneration || !session.user || !session.workspaceAdmin) {
        // Never reveal plaintext to a different session or a dismissed dialog,
        // but never leave an unseen live credential behind silently either.
        await this.#discardUnseen(agent, result);
        return;
      }
      this.reveal = result; this.revealAgent = agent; this.saved = false; this.copied = false; this.manualCopy = false;
      this.agent = null; this.name = ''; this.issueError = ''; this.candidates = [];
      await loadAgents();
      if (generation === this.#generation && sessionGeneration === session.sessionGeneration && settings.agentsError && this.reveal === result) this.refreshError = 'Your token was created, but the inventory could not be refreshed. You can still save the token.';
    } catch (error) {
      if (generation !== this.#generation || sessionGeneration !== session.sessionGeneration) return;
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) this.issueError = friendlyError(error, 'Check the token settings and try again.');
      else {
        this.uncertain = true;
        this.issueError = 'A token may have been created without being shown to you. Do not retry until the inventory is checked; revoke the possible token before generating another.';
        await this.recoverUncertain(before, started);
      }
    } finally { if (generation === this.#generation) this.creating = false; }
  };
  #discardUnseen = async (agent: Agent, token: ApiToken) => {
    try {
      await api.deleteToken(token.id);
      toast('info', `A token for ${agent.name} was created during a session change and has been revoked. Generate a new one if you still need it.`);
    } catch {
      this.orphanNotice = `A token (${token.id.slice(0, 8)}, “${token.name}”) for ${agent.name} was created but never shown. Revoke it from the inventory.`;
    }
    if (session.user && session.workspaceAdmin) await loadAgents();
  };
  recoverUncertain = async (before?: Set<string>, started?: number) => {
    const agent = this.agent, generation = this.#generation, sessionGeneration = session.sessionGeneration;
    if (!session.workspaceAdmin || !agent) return;
    before ??= this.#issuanceBefore; started ??= this.#issuanceStarted;
    await loadAgents();
    if (generation !== this.#generation || sessionGeneration !== session.sessionGeneration || !session.workspaceAdmin || !this.agent) return;
    if (settings.agentsError) { this.issueError = 'A token may have been created. The inventory could not be checked; refresh it before retrying.'; return; }
    const tokens = settings.agents.find((item) => item.id === agent.id)?.tokens || [];
    this.candidates = tokens.filter((token) => (!before || !before.has(token.id)) && (!started || Date.parse(token.created_at) >= started - 60_000) && token.name === this.name.trim());
    this.uncertain = this.candidates.length > 0;
    if (!this.uncertain) this.issueError = 'No possible token was found in the refreshed inventory. Review the settings before trying again.';
  };
  revoke = async (agent: Agent, token: ApiToken): Promise<boolean> => {
    if (!session.workspaceAdmin || this.revokePending) return false;
    const sessionGeneration = session.sessionGeneration, generation = this.#generation;
    const current = () => generation === this.#generation && sessionGeneration === session.sessionGeneration && session.workspaceAdmin;
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const used = token.last_used_at ? `Used ${usedAgo(token.last_used_at) === 'Just now' ? 'less than a minute ago' : usedAgo(token.last_used_at).toLowerCase()}. Its client may fail immediately.` : 'This token has never been used.';
    this.revokePending = token.id;
    try {
      const confirmed = await requestConfirm({ title: `Revoke token “${token.name}”?`, message: `${agent.name} · ${token.id.slice(0, 8)} · ${tokenProjectLabel(agent, token, workspace.projects)}. ${used} Revocation takes effect immediately and cannot be undone.`, confirmLabel: 'Revoke token', fallbackSelector: `[data-agent-id="${agent.id}"] [data-token-trigger], main.content` });
      if (!confirmed || !current()) return false;
      await api.deleteToken(token.id);
      if (!current()) return false;
      settings.agents = settings.agents.map((item) => ({ ...item, tokens: item.tokens?.filter((entry) => entry.id !== token.id) }));
      this.candidates = this.candidates.filter((item) => item.id !== token.id);
      if (this.orphanNotice.includes(token.id.slice(0, 8))) this.orphanNotice = '';
      if (!this.candidates.length) this.uncertain = false;
      toast('success', `${token.name} was revoked.`);
      restoreDialogFocus();
      return true;
    } catch (error) {
      if (current()) { toast('error', friendlyError(error, 'The token could not be revoked.')); restoreDialogFocus(); }
      return false;
    } finally {
      if (current()) {
        this.revokePending = '';
        await tick();
        if (trigger?.isConnected) trigger.focus();
      }
    }
  };
  copy = async (field: HTMLInputElement | undefined) => {
    const value = this.reveal?.token;
    if (!value) return;
    try { await navigator.clipboard.writeText(value); if (this.reveal?.token === value) { this.copied = true; this.manualCopy = false; } }
    catch { if (this.reveal?.token === value) { this.manualCopy = true; field?.focus(); field?.select(); } }
  };
  closeReveal = async (discard = false) => {
    if (!this.reveal) return;
    const reveal = this.reveal, generation = this.#generation;
    if ((!this.saved && !this.copied) || discard) {
      const yes = await requestConfirm({ title: 'Close without saving?', message: 'This token cannot be shown again. If you lose it, revoke it from the inventory and create another.', confirmLabel: 'Discard token', cancelLabel: 'Keep token open' });
      if (!yes || generation !== this.#generation || reveal !== this.reveal) return;
    }
    this.reveal = null; this.revealAgent = null; this.replacement = null; this.saved = false; this.copied = false; this.manualCopy = false; this.refreshError = '';
    void this.restoreFocus();
  };
  revokeOld = async () => {
    if (!this.replacement || !this.revealAgent || (!this.saved && !this.copied)) return;
    if (await this.revoke(this.revealAgent, this.replacement)) await this.closeReveal();
  };
  clear = () => {
    resetAgents();
    if (dialogs.confirmRequest) settleConfirm(false);
    this.#issuanceBefore.clear(); this.#issuanceStarted = 0;
    this.#returnFocus = null; this.#returnAgentId = ''; this.accessChange = '';
    this.#generation++; this.reveal = null; this.revealAgent = null; this.agent = null; this.replacement = null;
    this.name = ''; this.scopes = []; this.projectIds = []; this.candidates = []; this.creating = false; this.saved = false; this.copied = false; this.manualCopy = false; this.issueError = ''; this.refreshError = ''; this.uncertain = false; this.revokePending = '';
  };
  connect = () => {
    $effect(() => {
      if (this.reveal || this.agent || this.creating) document.documentElement.dataset.helmSecretOpen = 'true';
      else delete document.documentElement.dataset.helmSecretOpen;
    });
    onMount(() => {
      const clear = () => this.clear();
      window.addEventListener('helm:auth-invalidated', clear);
      return () => { clear(); delete document.documentElement.dataset.helmSecretOpen; window.removeEventListener('helm:auth-invalidated', clear); };
    });
  };
}

export const tokens = new TokensState();
