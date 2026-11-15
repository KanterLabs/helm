package githubconnect

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Outbound limits (contract C4).
const (
	requestTimeout = 10 * time.Second
	maxBody        = 64 << 10
	// maxListBody bounds one page of installations or repositories. GitHub's
	// repository objects run to several kilobytes each, so a page of 100 does
	// not fit the 64 KB cap the OAuth and /user calls use.
	maxListBody = 2 << 20

	defaultWebOrigin = "https://github.com"
	defaultAPIOrigin = "https://api.github.com"
)

// proxyFromEnvironment is the operator's proxy policy (HTTPS_PROXY,
// NO_PROXY; amendment A1). Tests replace it to prove requests use it.
var proxyFromEnvironment = http.ProxyFromEnvironment

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newHTTPClient is the production client: the operator's proxy, verified
// TLS, a 10-second budget per request and no redirects.
func newHTTPClient(proxy func(*http.Request) (*url.URL, error)) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                  proxy,
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    requestTimeout,
		ResponseHeaderTimeout:  requestTimeout,
		MaxResponseHeaderBytes: maxBody,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           4,
		IdleConnTimeout:        90 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: noRedirect}
}

// confine applies the C4 rules to an injected test client.
func confine(c *http.Client) *http.Client {
	copied := *c
	if copied.Timeout <= 0 || copied.Timeout > requestTimeout {
		copied.Timeout = requestTimeout
	}
	copied.CheckRedirect = noRedirect
	return &copied
}

// normalizeOrigin accepts scheme://host[:port] and nothing more. Only tests
// pass an origin; production always uses the GitHub defaults.
func normalizeOrigin(raw, fallback string) (string, bool) {
	if raw == "" {
		return fallback, true
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

type reply struct {
	status int
	body   []byte
}

// send performs one request and reads at most limit bytes. Every failure is
// github_unreachable; bodies never reach an error.
func (m *Manager) send(request *http.Request, limit int64) (*reply, error) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Helm")
	response, err := m.http.Do(request)
	if err != nil {
		return nil, fail(CodeGitHubUnreachable)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, fail(CodeGitHubUnreachable)
	}
	return &reply{status: response.StatusCode, body: body}, nil
}

// postForm posts to the web origin (device code and token endpoints).
func (m *Manager) postForm(ctx context.Context, path string, form url.Values) (*reply, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.web+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fail(CodeGitHubUnreachable)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return m.send(request, maxBody)
}

// getAPI calls the API origin with token.
func (m *Manager) getAPI(ctx context.Context, path, token string, limit int64) (*reply, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, m.api+path, nil)
	if err != nil {
		return nil, fail(CodeGitHubUnreachable)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return m.send(request, limit)
}
