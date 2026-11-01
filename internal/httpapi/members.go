package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// members serves the explicit human membership directory. Agent credentials
// and pending Cloudflare identities never reach this handler because the
// central authorization gate runs before dispatch.
func (s *Server) members(w http.ResponseWriter, r *http.Request, identity auth.Identity, actorID string) {
	if !requireAdmin(w, identity) {
		return
	}
	if actorID == "" {
		s.membersCollection(w, r, identity)
		return
	}
	if r.Method != http.MethodPatch {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	var payload struct {
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Role        *string  `json:"role"`
		ProjectIDs  []string `json:"project_ids"`
		Disabled    *bool    `json:"disabled"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := rejectJSONNull(fields, "name", "description", "role", "project_ids", "disabled"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if len(fields) == 0 {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "member patch must include at least one field", nil)
		return
	}
	if payload.Name != nil {
		if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Name)), "name", 1, 200, true); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	if payload.Description != nil {
		if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Description)), "description", 0, 10000, false); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	if raw, present := fields["project_ids"]; present {
		if err := validateIdentifierArray(raw, "project_ids", false); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	var projects *[]string
	if _, present := fields["project_ids"]; present {
		projects = &payload.ProjectIDs
	}
	input := store.MemberUpdate{Name: payload.Name, Description: payload.Description, Role: payload.Role, ProjectIDs: projects, Disabled: payload.Disabled}
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		member, err := s.Store.UpdateMember(r.Context(), actorID, input, identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(member)
		return http.StatusOK, body, "", err
	})
}

func (s *Server) membersCollection(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	if r.Method == http.MethodGet {
		limit, offset, err := parsePagination(r, 50)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
			return
		}
		includeDisabled := false
		if value, present := r.URL.Query()["disabled"]; present {
			if len(value) != 1 {
				s.writeError(w, http.StatusBadRequest, "invalid_request", "disabled must be a boolean", nil)
				return
			}
			switch strings.ToLower(strings.TrimSpace(value[0])) {
			case "true":
				includeDisabled = true
			case "false":
			default:
				s.writeError(w, http.StatusBadRequest, "invalid_request", "disabled must be a boolean", nil)
				return
			}
		}
		members, more, err := s.Store.ListMembers(r.Context(), limit, offset, includeDisabled)
		if err != nil {
			s.writeInternal(w, err)
			return
		}
		next := ""
		if more {
			next = encodeCursor(offset + len(members))
		}
		s.writeCollection(w, members, next)
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	if s.idempotencyReplay(w, r, identity) {
		return
	}
	var payload struct {
		Email       *string  `json:"email"`
		Name        *string  `json:"name"`
		Description *string  `json:"description"`
		Role        *string  `json:"role"`
		ProjectIDs  []string `json:"project_ids"`
		Password    *string  `json:"password"`
	}
	fields, err := decodeJSONObject(r, &payload)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return
	}
	if err := requireJSONFields(fields, "email", "name"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if err := rejectJSONNull(fields, "email", "name", "description", "role", "project_ids", "password"); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Email == nil || !auth.ValidEmail(*payload.Email) {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "a valid email is required", nil)
		return
	}
	if payload.Name == nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request", "name is required", nil)
		return
	}
	if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Name)), "name", 1, 200, true); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if payload.Description != nil {
		if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Description)), "description", 0, 10000, false); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	if raw, present := fields["project_ids"]; present {
		if err := validateIdentifierArray(raw, "project_ids", false); err != nil {
			s.writeStoreError(w, err)
			return
		}
	}
	var passwordHash string
	if payload.Password != nil {
		if err := validateStringLength(json.RawMessage(strconv.Quote(*payload.Password)), "password", 12, 0, false); err != nil {
			s.writeStoreError(w, err)
			return
		}
		passwordHash, err = auth.HashPassword(*payload.Password)
		if err != nil {
			s.writeStoreError(w, &store.Error{Kind: store.ErrInvalid, Message: err.Error()})
			return
		}
	}
	name, description, role := strings.TrimSpace(*payload.Name), "", "member"
	if payload.Description != nil {
		description = *payload.Description
	}
	if payload.Role != nil {
		role = *payload.Role
	}
	email := strings.TrimSpace(*payload.Email)
	s.mutation(w, r, identity, func() (int, []byte, string, error) {
		member, err := s.Store.CreateMember(r.Context(), store.Actor{Kind: "human", Name: name, Description: description, Email: &email}, role, payload.ProjectIDs, passwordHash, identity.Actor.ID)
		if err != nil {
			return 0, nil, "", err
		}
		body, err := json.Marshal(member)
		return http.StatusCreated, body, "", err
	})
}
