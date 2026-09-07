package auth

// Copy configuration at construction so a caller cannot later widen a live
// manager's host bindings by mutating the original map or its slices.
func cloneHostAudiences(bindings map[string][]string) map[string][]string {
	if len(bindings) == 0 {
		return nil
	}
	result := make(map[string][]string, len(bindings))
	for host, audiences := range bindings {
		result[host] = append([]string(nil), audiences...)
	}
	return result
}

func cloudflareHostAudienceAllowed(bindings map[string][]string, host string, verified []string) bool {
	if len(bindings) == 0 {
		return true // Existing single-host deployments retain their verifier policy.
	}
	// Deliberately use the actual request Host, including any explicit port.
	// Forwarded headers must never select a different identity trust boundary.
	for _, allowed := range bindings[host] {
		for _, asserted := range verified {
			if allowed == asserted {
				return true
			}
		}
	}
	return false
}
