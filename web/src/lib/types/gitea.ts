// Gitea / Forgejo connection wire shapes. Source of truth: contract C5 in
// docs/GITEA_OAUTH_PLAN.md (the HTTP API under /api/v1/integrations/gitea).

export type GiteaProduct = 'gitea' | 'forgejo';
export type GiteaConnectionState = 'disconnected' | 'connected' | 'reconnect_required';
export type GiteaUnavailableReason = '' | 'disabled' | 'public_origin_missing';
export type GiteaAccess = 'scoped' | 'full' | 'unknown';
export type GiteaCheckState = 'ok' | 'unreachable' | 'unauthorized';

/** The fixed error codes of contract C1; anything else is shown generically. */
export type GiteaErrorCode =
  | 'unavailable' | 'invalid_server_url' | 'insecure_http' | 'server_unreachable' | 'tls_error' | 'not_a_forge'
  | 'issuer_mismatch' | 'already_connected' | 'state_invalid' | 'denied' | 'scope_conflict' | 'authorize_failed'
  | 'client_confidential' | 'client_unknown' | 'exchange_failed' | 'storage_failed' | 'reconnect_required';

export interface GiteaServer {
  base_url: string;
  product: GiteaProduct | string;
  /** May be empty when the forge does not report it. */
  version: string;
  insecure_http: boolean;
}

export interface GiteaAccount {
  login: string;
  id: number;
}

export interface GiteaStatus {
  available: boolean;
  unavailable_reason: GiteaUnavailableReason | string;
  redirect_uri: string;
  state: GiteaConnectionState | string;
  server: GiteaServer | null;
  client_id: string;
  account: GiteaAccount | null;
  access: GiteaAccess | string;
  repository_count: number;
  connected_at: string | null;
  checked_at: string | null;
  check: { state: GiteaCheckState | string; code: string };
  register_url: string;
  revoke_url: string;
}

export interface GiteaProbeInput {
  base_url: string;
  allow_insecure_http: boolean;
}

export interface GiteaProbeResult {
  server: GiteaServer;
  redirect_uri: string;
  register_url: string;
}

/** In `reconnect_required`, empty values reuse the stored server and client ID. */
export interface GiteaStartInput extends GiteaProbeInput {
  client_id: string;
}

export interface GiteaRepository {
  full_name: string;
  private: boolean;
  archived: boolean;
}

export interface GiteaRepositoryList {
  repositories: GiteaRepository[];
  truncated: boolean;
}
