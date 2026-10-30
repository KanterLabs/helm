package httpapi

import (
	"encoding/json"
	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
	"net/http"
	"time"
)

func (s *Server) taskAction(w http.ResponseWriter, r *http.Request, identity auth.Identity, id, action string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if !requireScope(w, identity, "tasks:claim") {
		return
	}
	version, err := parseVersion(r)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	current, err := s.Store.ResolveTaskReference(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	id = current.ID
	if !identity.CanProject(current.ProjectID) {
		s.writeError(w, http.StatusForbidden, "forbidden", "token is not scoped to this project", nil)
		return
	}
	if current.ClaimedBy != nil && *current.ClaimedBy != identity.Actor.ID && !identity.Actor.Admin && action != "claim" && activeTaskClaim(current) {
		s.writeError(w, http.StatusForbidden, "forbidden", "only the claim owner may perform this action", nil)
		return
	}
	var payload struct {
		LeaseSeconds    *int64  `json:"lease_seconds"`
		DurationSeconds *int64  `json:"duration_seconds"`
		Comment         *string `json:"comment"`
		Reason          *string `json:"reason"`
	}
	if r.Body != nil && len(bodyBytes(r)) > 0 {
		fields, err := decodeJSONObject(r, &payload)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
			return
		}
		if err := validateTaskActionFields(fields, action); err != nil {
			writeTaskInputError(w, err)
			return
		}
	}
	duration := time.Duration(0)
	if action == "claim" || action == "renew" {
		seconds, supplied := payload.LeaseSeconds, payload.LeaseSeconds != nil
		if !supplied {
			seconds, supplied = payload.DurationSeconds, payload.DurationSeconds != nil
		}
		if supplied {
			const maxLeaseSeconds = int64(store.MaxTaskClaimDuration / time.Second)
			const minLeaseSeconds = int64(store.MinTaskClaimDuration / time.Second)
			if *seconds < minLeaseSeconds || *seconds > maxLeaseSeconds {
				writeTaskInputError(w, taskInputError("lease duration must be between 30 and 604800 seconds"))
				return
			}
			duration = time.Duration(*seconds) * time.Second
		}
	}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		var task store.Task
		var err error
		switch action {
		case "claim":
			task, err = s.Store.ClaimTask(r.Context(), id, identity.Actor.ID, duration, version)
		case "renew":
			if identity.IsToken {
				task, err = s.Store.RenewTaskWithClaim(r.Context(), id, identity.Actor.ID, duration, version)
			} else {
				task, err = s.Store.RenewTask(r.Context(), id, identity.Actor.ID, duration, version)
			}
		case "release":
			if identity.IsToken {
				task, err = s.Store.ReleaseTaskWithClaim(r.Context(), id, identity.Actor.ID, version)
			} else {
				task, err = s.Store.ReleaseTask(r.Context(), id, identity.Actor.ID, version)
			}
		case "complete":
			comment := ""
			if payload.Comment != nil {
				comment = *payload.Comment
			}
			if identity.IsToken {
				task, err = s.Store.CompleteTaskWithCommentWithClaim(r.Context(), id, identity.Actor.ID, version, comment)
			} else {
				task, err = s.Store.CompleteTaskWithComment(r.Context(), id, identity.Actor.ID, version, comment)
			}
		case "block":
			reason := ""
			if payload.Reason != nil {
				reason = *payload.Reason
			}
			if identity.IsToken {
				task, err = s.Store.BlockTaskWithReasonWithClaim(r.Context(), id, identity.Actor.ID, version, reason)
			} else {
				task, err = s.Store.BlockTaskWithReason(r.Context(), id, identity.Actor.ID, version, reason)
			}
		}
		if err != nil {
			return 0, nil, "", err
		}
		body, err := marshalTaskForIdentity(identity, task)
		if err != nil {
			return 0, nil, "", err
		}
		return http.StatusOK, body, taskETag(task), nil
	})
}

func validateTaskActionFields(payload map[string]json.RawMessage, action string) error {
	for name := range payload {
		allowed := false
		switch action {
		case "claim", "renew":
			allowed = name == "lease_seconds" || name == "duration_seconds"
		case "release":
			allowed = false
		case "complete":
			allowed = name == "comment"
		case "block":
			allowed = name == "reason"
		}
		if !allowed {
			return taskInputError("request body contains fields unsupported by this action")
		}
		if isJSONNull(payload[name]) {
			return taskInputError(name + " cannot be null")
		}
	}
	return nil
}

func activeTaskClaim(task store.Task) bool {
	if task.ClaimedBy == nil || task.ClaimExpiresAt == nil {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, *task.ClaimExpiresAt)
	return err == nil && expires.After(time.Now().UTC())
}
