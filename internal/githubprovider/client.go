package githubprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxPayload      = 2 * 1024 * 1024
	maxCacheBytes   = 8 * 1024 * 1024
	maxCacheEntries = 128
	maxPages        = 3
)

type cacheEntry struct {
	body []byte
	etag string
	more bool
}

type Client struct {
	enabled           bool
	tokenFile         string
	repositories      []string
	allowed           map[string]bool
	http              *http.Client
	endpoint          string
	mu                sync.Mutex
	generation        uint64
	fingerprint       [32]byte
	credentialPresent bool
	cache             map[string]cacheEntry
	cacheBytes        int
	snapshots         map[string]PullRequestSnapshot
	backoffUntil      time.Time
	denied            map[string]bool
	lastDiagnostics   Diagnostics
}

// New never contacts GitHub or makes the optional integration a startup gate.
func New(options Options) *Client {
	c := &Client{enabled: options.Enabled, tokenFile: options.TokenFile, allowed: map[string]bool{}, endpoint: "https://api.github.com", generation: 1, cache: map[string]cacheEntry{}, snapshots: map[string]PullRequestSnapshot{}, denied: map[string]bool{}}
	for _, raw := range options.Repositories {
		repository, err := NormalizeRepository(raw)
		if err != nil || len(c.allowed) >= 50 {
			continue
		}
		if !c.allowed[repository] {
			c.allowed[repository] = true
			c.repositories = append(c.repositories, repository)
		}
	}
	sort.Strings(c.repositories)
	hc := http.Client{Timeout: 5 * time.Second}
	if options.HTTPClient != nil {
		hc = *options.HTTPClient
		if hc.Timeout <= 0 || hc.Timeout > 5*time.Second {
			hc.Timeout = 5 * time.Second
		}
		// The override requires explicit code injection and an origin without
		// credentials, paths or query parameters. There is no runtime setting.
		if u, err := url.Parse(options.Endpoint); err == nil && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.Path == "" && ((u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) || u.Scheme == "https" && u.Host != "") {
			c.endpoint = u.String()
		}
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.http = &hc
	return c
}

func (c *Client) resetLocked() {
	c.generation++
	c.cache = map[string]cacheEntry{}
	c.cacheBytes = 0
	c.snapshots = map[string]PullRequestSnapshot{}
	c.backoffUntil = time.Time{}
}

func (c *Client) credentials() (string, error) {
	token, err := readToken(c.tokenFile)
	if !c.enabled || len(c.repositories) == 0 {
		token = ""
		err = providerError("not_configured")
	}
	fingerprint := sha256.Sum256([]byte(token))
	c.mu.Lock()
	present := err == nil
	if present != c.credentialPresent || fingerprint != c.fingerprint {
		c.resetLocked()
		c.denied = map[string]bool{}
		c.lastDiagnostics = Diagnostics{}
		c.credentialPresent = present
		c.fingerprint = fingerprint
	}
	c.mu.Unlock()
	if err != nil {
		return "", providerError("not_configured")
	}
	return token, nil
}

func readToken(filename string) (string, error) {
	if filename == "" {
		return "", providerError("not_configured")
	}
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() < 1 || info.Size() > 4096 {
		return "", providerError("not_configured")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() && stat.Uid != 0 {
		return "", providerError("not_configured")
	}
	file, err := os.Open(filename)
	if err != nil {
		return "", providerError("not_configured")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Mode().Perm()&0077 != 0 {
		return "", providerError("not_configured")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return "", providerError("not_configured")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, " \t\r\n\x00") {
		return "", providerError("not_configured")
	}
	return token, nil
}

// Generation observes file rotation/removal without exposing its fingerprint.
// Callers must additionally fence their own project selection and actor access.
func (c *Client) Generation() uint64 {
	_, _ = c.credentials()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *Client) scoped(repository string) (string, error) {
	normalized, err := NormalizeRepository(repository)
	if err != nil {
		return "", err
	}
	if !c.allowed[normalized] {
		return "", providerError("repository_not_allowed")
	}
	if _, err = c.credentials(); err != nil {
		return "", err
	}
	return normalized, nil
}

func (c *Client) sameGeneration(generation uint64) bool { return c.Generation() == generation }

func (c *Client) deny(repository string, generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return
	}
	// Permission loss invalidates all cached private data from this credential.
	// A subsequent authorized fetch must establish fresh access.
	c.resetLocked()
	c.denied[repository] = true
	c.lastDiagnostics = Diagnostics{State: "permission_required", Message: (&Error{Code: "permission_required"}).Error(), CheckedAt: time.Now().UTC()}
}

func (c *Client) get(ctx context.Context, repository, relative string, target any) (bool, error) {
	return c.getWithPolicy(ctx, repository, relative, target, false)
}

// Code-search discovery is optional and can have a separate permission boundary.
// Its 403/404 cannot revoke verified Contents access; a 401 always rejects the
// credential. Every ordinary source/PR/check request keeps fail-closed behavior.
func (c *Client) getWithPolicy(ctx context.Context, repository, relative string, target any, optionalSearch bool) (bool, error) {
	token, err := c.credentials()
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	generation := c.generation
	if !c.credentialPresent || c.fingerprint != sha256.Sum256([]byte(token)) {
		c.mu.Unlock()
		return false, providerError("unavailable")
	}
	if time.Now().Before(c.backoffUntil) {
		c.mu.Unlock()
		return false, providerError("rate_limited")
	}
	cached, hasCache := c.cache[relative]
	c.mu.Unlock()
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, c.endpoint+relative, nil)
	if err != nil {
		return false, providerError("invalid_input")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Helm-GitHub-read-only-beta")
	if hasCache && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	response, err := c.http.Do(req)
	if err != nil {
		c.transientBackoff(generation)
		return false, providerError("unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == 429 || (response.StatusCode == 403 && (response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "")) {
		delay := time.Minute
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
		if reset, err := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && reset > time.Now().Unix() {
			delay = time.Until(time.Unix(reset, 0))
		}
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
		if delay < time.Second {
			delay = time.Second
		}
		c.mu.Lock()
		if c.generation == generation {
			c.backoffUntil = time.Now().Add(delay)
		}
		c.mu.Unlock()
		return false, providerError("rate_limited")
	}
	if response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 {
		if response.StatusCode == 401 || !optionalSearch || !strings.HasPrefix(relative, "/search/code?") {
			c.deny(repository, generation)
		} else {
			c.mu.Lock()
			if c.generation == generation {
				if old, ok := c.cache[relative]; ok {
					c.cacheBytes -= len(old.body)
					delete(c.cache, relative)
				}
			}
			c.mu.Unlock()
		}
		return false, providerError("permission_required")
	}
	var data []byte
	if response.StatusCode == http.StatusNotModified && hasCache {
		data = cached.body
	} else if response.StatusCode == http.StatusOK {
		data, err = io.ReadAll(io.LimitReader(response.Body, maxPayload+1))
		if err != nil || len(data) > maxPayload || bytes.Contains(data, []byte(token)) {
			return false, providerError("unavailable")
		}
	} else {
		if response.StatusCode >= 500 {
			c.transientBackoff(generation)
		}
		return false, providerError("unavailable")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return false, providerError("unavailable")
	}
	if err = json.Unmarshal(data, target); err != nil {
		return false, providerError("unavailable")
	}
	if !c.sameGeneration(generation) {
		return false, providerError("unavailable")
	}
	c.mu.Lock()
	if c.generation == generation && response.StatusCode == http.StatusOK {
		if len(c.cache) >= maxCacheEntries || c.cacheBytes+len(data) > maxCacheBytes {
			c.cache = map[string]cacheEntry{}
			c.cacheBytes = 0
		}
		if old, ok := c.cache[relative]; ok {
			c.cacheBytes -= len(old.body)
		}
		c.cache[relative] = cacheEntry{body: data, etag: response.Header.Get("ETag"), more: strings.Contains(response.Header.Get("Link"), `rel="next"`)}
		c.cacheBytes += len(data)
	}
	c.mu.Unlock()
	// Link is used only as a coverage signal; never follow an upstream URL.
	if response.StatusCode == http.StatusNotModified {
		return cached.more, nil
	}
	return strings.Contains(response.Header.Get("Link"), `rel="next"`), nil
}

func (c *Client) transientBackoff(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation == generation {
		c.backoffUntil = time.Now().Add(2*time.Second + time.Duration(time.Now().Nanosecond()%1000)*time.Millisecond)
	}
}

func (c *Client) Diagnostics(ctx context.Context) Diagnostics {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	d := Diagnostics{Enabled: c.enabled, State: "not_configured", Message: (&Error{Code: "not_configured"}).Error(), AllowedRepositories: append([]string{}, c.repositories...), CheckedAt: time.Now().UTC()}
	defer func() { c.mu.Lock(); c.lastDiagnostics = d; c.mu.Unlock() }()
	if _, err := c.credentials(); err != nil {
		return d
	}
	d.Configured = true
	generation := c.Generation()
	for _, repository := range c.repositories {
		var metadata struct {
			ID            int64  `json:"id"`
			DefaultBranch string `json:"default_branch"`
		}
		_, err := c.get(ctx, repository, "/repos/"+repository, &metadata)
		if err == nil && (!validRef(metadata.DefaultBranch) || metadata.ID <= 0) {
			err = providerError("unavailable")
		}
		if err == nil {
			var resolved ResolvedRef
			resolved, err = c.Resolve(ctx, repository, metadata.DefaultBranch)
			if err == nil {
				var result any
				for _, suffix := range []string{"/pulls?per_page=1", "/commits/" + resolved.SHA + "/check-runs?per_page=1", "/commits/" + resolved.SHA + "/statuses?per_page=1"} {
					_, err = c.get(ctx, repository, "/repos/"+repository+suffix, &result)
					if err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			d.State = ErrorCode(err)
			d.Message = err.Error()
			return d
		}
	}
	if !c.sameGeneration(generation) {
		d.State = "unavailable"
		d.Message = (&Error{Code: "unavailable"}).Error()
		return d
	}
	d.State = "connected"
	d.Message = "GitHub read access verified for configured repositories."
	c.mu.Lock()
	c.denied = map[string]bool{}
	c.mu.Unlock()
	return d
}
