package publicendpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// workerCompatibilityDate pins the Workers runtime behaviour the email
// Worker was tested against.
const workerCompatibilityDate = "2026-09-01"

type emailRoutingSettings struct {
	Enabled           bool   `json:"enabled"`
	Status            string `json:"status"`
	SupportSubaddress bool   `json:"support_subaddress"`
}

type emailRule struct {
	ID       string `json:"id"`
	Matchers []struct {
		Type  string `json:"type"`
		Field string `json:"field"`
		Value string `json:"value"`
	} `json:"matchers"`
}

type workerBinding struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Text string `json:"text"`
}

// findZoneFor resolves the zone holding domain, trying domain itself first
// (an email domain is often the zone apex).
func (c *cloudflareClient) findZoneFor(ctx context.Context, domain string) (cfZone, error) {
	labels := strings.Split(domain, ".")
	for index := 0; index < len(labels)-1; index++ {
		candidate := strings.Join(labels[index:], ".")
		var zones []cfZone
		if err := c.call(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil, &zones); err != nil {
			return cfZone{}, err
		}
		if len(zones) == 1 && zones[0].Account.ID != "" {
			return zones[0], nil
		}
	}
	return cfZone{}, fmt.Errorf("%w: the token cannot see a Cloudflare zone for %s (needs Zone › Zone › Read on that zone)", ErrCloudflare, domain)
}

func (c *cloudflareClient) emailRouting(ctx context.Context, zoneID string) (emailRoutingSettings, error) {
	var settings emailRoutingSettings
	err := c.call(ctx, http.MethodGet, "/zones/"+zoneID+"/email/routing", nil, &settings)
	return settings, err
}

func (c *cloudflareClient) setSubaddressing(ctx context.Context, zoneID string, enabled bool) error {
	return c.call(ctx, http.MethodPatch, "/zones/"+zoneID+"/email/routing", map[string]any{"support_subaddress": enabled}, nil)
}

// emailRules lists a zone's routing rules (at most 200 exist per domain).
func (c *cloudflareClient) emailRules(ctx context.Context, zoneID string) ([]emailRule, error) {
	all := []emailRule{}
	for page := 1; page <= 5; page++ {
		var rules []emailRule
		if err := c.call(ctx, http.MethodGet, fmt.Sprintf("/zones/%s/email/routing/rules?per_page=50&page=%d", zoneID, page), nil, &rules); err != nil {
			return nil, err
		}
		all = append(all, rules...)
		if len(rules) < 50 {
			break
		}
	}
	return all, nil
}

func (c *cloudflareClient) createWorkerRule(ctx context.Context, zoneID, address, script string) (string, error) {
	var rule emailRule
	err := c.call(ctx, http.MethodPost, "/zones/"+zoneID+"/email/routing/rules", map[string]any{
		"name":     "Helm email intake (" + script + ")",
		"enabled":  true,
		"matchers": []map[string]string{{"type": "literal", "field": "to", "value": address}},
		"actions":  []map[string]any{{"type": "worker", "value": []string{script}}},
	}, &rule)
	if err == nil && rule.ID == "" {
		err = fmt.Errorf("%w: routing rule response had no ID", ErrCloudflare)
	}
	return rule.ID, err
}

func (c *cloudflareClient) deleteRule(ctx context.Context, zoneID, ruleID string) error {
	return ignoreMissing(c.call(ctx, http.MethodDelete, "/zones/"+zoneID+"/email/routing/rules/"+ruleID, nil, nil))
}

// uploadWorker deploys an ES-module Worker. Secrets travel only as
// secret_text bindings, which Cloudflare never returns.
func (c *cloudflareClient) uploadWorker(ctx context.Context, accountID, name, source string, bindings []workerBinding) error {
	metadata, err := json.Marshal(map[string]any{
		"main_module":        "worker.js",
		"compatibility_date": workerCompatibilityDate,
		"bindings":           bindings,
	})
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormField("metadata")
	if err != nil {
		return err
	}
	if _, err := part.Write(metadata); err != nil {
		return err
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="worker.js"; filename="worker.js"`)
	header.Set("Content-Type", "application/javascript+module")
	part, err = writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(source)); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return c.callRaw(ctx, http.MethodPut, "/accounts/"+accountID+"/workers/scripts/"+name, writer.FormDataContentType(), &body, nil)
}

func (c *cloudflareClient) deleteWorker(ctx context.Context, accountID, name string) error {
	return ignoreMissing(c.call(ctx, http.MethodDelete, "/accounts/"+accountID+"/workers/scripts/"+name, nil, nil))
}

// verifiedDestination reports whether address is a verified Email Routing
// destination, which Cloudflare requires before a Worker may forward to it.
func (c *cloudflareClient) verifiedDestination(ctx context.Context, accountID, address string) (bool, error) {
	var addresses []struct {
		Email    string  `json:"email"`
		Verified *string `json:"verified"`
	}
	if err := c.call(ctx, http.MethodGet, "/accounts/"+accountID+"/email/routing/addresses?per_page=50", nil, &addresses); err != nil {
		return false, err
	}
	for _, item := range addresses {
		if strings.EqualFold(item.Email, address) {
			return item.Verified != nil && *item.Verified != "", nil
		}
	}
	return false, nil
}

// ignoreMissing treats an already-deleted resource as cleaned up.
func ignoreMissing(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "not found") || strings.Contains(text, "does not exist") || strings.Contains(text, "(code 10007)") {
		return nil
	}
	return err
}
