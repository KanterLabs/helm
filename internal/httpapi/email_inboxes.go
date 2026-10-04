package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// Email inboxes: addresses created in Helm whose mail becomes tickets.
//
//	GET    /api/v1/email-inboxes               inboxes with their addresses
//	POST   /api/v1/email-inboxes               {name, project, assignee?}
//	DELETE /api/v1/email-inboxes/{id}          turn an inbox off
//	POST   /api/v1/email-inboxes/{id}/address  replace its address

// emailInboxView is an inbox with its full address (empty while email is
// turned off).
type emailInboxView struct {
	store.EmailInbox
	Address string `json:"address"`
}

func (s *Server) inboxView(ctx context.Context, inbox store.EmailInbox) (emailInboxView, error) {
	view := emailInboxView{EmailInbox: inbox}
	intake, ok, err := s.Store.ActiveEmailIntake(ctx)
	if err == nil && ok {
		view.Address = intake.InboxAddress(inbox.Tag)
	}
	return view, err
}

func (s *Server) emailInboxes(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if !requireAdmin(w, identity) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		inboxes, err := s.Store.ListEmailInboxes(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		intake, emailOn, err := s.Store.ActiveEmailIntake(r.Context())
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		views := make([]emailInboxView, 0, len(inboxes))
		for _, inbox := range inboxes {
			view := emailInboxView{EmailInbox: inbox}
			if emailOn {
				view.Address = intake.InboxAddress(inbox.Tag)
			}
			views = append(views, view)
		}
		response := map[string]any{"data": views, "email_on": emailOn}
		if emailOn {
			response["domain"] = intake.Domain
		}
		s.writeJSON(w, http.StatusOK, response)
	case http.MethodPost:
		var payload struct {
			Name     string `json:"name"`
			Project  string `json:"project"`
			Assignee string `json:"assignee"`
		}
		if err := decodeJSON(r, &payload); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_json", "request body must be {name, project, assignee?}", nil)
			return
		}
		if _, ok, err := s.Store.ActiveEmailIntake(r.Context()); err != nil {
			s.writeStoreError(w, err)
			return
		} else if !ok {
			s.writeError(w, http.StatusConflict, "email_not_set_up", "Turn on email first: Reach Helm from outside → Sign in with Cloudflare.", nil)
			return
		}
		assignee := strings.TrimSpace(payload.Assignee)
		if assignee == "me" {
			assignee = identity.Actor.ID
		}
		inbox, err := s.Store.CreateEmailInbox(r.Context(), store.EmailInboxInput{Name: payload.Name, ProjectRef: payload.Project, AssigneeID: assignee}, identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		view, err := s.inboxView(r.Context(), inbox)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, view)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}

func (s *Server) emailInbox(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) {
	if !requireAdmin(w, identity) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id := parts[1]
	switch {
	case len(parts) == 2 && r.Method == http.MethodDelete:
		if _, err := s.Store.DisableEmailInbox(r.Context(), id, identity.Actor.ID); err != nil {
			s.writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 3 && r.Method == http.MethodPost:
		inbox, err := s.Store.ReplaceEmailInboxAddress(r.Context(), id, identity.Actor.ID)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		view, err := s.inboxView(r.Context(), inbox)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, view)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
	}
}
