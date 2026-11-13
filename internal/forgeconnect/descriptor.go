package forgeconnect

// Scope is the fixed grant Helm asks for. It is never built from input and
// never includes openid: identity comes from the API.
const Scope = "read:repository read:user"

// descriptor holds everything forge-specific. The sign-in, refresh and check
// flow reads only these values, so another forge kind is a new descriptor.
type descriptor struct {
	Kind          string
	Scope         string
	DiscoveryPath string
	AuthorizePath string
	TokenPath     string
	UserPath      string
	VersionPath   string
	// LimitCheckPath needs a scope Helm does not ask for: 403 proves the
	// grant is limited, 200 means full access.
	LimitCheckPath string
	ReposPath      string
	RepoPageSize   int
	MaxRepoPages   int
	SettingsPath   string
	// ForkMarker in the version string identifies Forgejo.
	ForkMarker   string
	ForkProduct  string
	Product      string
	CallbackPath string
}

var gitea = descriptor{
	Kind:           "gitea",
	Scope:          Scope,
	DiscoveryPath:  "/.well-known/openid-configuration",
	AuthorizePath:  "/login/oauth/authorize",
	TokenPath:      "/login/oauth/access_token",
	UserPath:       "/api/v1/user",
	VersionPath:    "/api/v1/version",
	LimitCheckPath: "/api/v1/user/orgs?limit=1",
	ReposPath:      "/api/v1/user/repos",
	RepoPageSize:   50,
	MaxRepoPages:   10,
	SettingsPath:   "/user/settings/applications",
	ForkMarker:     "+gitea-",
	ForkProduct:    "forgejo",
	Product:        "gitea",
	CallbackPath:   "/api/v1/integrations/gitea/oauth/callback",
}
