// Package publicendpoint exposes Helm's webhook routes on a public HTTPS
// hostname through a Cloudflare Tunnel, so self-hosted installs need no open
// ports, DNS server or certificates. Only webhook paths are ever routed.
package publicendpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultCloudflareAPI is the production Cloudflare v4 API.
const DefaultCloudflareAPI = "https://api.cloudflare.com/client/v4"

// HookPathPattern is the only path family a public endpoint routes.
const HookPathPattern = `^/api/v1/(hooks/tickets|intake/coolify)/`

// ErrCloudflare marks a failure reported by the Cloudflare API; its message
// is safe to show an administrator (it never contains the token).
var ErrCloudflare = errors.New("cloudflare request failed")

type cloudflareClient struct {
	base  string
	token string
	http  *http.Client
}

type cloudflareEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfMessage     `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newCloudflareClient(base, token string) *cloudflareClient {
	return &cloudflareClient{base: strings.TrimRight(base, "/"), token: token, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *cloudflareClient) call(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%w: Cloudflare API unreachable", ErrCloudflare)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("%w: could not read Cloudflare response", ErrCloudflare)
	}
	var envelope cloudflareEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("%w: unexpected Cloudflare response (HTTP %d)", ErrCloudflare, response.StatusCode)
	}
	if !envelope.Success {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, fmt.Sprintf("%s (code %d)", item.Message, item.Code))
		}
		if len(messages) == 0 {
			messages = append(messages, fmt.Sprintf("HTTP %d", response.StatusCode))
		}
		return fmt.Errorf("%w: %s %s: %s", ErrCloudflare, method, redactPath(path), strings.Join(messages, "; "))
	}
	if result != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, result)
	}
	return nil
}

// redactPath keeps resource kinds but drops query strings from messages.
func redactPath(path string) string {
	return strings.SplitN(path, "?", 2)[0]
}

type cfZone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

// findZone resolves the most specific zone the token can see for hostname.
func (c *cloudflareClient) findZone(ctx context.Context, hostname string) (cfZone, error) {
	labels := strings.Split(hostname, ".")
	for index := 1; index < len(labels)-1; index++ {
		candidate := strings.Join(labels[index:], ".")
		var zones []cfZone
		if err := c.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil, &zones); err != nil {
			return cfZone{}, err
		}
		if len(zones) == 1 && zones[0].Account.ID != "" {
			return zones[0], nil
		}
	}
	return cfZone{}, fmt.Errorf("%w: the token cannot see a Cloudflare zone for %s (needs Zone:Read and DNS:Edit on that zone)", ErrCloudflare, hostname)
}

func (c *cloudflareClient) createTunnel(ctx context.Context, accountID, name string) (string, error) {
	var tunnel struct {
		ID string `json:"id"`
	}
	err := c.call(ctx, http.MethodPost, "/accounts/"+accountID+"/cfd_tunnel", map[string]any{"name": name, "config_src": "cloudflare"}, &tunnel)
	if err == nil && tunnel.ID == "" {
		err = fmt.Errorf("%w: tunnel response had no ID", ErrCloudflare)
	}
	return tunnel.ID, err
}

func (c *cloudflareClient) tunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	err := c.call(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/token", nil, &token)
	if err == nil && token == "" {
		err = fmt.Errorf("%w: tunnel token response was empty", ErrCloudflare)
	}
	return token, err
}

// putIngress routes only webhook paths for hostname to the local hook
// listener; every other request gets a 404 from Cloudflare itself.
func (c *cloudflareClient) putIngress(ctx context.Context, accountID, tunnelID, hostname, service string) error {
	config := map[string]any{"config": map[string]any{"ingress": []map[string]any{
		{"hostname": hostname, "path": HookPathPattern, "service": service},
		{"service": "http_status:404"},
	}}}
	return c.call(ctx, http.MethodPut, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", config, nil)
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// createCNAME refuses to take over a hostname that already has records.
func (c *cloudflareClient) createCNAME(ctx context.Context, zoneID, hostname, target string) (string, error) {
	var existing []cfRecord
	if err := c.call(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?name="+url.QueryEscape(hostname), nil, &existing); err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return "", fmt.Errorf("%w: %s already has DNS records; choose an unused hostname", ErrCloudflare, hostname)
	}
	var record cfRecord
	err := c.call(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", map[string]any{
		"type": "CNAME", "name": hostname, "content": target, "proxied": true, "ttl": 1,
		"comment": "Helm public webhook endpoint",
	}, &record)
	if err == nil && record.ID == "" {
		err = fmt.Errorf("%w: DNS response had no ID", ErrCloudflare)
	}
	return record.ID, err
}

func (c *cloudflareClient) deleteDNS(ctx context.Context, zoneID, recordID string) error {
	return c.call(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
}

func (c *cloudflareClient) deleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	// Connections linger briefly after cloudflared stops; clear them first.
	_ = c.call(ctx, http.MethodDelete, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/connections", nil, nil)
	err := c.call(ctx, http.MethodDelete, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, nil)
	// Cloudflare soft-deletes tunnels and accepts repeat deletes; code 1002
	// means the tunnel is already gone, which is what cleanup wants.
	if err != nil && strings.Contains(err.Error(), "(code 1002)") {
		return nil
	}
	return err
}
