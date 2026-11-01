import type {
  CloudflareConnectStatus,
  CloudflareSetupRun,
  CloudflareZoneOption,
  EmailInbox,
  EmailIntake,
  EmailIntakeView,
  PublicEndpoint,
  PublicEndpointTestResult,
  PublicEndpointView,
  Task,
  TicketCollection,
  TicketQueue,
  TicketTestResult,
  TicketWebhook,
  TicketWebhookFormat,
  TicketWebhookSecret
} from '../types';
import { key, pathWithQuery, request } from './client';

export const ticketsApi = {
  /** New tickets land in the ticket queue's Needs triage. */
  createTicket: (input: { title: string; description?: string; priority?: Task['priority']; assignee?: string | null }) =>
    request<Task>('/tickets', {
      method: 'POST',
      body: input,
      idempotencyKey: key()
    }),

  /** Moves a ticket into a project; it gets that project's next key. */
  fileTicket: (task: string, project: string) =>
    request<Task>(`/tickets/${encodeURIComponent(task)}/file`, { method: 'POST', body: { project } }),

  listTickets: (params: { queue?: TicketQueue; project?: string; q?: string; cursor?: string; limit?: number } = {}, signal?: AbortSignal) =>
    request<TicketCollection>(pathWithQuery('/tickets', params), { signal }),

  listTicketWebhooks: () => request<{ data: TicketWebhook[]; endpoint_base: string; public_hostname: string }>('/ticket-webhooks'),

  listEmailInboxes: () => request<{ data: EmailInbox[]; email_on: boolean; domain?: string }>('/email-inboxes'),

  createEmailInbox: (input: { name: string; assignee?: string }) =>
    request<EmailInbox>('/email-inboxes', { method: 'POST', body: input }),

  disableEmailInbox: (inbox: string) => request<void>(`/email-inboxes/${encodeURIComponent(inbox)}`, { method: 'DELETE' }),

  /** The old address stops working at once. */
  replaceEmailInboxAddress: (inbox: string) =>
    request<EmailInbox>(`/email-inboxes/${encodeURIComponent(inbox)}/address`, { method: 'POST' }),

  /** Helm posts a test ticket for the webhook through its own public URL. */
  sendTestTicket: (webhook: string) =>
    request<TicketTestResult>(`/ticket-webhooks/${encodeURIComponent(webhook)}/test`, { method: 'POST' }),

  getEmailIntake: () => request<EmailIntakeView>('/email-intake'),

  getCloudflareConnect: () => request<CloudflareConnectStatus>('/cloudflare'),

  /** The token is held in server memory only, never stored or returned. */
  connectCloudflareToken: (apiToken: string) =>
    request<CloudflareConnectStatus>('/cloudflare/session', { method: 'POST', body: { api_token: apiToken } }),

  disconnectCloudflare: () => request<CloudflareConnectStatus>('/cloudflare/session', { method: 'DELETE' }),

  startCloudflareOAuth: () => request<{ authorize_url: string }>('/cloudflare/oauth/start', { method: 'POST' }),

  listCloudflareZones: () => request<{ data: CloudflareZoneOption[] }>('/cloudflare/zones'),

  startCloudflareSetup: (input: { zone: string; hostname?: string; email?: { local_part: string; fallback_address?: string; enable_subaddressing: boolean; inbox_name?: string; assign_to_me?: boolean } }) =>
    request<CloudflareSetupRun>('/cloudflare/setup', { method: 'POST', body: input }),

  getCloudflareSetup: (run: string) => request<CloudflareSetupRun>(`/cloudflare/setup/${encodeURIComponent(run)}`),

  /** The Cloudflare API token is sent once and never stored by Helm. */
  createEmailIntake: (input: { domain: string; local_part: string; fallback_address?: string; enable_subaddressing: boolean; api_token: string }) =>
    request<EmailIntakeView>('/email-intake', { method: 'POST', body: input }),

  disableEmailIntake: (intake: string, apiToken = '') =>
    request<{ intake: EmailIntake; warning?: string }>(`/email-intake/${encodeURIComponent(intake)}`, { method: 'DELETE', ...(apiToken ? { body: { api_token: apiToken } } : {}) }),

  /** No idempotency key: the response carries a one-time secret. */
  createTicketWebhook: (input: { name: string; format: TicketWebhookFormat; assignee?: string }) =>
    request<TicketWebhookSecret>('/ticket-webhooks', { method: 'POST', body: input }),

  rotateTicketWebhook: (webhook: string) =>
    request<TicketWebhookSecret>(`/ticket-webhooks/${encodeURIComponent(webhook)}/rotate`, { method: 'POST' }),

  disableTicketWebhook: (webhook: string) =>
    request<void>(`/ticket-webhooks/${encodeURIComponent(webhook)}`, { method: 'DELETE' }),

  getPublicEndpoints: () => request<PublicEndpointView>('/public-endpoints'),

  /** The Cloudflare API token is sent once and never stored by Helm. */
  createPublicEndpoint: (input: { hostname: string; api_token: string }) =>
    request<PublicEndpointView>('/public-endpoints', { method: 'POST', body: { provider: 'cloudflare', ...input } }),

  testPublicEndpoint: (endpoint: string) =>
    request<PublicEndpointTestResult>(`/public-endpoints/${encodeURIComponent(endpoint)}/test`, { method: 'POST' }),

  disablePublicEndpoint: (endpoint: string, apiToken = '') =>
    request<{ endpoint: PublicEndpoint; warning?: string }>(`/public-endpoints/${encodeURIComponent(endpoint)}`, { method: 'DELETE', ...(apiToken ? { body: { api_token: apiToken } } : {}) })
};
