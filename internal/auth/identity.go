package auth

import (
	"github.com/KanterLabs/helm/internal/store"
)

type Identity struct {
	Actor   store.Actor
	IsToken bool
	Token   store.AuthToken
	// SessionID is populated for local cookie identities and is never a secret.
	SessionID string
	// DevelopmentBypass is true only for the explicitly configured disabled
	// authentication mode. It keeps existing local development fixtures usable
	// without making the bypass available to Cloudflare or bearer callers.
	DevelopmentBypass bool
}

func (i Identity) HasScope(scope string) bool {
	if i.Actor.DisabledAt != nil {
		return false
	}
	if i.IsToken {
		return i.Token.Scopes[scope]
	}
	return i.DevelopmentBypass || i.IsWorkspaceMember()
}

func (i Identity) CanProject(projectID string) bool {
	if i.Actor.DisabledAt != nil {
		return false
	}
	if i.DevelopmentBypass {
		return true
	}
	if i.IsToken {
		if !i.Token.ProjectsScoped {
			return true
		}
		return i.Token.Projects[projectID]
	}
	if i.IsAdmin() {
		return true
	}
	for _, allowed := range i.Actor.ProjectIDs {
		if allowed == projectID {
			return true
		}
	}
	return false
}

func (i Identity) IsWorkspaceMember() bool {
	return i.Actor.DisabledAt == nil && (i.IsToken || i.DevelopmentBypass || (i.Actor.Kind == "human" && i.Actor.WorkspaceRole != ""))
}

func (i Identity) IsAdmin() bool {
	return i.Actor.DisabledAt == nil && !i.IsToken && i.Actor.Kind == "human" && (i.DevelopmentBypass || store.IsWorkspaceAdminRole(i.Actor.WorkspaceRole))
}
