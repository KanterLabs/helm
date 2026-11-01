import type { Actor, WorkspaceRole } from './core';

/**
 * Administrator-only human membership directory entry. A missing
 * workspace_role means a verified identity is still awaiting admission.
 * This is not the TC-8 assignee directory.
 */
export interface Member extends Actor {
  kind: 'human';
  last_seen_at?: string | null;
}

/** POST /members. No invitation email exists; omit password outside local auth. */
export interface MemberCreateInput {
  email: string;
  name: string;
  description?: string;
  role?: WorkspaceRole;
  project_ids?: string[];
  password?: string;
}

/**
 * PATCH /members/{id}. Omitted fields stay unchanged; project_ids [] revokes
 * every grant. The server rejects null values, so never send them.
 */
export interface MemberPatchInput {
  name?: string;
  description?: string;
  role?: WorkspaceRole;
  project_ids?: string[];
  disabled?: boolean;
}

/**
 * Session descriptor. IDs are non-secret audit handles, not labels. The
 * server never sends token/hash values or a current-session flag.
 */
export interface SessionDescriptor {
  id: string;
  actor_id: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  revoked_at?: string | null;
  ip_address?: string;
  user_agent?: string;
}
