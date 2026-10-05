package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/intake"
	"github.com/KanterLabs/helm/internal/publicendpoint"
	"github.com/KanterLabs/helm/internal/store"
)

// isTicketHookPath matches /api/v1/hooks/tickets/{secret}, the public entry
// point outside apps post to. Like the Coolify route it authenticates with
// the path secret, so any app that can call a URL can open tickets.
func isTicketHookPath(parts []string) bool {
	return len(parts) == 3 && parts[0] == "hooks" && parts[1] == "tickets"
}

type ticketWebhookSecretResponse struct {
	Webhook store.TicketWebhook `json:"webhook"`
	Secret  string              `json:"secret"`
	URL     string              `json:"url"`
}

type ticketHookTicket struct {
	ID  string `json:"id"`
	Key string `json:"key"`
	URL string `json:"url"`
}

type ticketHookResponse struct {
	Disposition     string           `json:"disposition"`
	OccurrenceCount int              `json:"occurrence_count"`
	Ticket          ticketHookTicket `json:"ticket"`
}

// ticketHookBase prefers an active public endpoint so outside apps get a
// reachable URL; otherwise it uses the configured origin.
func (s *Server) ticketHookBase(ctx context.Context) string {
	if base := s.PublicEndpoints.PublicHookBase(ctx); base != "" {
		return base
	}
	return s.Cfg.PublicOrigin + "/api/v1/hooks/tickets/"
}

