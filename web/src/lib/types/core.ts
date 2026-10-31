export type SemanticState = 'backlog' | 'ready' | 'active' | 'blocked' | 'completed';

export type Priority = 'low' | 'normal' | 'high' | 'urgent';

export type ActorKind = 'human' | 'agent';

/** Reviewed human role; absent on a human means a verified identity awaiting admission. */
export type WorkspaceRole = 'owner' | 'admin' | 'member';

export type ChecklistCompletionPolicy = 'warn' | 'require';

export type Scope = 'projects:read' | 'projects:write' | 'tasks:read' | 'tasks:write' | 'tasks:claim' | 'events:read' | 'notifications:read' | 'notifications:write';

export interface Actor {
  id: string;
  kind: ActorKind;
  name: string;
  /** Present for human actors; agent email is unsupported and omitted. */
  email?: string;
  description?: string;
  admin?: boolean;
  workspace_role?: WorkspaceRole;
  disabled_at?: string;
  /** Agent ceiling, or an ordinary human member's explicit grants (empty = none). */
  project_ids?: string[];
  tokens?: ApiToken[];
  created_at?: string;
  updated_at?: string;
}

/** Small actor shape used by read-only coordination surfaces. */
export type ActorSummary = Pick<Actor, 'id' | 'kind' | 'name'>;

export interface Project {
  id: string;
  key: string;
  slug: string;
  name: string;
  description: string;
  color: string;
  favorite: boolean;
  archived_at?: string;
  checklist_completion_policy?: ChecklistCompletionPolicy;
  created_at?: string;
  updated_at?: string;
  task_count?: number;
  completed_task_count?: number;
  completed_count?: number;
  version?: number;
  /** "tickets" for the built-in ticket queue, hidden from project navigation. */
  system_kind?: 'tickets';
}

export interface Column {
  id: string;
  project_id: string;
  name: string;
  semantic_state: SemanticState;
  position: number;
  archived_at?: string;
  version?: number;
  /** Incremented whenever a card order in this column changes. */
  ordering_version?: number;
  created_at?: string;
  updated_at?: string;
}

export interface Label {
  id: string;
  project_id: string;
  name: string;
  color?: string;
  created_at?: string;
  updated_at?: string;
}

export interface Agent {
  id: string;
  kind: 'agent';
  name: string;
  description?: string;
  admin: false;
  project_ids?: string[];
  created_at: string;
  updated_at: string;
  disabled_at?: string;
  tokens?: ApiToken[];
}

export interface ApiToken {
  id: string;
  agent_id: string;
  actor_id: string;
  name: string;
  scopes: Scope[];
  project_ids: string[];
  created_at: string;
  expires_at?: string | null;
  token?: string;
  /** @deprecated The runtime calls this field token; plaintext is never returned again. */
  plaintext?: never;
  last_used_at?: string;
}

export interface AuthStatus {
  mode: 'local' | 'cloudflare' | 'tailnet' | 'disabled';
  configured: boolean;
  setup_required: boolean;
  authenticated: boolean;
  actor: Actor | null;
  user: Actor | null;
  /** @deprecated Compatibility alias not emitted by the runtime. */
  needs_setup?: never;
}

export interface Collection<T> {
  data: T[];
  next_cursor?: string | null;
}

export interface BoardDescriptor {
  id: string;
  project_id: string;
  name: string;
  slug: string;
  default: boolean;
  enabled: boolean;
}

export interface ApiErrorShape {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

export class ApiError extends Error {
  code: string;
  status: number;
  details: Record<string, unknown>;

  constructor(message: string, status: number, code = 'request_failed', details: Record<string, unknown> = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
    this.details = details;
  }
}
