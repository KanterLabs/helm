package httpapi

// Route labels for the Gitea connection (contract C5). They are registered
// here rather than in observability.go so that file does not grow. Only the
// path is ever labelled: the callback's code and state live in the query
// string, which metricRoute drops.
func init() {
	for _, route := range []string{
		"/api/v1/integrations/gitea",
		"/api/v1/integrations/gitea/probe",
		"/api/v1/integrations/gitea/oauth/start",
		"/api/v1/integrations/gitea/oauth/callback",
		"/api/v1/integrations/gitea/check",
		"/api/v1/integrations/gitea/repositories",
		"/api/v1/integrations/gitea/disconnect",
	} {
		metricRouteTemplates[route] = struct{}{}
	}
}
