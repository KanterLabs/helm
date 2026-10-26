package githubprovider

// AllowedRepositories returns a copy of the constructor's immutable allowlist.
func (c *Client) AllowedRepositories() []string { return append([]string{}, c.repositories...) }

// AccessAllowed performs no network I/O. It reflects current credential
// availability and permission loss already observed by provider requests.
// Project access and selection are separate caller-owned authorization checks.
func (c *Client) AccessAllowed(repository string) bool {
	repository, err := c.scoped(repository)
	if err != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.denied[repository]
}

// CachedDiagnostics never contacts GitHub. A configured but unchecked token
// is unavailable until endpoint access has actually been verified.
func (c *Client) CachedDiagnostics() Diagnostics {
	_, err := c.credentials()
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.lastDiagnostics
	d.Enabled = c.enabled
	d.Configured = err == nil
	d.AllowedRepositories = c.AllowedRepositories()
	if err != nil {
		d.State = "not_configured"
		d.Message = (&Error{Code: "not_configured"}).Error()
	} else if d.State == "" {
		d.State = "unavailable"
		d.Message = "GitHub access has not been checked."
	}
	return d
}
