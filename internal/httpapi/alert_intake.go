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
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if r.ContentLength > intake.MaxCoolifyBodyBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "alert body exceeds 64 KiB", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, intake.MaxCoolifyBodyBytes+1))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_body", "could not read request body", nil)
		return
	}
	if len(body) > intake.MaxCoolifyBodyBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "alert body exceeds 64 KiB", nil)
		return
	}
	delivery, err := intake.ParseCoolify(body)
	if err != nil {
		s.logAlertIntake("", "rejected", nil)
		s.writeError(w, http.StatusBadRequest, "invalid_alert", err.Error(), nil)
		return
	}
	response := alertIntakeResponse{Event: delivery.Event, Disposition: delivery.Disposition, Alerts: []store.AlertDisposition{}}
	if len(delivery.Alerts) == 0 {
		s.logAlertIntake(delivery.Event, delivery.Disposition, nil)
		s.writeJSON(w, http.StatusOK, response)
		return
	}
	results, err := s.Store.IngestAlerts(r.Context(), store.AlertIntakeRoute{
		Integration: intake.CoolifyIntegration,
		ActorName:   "Coolify",
		ProjectRef:  cfg.Project,
		AssigneeRef: cfg.Assignee,
	}, delivery.Alerts)
	if errors.Is(err, store.ErrIntakeRouting) {
		s.logAlertIntake(delivery.Event, "routing_unavailable", nil)
		s.writeError(w, http.StatusServiceUnavailable, "intake_unavailable", err.Error(), nil)
		return
	}
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	response.Disposition = "recorded"
	response.Alerts = results
	s.logAlertIntake(delivery.Event, response.Disposition, results)
	s.writeJSON(w, http.StatusOK, response)
}

// logAlertIntake records sanitized outcome counts only: never the secret,
// task text or resource names.
func (s *Server) logAlertIntake(event, disposition string, results []store.AlertDisposition) {
	fields := map[string]any{
		"level":       "info",
		"msg":         "alert intake",
		"integration": intake.CoolifyIntegration,
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
