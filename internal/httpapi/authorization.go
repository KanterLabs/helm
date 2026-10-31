package httpapi

import (
	"errors"
	"net/http"
	"sort"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/store"
)

// scopedProjectIDs is the single ceiling used by collection readers. Nil is
// unrestricted; an allocated empty slice intentionally authorizes no projects.
func scopedProjectIDs(identity auth.Identity) []string {
	if identity.IsToken {
		if !identity.Token.ProjectsScoped {
			return nil
		}
		result := make([]string, 0, len(identity.Token.Projects))
		for id := range identity.Token.Projects {
			result = append(result, id)
		}
		sort.Strings(result)
		return result
	}
	if identity.IsAdmin() {
		return nil
	}
	result := make([]string, len(identity.Actor.ProjectIDs))
	copy(result, identity.Actor.ProjectIDs)
	sort.Strings(result)
	return result
}

// authorizeWorkspace runs before dispatch and idempotency replay. Human
// membership is consulted after verified ingress; it cannot expand ingress.
// Individual handlers retain bearer scope, claim and version enforcement.
func (s *Server) authorizeWorkspace(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) bool {
	if identity.Actor.DisabledAt != nil || !identity.IsWorkspaceMember() {
		s.writeError(w, 403, "membership_required", "workspace membership is required", nil)
		return false
	}
	if len(parts) == 0 {
		return true
	}
	// Workspace administration is a human owner/admin responsibility. Existing
	// scoped-agent project operations remain governed by their original scopes.
	admin := false
	switch parts[0] {
	case "members", "agents", "tokens", "admin", "cloudflare", "public-endpoints", "email-inboxes", "email-intake", "ticket-webhooks":
		admin = true
	case "projects":
		admin = r.Method == http.MethodPost && len(parts) == 1
		if !identity.IsToken && len(parts) == 2 && r.Method == http.MethodPatch {
			admin = true
		}
		if !identity.IsToken && len(parts) == 3 && parts[2] == "columns" && r.Method != http.MethodGet {
			admin = true
		}
	case "columns":
		if !identity.IsToken && r.Method != http.MethodGet {
			admin = true
		}
	}
	if admin && !identity.IsToken && !requireAdmin(w, identity) {
		return false
	}
	// This defense also covers new descendants of current resource routes,
	// including evidence, before any reader or replay can disclose their data.
	if scopedProjectIDs(identity) == nil || len(parts) < 2 {
		return true
	}
	var projectID string
	var err error
	switch parts[0] {
	case "projects":
		project, e := s.Store.GetProject(r.Context(), parts[1])
		projectID, err = project.ID, e
	case "tasks":
		task, e := s.Store.ResolveTaskReference(r.Context(), parts[1])
		if e != nil {
			task, _, e = s.Store.GetTaskForRestore(r.Context(), parts[1])
		}
		projectID, err = task.ProjectID, e
	case "columns":
		column, e := s.Store.GetColumn(r.Context(), parts[1])
		projectID, err = column.ProjectID, e
	case "labels":
		projectID, err = s.labelProject(r, parts[1])
	case "releases":
		release, e := s.Store.GetRelease(r.Context(), parts[1])
		projectID, err = release.ProjectID, e
	case "audits":
		err = s.Store.DB.QueryRowContext(r.Context(), `SELECT project_id FROM audit_runs WHERE id=?`, parts[1]).Scan(&projectID)
	case "audit-findings":
		err = s.Store.DB.QueryRowContext(r.Context(), `SELECT r.project_id FROM audit_findings f JOIN audit_runs r ON r.id=f.audit_id WHERE f.id=?`, parts[1]).Scan(&projectID)
	}
	if err != nil {
		// Labels are hard-deleted. Let the handler enforce write scope and
		// acknowledge an existing empty DELETE receipt; replay hashes still
		// require the same current authority. A missing receipt remains 404.
		if parts[0] == "labels" && r.Method == http.MethodDelete && errors.Is(err, store.ErrNotFound) {
			return true
		}
		s.writeError(w, 404, "not_found", "resource not found", nil)
		return false
	}
	if projectID != "" && !identity.CanProject(projectID) {
		s.writeError(w, 403, "forbidden", "project access is required", nil)
		return false
	}
	return true
}

func (s *Server) dispatchMembership(w http.ResponseWriter, r *http.Request, identity auth.Identity, parts []string) bool {
	if len(parts) == 1 && parts[0] == "members" {
		s.members(w, r, identity, "")
		return true
	}
	if len(parts) == 2 && parts[0] == "members" {
		s.members(w, r, identity, parts[1])
		return true
	}
	if len(parts) == 3 && parts[0] == "members" && parts[2] == "password" {
		s.memberPassword(w, r, identity, parts[1])
		return true
	}
	if len(parts) == 2 && parts[0] == "auth" && parts[1] == "password" {
		s.memberPassword(w, r, identity, "")
		return true
	}
	if len(parts) >= 2 && parts[0] == "auth" && parts[1] == "sessions" {
		if len(parts) == 2 {
			s.sessions(w, r, identity, "")
		} else if len(parts) == 3 {
			s.sessions(w, r, identity, parts[2])
		} else {
			return false
		}
		return true
	}
	if parts[0] == "sessions" && len(parts) <= 2 {
		id := ""
		if len(parts) == 2 {
			id = parts[1]
		}
		s.sessions(w, r, identity, id)
		return true
	}
	return false
}
