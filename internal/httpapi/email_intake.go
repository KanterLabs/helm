package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/intake"
	"github.com/KanterLabs/helm/internal/publicendpoint"
	"github.com/KanterLabs/helm/internal/store"
)

// isEmailHookPath matches /api/v1/hooks/tickets/email/{secret}: the route
// Helm's Cloudflare email Worker posts raw messages to. It sits under the
// ticket-hook ingress, so public tunnels need no change.
func isEmailHookPath(parts []string) bool {
	return len(parts) == 4 && parts[0] == "hooks" && parts[1] == "tickets" && parts[2] == "email"
}

func boundedText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit-1]) + "…"
}

// emailHook turns one message from the Worker into a ticket. Refusals are
// recorded so admins can see mail that bounced; their reasons are what the
// Worker bounces to the sender.
func (s *Server) emailHook(w http.ResponseWriter, r *http.Request, secret string) {
	if !validHookSecret(secret) {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	emailIntake, err := s.Store.ResolveEmailIntake(r.Context(), secret)
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	envelope := intake.EmailEnvelope{
		From:        r.Header.Get("X-Helm-Envelope-From"),
		To:          r.Header.Get("X-Helm-Envelope-To"),
		AuthResults: r.Header.Get("X-Helm-Authentication-Results"),
		ReceivedAt:  time.Now().UTC(),
	}
	inboxID := ""
	var inbox store.EmailInbox
	if tag, ok := intake.ParseEmailRecipient(envelope.To, emailIntake.LocalPart, emailIntake.Domain); ok {
		if found, err := s.Store.ResolveEmailInbox(r.Context(), tag); err == nil {
			inbox, inboxID = found, found.ID
		} else if !errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, err)
			return
		}
	}
	sender := boundedText(envelope.From, 200)
	refuse := func(status int, outcome, code, message, key, subject string) {
		receipt := store.IntakeReceipt{IntakeID: emailIntake.ID, Key: key, Sender: sender, Subject: boundedText(subject, 300)}
		if err := s.Store.RecordEmailRefusal(r.Context(), receipt, inboxID, outcome, message); err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.logAlertIntake("email-"+emailIntake.ID, "email", outcome, nil)
		s.writeError(w, status, code, message, nil)
	}

	if size := r.Header.Get("X-Helm-Oversize"); size != "" {
		bytes, _ := strconv.Atoi(size)
		key := "oversize:" + r.Header.Get("X-Helm-Message-Id") + "\n" + envelope.To + "\n" + size
		refuse(http.StatusRequestEntityTooLarge, store.EmailOutcomeTooLarge, "request_too_large",
			fmt.Sprintf("Message is %s; Helm accepts email up to 1 MiB", humanBytes(bytes)), key, r.Header.Get("X-Helm-Subject"))
		return
	}
	if r.ContentLength > intake.MaxEmailBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Helm accepts email up to 1 MiB", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, intake.MaxEmailBytes+1))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_body", "could not read the message", nil)
		return
	}
	parsed, parseErr := intake.ParseEmail(body, envelope)
	if parseErr == nil {
		// The header From is what people recognize; bulk senders' envelope
		// senders are bounce addresses.
		sender = boundedText(parsed.FromDisplay, 200)
	}
	if inboxID == "" {
		subject, key := parsed.Subject, parsed.ReceiptKey
		if parseErr != nil {
			key = "unknown:" + envelope.To + "\n" + string(body[:min(len(body), 4096)])
		}
		refuse(http.StatusNotFound, store.EmailOutcomeUnknownRecipient, "unknown_recipient", "No Helm inbox uses this address", key, subject)
		return
	}
	if parseErr != nil {
		refuse(http.StatusBadRequest, store.EmailOutcomeUnreadable, "unreadable_email", strings.TrimPrefix(parseErr.Error(), intake.ErrUnreadableEmail.Error()+": "), "unreadable:"+string(body[:min(len(body), 4096)]), "")
		return
	}
	assignee := ""
	if inbox.AssigneeID != nil {
		assignee = *inbox.AssigneeID
	}
	route := store.AlertIntakeRoute{
		Integration: "inbox-" + inbox.ID,
		ActorName:   inbox.Name,
		ProjectRef:  inbox.ProjectID,
		AssigneeRef: assignee,
		InboxID:     inbox.ID,
		Receipt:     &store.IntakeReceipt{IntakeID: emailIntake.ID, Key: parsed.ReceiptKey, Sender: boundedText(parsed.FromDisplay, 200), Subject: parsed.Subject},
	}
	results, err := s.Store.IngestAlerts(r.Context(), route, []store.IntakeAlert{parsed.Alert(inbox.ID, inbox.Name)})
	if !s.writeIngestError(w, route.Integration, "email", err) {
		return
	}
	s.logAlertIntake(route.Integration, "email", "recorded", results)
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
			URL: s.Cfg.PublicOrigin + "/p/" + inbox.ProjectSlug + "/tasks/" + result.TaskKey,
		},
	})
}