func (s *Server) ticketHook(w http.ResponseWriter, r *http.Request, secret string) {
	if !validHookSecret(secret) {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	hook, err := s.Store.ResolveTicketWebhook(r.Context(), secret)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	body, ok := s.readIntakeBody(w, r)
	if !ok {
		return
	}
	if hook.Format == store.TicketWebhookCoolify {
		s.ingestCoolify(w, r, body, webhookRoute(hook))
		return
	}
	s.ingestGenericTicket(w, r, hook, body)
}

func webhookRoute(hook store.TicketWebhook) store.AlertIntakeRoute {
	assignee := ""
	if hook.AssigneeID != nil {
		assignee = *hook.AssigneeID
	}
	return store.AlertIntakeRoute{
		Integration: "webhook-" + hook.ID,
		ActorName:   hook.Name,
		AssigneeRef: assignee,
		WebhookID:   hook.ID,
	}
}

// ingestGenericTicket files one generic JSON ticket for hook. Real webhook
// deliveries and test tickets sent through the public URL share it.
func (s *Server) ingestGenericTicket(w http.ResponseWriter, r *http.Request, hook store.TicketWebhook, body []byte) {
	route := webhookRoute(hook)
	alert, err := intake.ParseGenericTicket(body, hook.ID, hook.Name)
	if err != nil {
		details := map[string]any{"allowed_fields": intake.GenericTicketFields}
		var fieldErr *intake.FieldError
		if errors.As(err, &fieldErr) && fieldErr.Field != "" {
			details["field"] = fieldErr.Field
		}
		s.logAlertIntake(route.Integration, "ticket", "rejected", nil)
		s.writeError(w, http.StatusBadRequest, "invalid_ticket", err.Error(), details)
		return
	}
	results, err := s.Store.IngestAlerts(r.Context(), route, []store.IntakeAlert{alert})
	if !s.writeIngestError(w, route.Integration, "ticket", err) {
		return
	}
	s.logAlertIntake(route.Integration, "ticket", "recorded", results)
	result := results[0]
	status := http.StatusOK
	if result.Disposition == "created" {
		status = http.StatusCreated
	}
	s.writeJSON(w, status, ticketHookResponse{
		Disposition:     result.Disposition,
		OccurrenceCount: result.OccurrenceCount,
		Ticket: ticketHookTicket{
			ID:  result.TaskID,
			Key: result.TaskKey,
			URL: s.Cfg.PublicOrigin + "/p/" + hook.ProjectSlug + "/tasks/" + result.TaskKey,
		},
	})
}

func validHookSecret(secret string) bool {
	if len(secret) < 32 || len(secret) > 256 {
		return false
	}
	for _, char := range secret {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

// ticketWebhooks serves GET/POST /api/v1/ticket-webhooks (human admins).
func (s *Server) ticketWebhooks(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !requireAdmin(w, identity) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		hooks, err := s.Store.ListTicketWebhooks(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		publicHostname := ""
		if endpoint, ok, err := s.Store.ActivePublicEndpoint(r.Context()); err != nil {
			s.writeStoreError(w, err)
			return
		} else if ok && s.PublicEndpoints != nil {
			publicHostname = endpoint.Hostname
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"data": hooks, "endpoint_base": s.ticketHookBase(r.Context()), "public_hostname": publicHostname})
	case http.MethodPost:
		if !s.secretResponseAllowed(w, r) {
			return
		}
		var payload struct {
			Name     string `json:"name"`
			Format   string `json:"format"`
			Project  string `json:"project"` // ignored: tickets land in the ticket queue
			Assignee string `json:"assignee"`
		}
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {name, format?, assignee?}", nil)
			return
		}
		assignee := strings.TrimSpace(payload.Assignee)
		if assignee == "me" {
			assignee = identity.Actor.ID
		}
		hook, secret, err := s.Store.CreateTicketWebhook(r.Context(), store.TicketWebhookInput{Name: payload.Name, Format: payload.Format, AssigneeID: assignee}, identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, ticketWebhookSecretResponse{Webhook: hook, Secret: secret, URL: s.ticketHookBase(r.Context()) + secret})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// ticketWebhook serves DELETE /api/v1/ticket-webhooks/{id} and
// POST /api/v1/ticket-webhooks/{id}/rotate|test (human admins).
func (s *Server) ticketWebhook(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) {
	if !requireAdmin(w, identity) {
		return
	}
	id := parts[1]
	switch {
	case len(parts) == 2 && r.Method == http.MethodDelete:
		if err := s.Store.DisableTicketWebhook(r.Context(), id, identity.Actor.ID); err != nil {
			s.writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && parts[2] == "rotate" && r.Method == http.MethodPost:
		if !s.secretResponseAllowed(w, r) {
			return
		}
		hook, secret, err := s.Store.RotateTicketWebhook(r.Context(), id, identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, ticketWebhookSecretResponse{Webhook: hook, Secret: secret, URL: s.ticketHookBase(r.Context()) + secret})
	case len(parts) == 3 && parts[2] == "test" && r.Method == http.MethodPost:
		s.sendTestTicket(w, r, id)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// sendTestTicket serves POST /api/v1/ticket-webhooks/{id}/test: Helm posts
// a test ticket for the webhook through its own public URL.
func (s *Server) sendTestTicket(w http.ResponseWriter, r *http.Request, id string) {
	w.Header().Set("Cache-Control", "no-store")
	if s.publicEndpointsUnavailable(w) {
		return
	}
	hook, err := s.Store.GetTicketWebhook(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if hook.DisabledAt != nil {
		s.writeError(w, http.StatusConflict, "webhook_disabled", "a disabled webhook cannot open tickets", nil)
		return
	}
	result, err := s.PublicEndpoints.SendTestTicket(r.Context(), hook.ID)
	if errors.Is(err, publicendpoint.ErrNoActiveEndpoint) {
		s.writeError(w, http.StatusConflict, "public_endpoint_inactive", "create a Public URL first; the test ticket travels through it", nil)
		return
	}
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}

// testTicketHook files a test ticket that arrived through the public URL
// with a one-time nonce bound to webhookID (hooks listener only).
func (s *Server) testTicketHook(w http.ResponseWriter, r *http.Request, webhookID string) {
	hook, err := s.Store.GetTicketWebhook(r.Context(), webhookID)
	if err != nil || hook.DisabledAt != nil {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	body, ok := s.readIntakeBody(w, r)
	if !ok {
		return
	}
	s.ingestGenericTicket(w, r, hook, body)
}

// secretResponseAllowed mirrors API token issue: the plaintext exists only in
// this response, so it is never cached or stored for idempotent replay.
func (s *Server) secretResponseAllowed(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		s.writeError(w, http.StatusBadRequest, "idempotency_not_supported_for_secret", "Idempotency-Key cannot be used when a response contains a secret", nil)
		return false
	}
	return true
}
