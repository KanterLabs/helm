package store

type Actor struct {
	WorkspaceRole string   `json:"workspace_role,omitempty"`
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Email         *string  `json:"email,omitempty"`
	Admin         bool     `json:"admin"`
	DisabledAt    *string  `json:"disabled_at,omitempty"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	ProjectIDs    []string `json:"project_ids,omitempty"`
	Tokens        []Token  `json:"tokens,omitempty"`
}

type Token struct {
	ID         string   `json:"id"`
	AgentID    string   `json:"agent_id,omitempty"`
	ActorID    string   `json:"actor_id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	ProjectIDs []string `json:"project_ids"`
	ExpiresAt  *string  `json:"expires_at,omitempty"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at,omitempty"`
}

const (
	WorkspaceRoleOwner  = "owner"
	WorkspaceRoleAdmin  = "admin"
	WorkspaceRoleMember = "member"
)

func IsWorkspaceAdminRole(role string) bool {
	return role == WorkspaceRoleOwner || role == WorkspaceRoleAdmin
}

// Member is the explicit human membership view returned by the membership
// directory. It intentionally contains no password hash or session secret.
type Member struct {
	Actor
	LastSeenAt *string `json:"last_seen_at,omitempty"`
}

// Session is an operator-visible session descriptor. token_hash is never part
// of this shape; the raw cookie value is intentionally unrecoverable.
type Session struct {
	ID         string  `json:"id"`
	ActorID    string  `json:"actor_id"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt string  `json:"last_seen_at"`
	ExpiresAt  string  `json:"expires_at"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
	IPAddress  string  `json:"ip_address,omitempty"`
	UserAgent  string  `json:"user_agent,omitempty"`
}
