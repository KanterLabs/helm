package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"

	"github.com/KanterLabs/helm/internal/intake"
	"github.com/KanterLabs/helm/internal/store"
)

// isCoolifyIntakePath matches /api/v1/intake/coolify/{secret}. The route
// authenticates with the path secret alone because Coolify webhooks can only
// be configured with a URL: they carry no signature or custom headers.
func isCoolifyIntakePath(parts []string) bool {
	return len(parts) == 3 && parts[0] == "intake" && parts[1] == intake.CoolifyIntegration
}

func coolifySecretMatches(expected []byte, presented string) bool {
	// Hashing first makes the comparison constant-time regardless of length.
	want := sha256.Sum256(expected)
	got := sha256.Sum256([]byte(presented))
	return len(expected) > 0 && subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

type alertIntakeResponse struct {
	Event       string                   `json:"event"`
	Disposition string                   `json:"disposition"`
	Alerts      []store.AlertDisposition `json:"alerts"`
}

func (s *Server) coolifyIntake(w http.ResponseWriter, r *http.Request, secret string) {
	cfg := s.Cfg.CoolifyIntake
	// Disabled intake and a wrong secret are indistinguishable from an
	// unknown route so the endpoint is not an oracle for its own existence.
	if !cfg.Enabled() || !coolifySecretMatches(cfg.Secret, secret) {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	body, ok := s.readIntakeBody(w, r)
	if !ok {
		return
	}
	s.ingestCoolify(w, r, body, store.AlertIntakeRoute{
		Integration: intake.CoolifyIntegration,
		ActorName:   "Coolify",
		AssigneeRef: cfg.Assignee,
	})
}

// readIntakeBody enforces POST and the 64 KiB intake body limit.
func (s *Server) readIntakeBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return nil, false
	}
	if r.ContentLength > intake.MaxCoolifyBodyBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "alert body exceeds 64 KiB", nil)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, intake.MaxCoolifyBodyBytes+1))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_body", "could not read request body", nil)
		return nil, false
	}
	if len(body) > intake.MaxCoolifyBodyBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "alert body exceeds 64 KiB", nil)
		return nil, false
	}
	return body, true
}

func (s *Server) ingestCoolify(w http.ResponseWriter, r *http.Request, body []byte, route store.AlertIntakeRoute) {
	delivery, err := intake.ParseCoolify(body)
	if err != nil {
		s.logAlertIntake(route.Integration, "", "rejected", nil)
		s.writeError(w, http.StatusBadRequest, "invalid_alert", err.Error(), nil)
		return
	}
	response := alertIntakeResponse{Event: delivery.Event, Disposition: delivery.Disposition, Alerts: []store.AlertDisposition{}}
	if len(delivery.Alerts) == 0 {
		s.logAlertIntake(route.Integration, delivery.Event, delivery.Disposition, nil)
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	results, err := s.Store.IngestAlerts(r.Context(), route, delivery.Alerts)
	if !s.writeIngestError(w, route.Integration, delivery.Event, err) {
		return
	}
	response.Disposition = "recorded"
	response.Alerts = results
	s.logAlertIntake(route.Integration, delivery.Event, response.Disposition, results)
	s.writeJSON(w, http.StatusOK, response)
}

// writeIngestError writes the response for a failed ingest and reports
// whether the caller may continue.
func (s *Server) writeIngestError(w http.ResponseWriter, integration, event string, err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, store.ErrIntakeRouting) {
		s.logAlertIntake(integration, event, "routing_unavailable", nil)
		s.writeError(w, http.StatusServiceUnavailable, "intake_unavailable", err.Error(), nil)
		return false
	}
	s.writeStoreError(w, err)
	return false
}

// logAlertIntake records sanitized outcome counts only: never the secret,
// task text or resource names.
func (s *Server) logAlertIntake(integration, event, disposition string, results []store.AlertDisposition) {
	fields := map[string]any{
		"level":       "info",
		"msg":         "alert intake",
		"integration": integration,
		"event":       event,
		"disposition": disposition,
	}
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Disposition]++
	}
	for _, key := range []string{"created", "repeated", "retained"} {
		fields[key] = counts[key]
	}
	s.logJSON(fields)
}
