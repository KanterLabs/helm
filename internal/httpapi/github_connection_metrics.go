package httpapi

// The GitHub integration routes get their own metric labels. They are added
// here rather than in observability.go, which is over the file size limit.
func init() {
	for _, route := range []string{
		"/api/v1/integrations/github",
		"/api/v1/integrations/github/check",
		"/api/v1/integrations/github/connect",
		"/api/v1/integrations/github/connect/cancel",
		"/api/v1/integrations/github/disconnect",
	} {
		metricRouteTemplates[route] = struct{}{}
	}
}
