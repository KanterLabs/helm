import type { Task } from './tasks';

export type TicketStatus = 'needs_triage' | 'ready' | 'in_progress' | 'waiting' | 'completed';

export type TicketQueue = 'open' | 'mine' | TicketStatus;

export interface TicketMembership {
  origin: 'alert' | 'manual';
  status: TicketStatus;
  created_at: string;
}

export type TicketCounts = Record<TicketQueue, number>;

export type TicketWebhookFormat = 'generic' | 'coolify';

export interface TicketWebhook {
  id: string;
  name: string;
  format: TicketWebhookFormat;
  project_id: string;
  project_key: string;
  project_slug: string;
  assignee_id?: string;
  secret_hint: string;
  created_at: string;
  rotated_at?: string;
  disabled_at?: string;
  last_delivery_at?: string;
  delivery_count: number;
}

/** Create/rotate response: the only time the secret and URL are returned. */
export interface TicketWebhookSecret {
  webhook: TicketWebhook;
  secret: string;
  url: string;
}

export interface CloudflareTokenPermission {
  key: string;
  type: 'read' | 'edit';
  label: string;
}

export type SetupStepStatus = 'pending' | 'running' | 'done' | 'skipped' | 'failed';

export interface CloudflareSetupRun {
  id: string;
  status: 'running' | 'done' | 'failed';
  zone: string;
  hostname: string;
  steps: { id: 'public_url' | 'connector' | 'reachable' | 'email' | 'forget'; label: string; status: SetupStepStatus; detail?: string }[];
  public_url?: string;
  email_base?: string;
  inbox_address?: string;
  inbox_name?: string;
  started_at: string;
  finished_at?: string;
}

export interface CloudflareConnectStatus {
  oauth_available: boolean;
  token_link: string;
  token_permissions: CloudflareTokenPermission[];
  connected: boolean;
  connected_via?: 'cloudflare' | 'token';
  expires_at?: string;
  run?: CloudflareSetupRun;
  active_public_hostname?: string;
}

export interface CloudflareZoneOption {
  id: string;
  name: string;
  account_name?: string;
  email_routing: 'ready' | 'off' | 'unknown';
  plus_addressing: boolean;
  suggested_hostname?: string;
  usable: boolean;
  reason?: string;
}

/** A test ticket Helm sent for a webhook through its own public URL. */
export interface TicketTestResult {
  ok: boolean;
  hostname: string;
  checked_at: string;
  latency_ms: number;
  status_code?: number;
  via_cloudflare: boolean;
  cf_ray?: string;
  disposition?: 'created' | 'repeated' | 'retained';
  ticket_key?: string;
  ticket_url?: string;
  occurrence_count?: number;
  message: string;
}

/** An address created in Helm whose mail becomes tickets in a project. */
export interface EmailInbox {
  id: string;
  name: string;
  project_id: string;
  project_key: string;
  project_slug: string;
  assignee_id?: string;
  assignee_name?: string;
  tag: string;
  /** Full address; empty while email is turned off. */
  address: string;
  created_at: string;
  replaced_at?: string;
  disabled_at?: string;
  last_received_at?: string;
  received_count: number;
}

export interface EmailIntake {
  id: string;
  provider: 'cloudflare';
  domain: string;
  local_part: string;
  account_id: string;
  zone_id: string;
  worker_name: string;
  rule_id: string;
  fallback_address?: string;
  subaddress_enabled_by_helm: boolean;
  public_endpoint_id?: string;
  status: 'active' | 'disabled';
  cleanup_pending: boolean;
  created_at: string;
  created_by_name?: string;
  disabled_at?: string;
}

export type EmailOutcome = 'created' | 'repeated' | 'retained' | 'duplicate' | 'unknown_recipient' | 'unreadable' | 'too_large';

export interface EmailReceipt {
  id: string;
  inbox_id?: string;
  inbox_name?: string;
  sender: string;
  subject: string;
  outcome: EmailOutcome;
  task_key?: string;
  project_slug?: string;
  occurrence_count: number;
  reason?: string;
  received_at: string;
}

export interface EmailIntakeView {
  active?: EmailIntake;
  history: EmailIntake[];
  recent: EmailReceipt[];
  required_permissions: string[];
  public_hostname?: string;
  suggested_domain?: string;
  worker_version: string;
  max_message_bytes: number;
}

export interface PublicEndpoint {
  id: string;
  provider: 'cloudflare';
  hostname: string;
  account_id: string;
  zone_id: string;
  tunnel_id: string;
  dns_record_id: string;
  status: 'active' | 'disabled';
  cleanup_pending: boolean;
  created_at: string;
  created_by_name?: string;
  disabled_at?: string;
}

export interface PublicEndpointTestResult {
  endpoint_id: string;
  ok: boolean;
  checked_at: string;
  latency_ms: number;
  status_code?: number;
  via_cloudflare: boolean;
  cf_ray?: string;
  message: string;
}

export interface PublicEndpointConnector {
  state: 'stopped' | 'starting' | 'connected' | 'restarting' | 'error';
  message?: string;
  started_at?: string;
  restarts: number;
  connected_at?: string;
  connections: number;
  locations?: string[];
  last_error_at?: string;
}

export interface PublicEndpointView {
  active?: PublicEndpoint;
  connector: PublicEndpointConnector;
  last_test?: PublicEndpointTestResult;
  public_hook_base?: string;
  history: PublicEndpoint[];
  required_permissions: string[];
  exposed_paths: string;
  connector_available: boolean;
}

export interface TicketCollection {
  data: Task[];
  next_cursor: string;
  counts: TicketCounts;
  /** The ticket queue; tickets there are not filed into a project yet. */
  queue?: { project_id: string; key: string };
}

export interface AlertSource {
  integration: string;
  alert_type: string;
  resource_name: string;
  resource_id: string;
  evidence: Record<string, string>;
  occurrence_count: number;
  first_received_at: string;
  last_received_at: string;
  previous_task_id?: string;
  previous_task_key?: string;
}