func humanBytes(size int) string {
	if size >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", size/1024)
}

type emailIntakeRequest struct {
	Domain              string `json:"domain"`
	LocalPart           string `json:"local_part"`
	FallbackAddress     string `json:"fallback_address"`
	EnableSubaddressing bool   `json:"enable_subaddressing"`
	APIToken            string `json:"api_token"`
}

func (s *Server) writeEmailIntakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publicendpoint.ErrSubaddressingConsent):
		s.writeError(w, http.StatusConflict, "subaddressing_consent_required", err.Error(), map[string]any{"field": "enable_subaddressing"})
	case errors.Is(err, publicendpoint.ErrEmailPrerequisite):
		s.writeError(w, http.StatusConflict, "email_prerequisite", strings.TrimPrefix(err.Error(), publicendpoint.ErrEmailPrerequisite.Error()+": "), nil)
	default:
		s.writePublicEndpointError(w, err)
	}
}

// emailIntakes serves GET/POST /api/v1/email-intake (human admins).
func (s *Server) emailIntakes(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		view, err := s.PublicEndpoints.EmailView(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, view)
	case http.MethodPost:
		// The request carries a Cloudflare API token; never replay or cache.
		if !s.secretResponseAllowed(w, r) {
			return
		}
		var payload emailIntakeRequest
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {domain, local_part, fallback_address?, enable_subaddressing?, api_token}", nil)
			return
		}
		if _, err := s.PublicEndpoints.ProvisionEmail(r.Context(), publicendpoint.EmailSetup{
			Domain: payload.Domain, LocalPart: payload.LocalPart, FallbackAddress: payload.FallbackAddress,
			EnableSubaddressing: payload.EnableSubaddressing, APIToken: payload.APIToken,
		}, identity.Actor.ID); err != nil {
			s.writeEmailIntakeError(w, err)
			return
		}
		view, err := s.PublicEndpoints.EmailView(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, view)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

// emailIntake serves DELETE /api/v1/email-intake/{id}. An optional
// {api_token} also deletes the Worker and routing rule.
func (s *Server) emailIntake(w http.ResponseWriter, r *http.Request, identity auth.Identity, id string) {
	if !requireAdmin(w, identity) || s.publicEndpointsUnavailable(w) {
		return
	}
	if r.Method != http.MethodDelete {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !s.secretResponseAllowed(w, r) {
		return
	}
	var payload emailIntakeRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be empty or {api_token}", nil)
			return
		}
	}
	emailIntake, err := s.PublicEndpoints.DisableEmail(r.Context(), id, payload.APIToken, identity.Actor.ID)
	if err != nil && emailIntake.ID == "" {
		s.writeEmailIntakeError(w, err)
		return
	}
	response := map[string]any{"intake": emailIntake}
	if err != nil {
		response["warning"] = "Email stopped in Helm, but Cloudflare cleanup failed: " + err.Error()
	} else if emailIntake.CleanupPending {
		response["warning"] = "Email stopped in Helm. Delete routing rule " + emailIntake.RuleID + " and Worker " + emailIntake.WorkerName + " in Cloudflare, or remove again with an API token."
	}
	s.writeJSON(w, http.StatusOK, response)
}
