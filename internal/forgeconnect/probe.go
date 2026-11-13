package forgeconnect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
)

var errNoDocument = errors.New("forgeconnect: no discovery document")

type discovery struct {
	Issuer  string   `json:"issuer"`
	Grants  []string `json:"grant_types_supported"`
	Methods []string `json:"code_challenge_methods_supported"`
}

func (d *discovery) supported() bool {
	return slices.Contains(d.Grants, "authorization_code") && slices.Contains(d.Grants, "refresh_token") && slices.Contains(d.Methods, "S256")
}

// Probe finds the forge behind an address an administrator typed: it tries
// the address, its parents and its origin for an OpenID discovery document,
// follows an issuer on another path of the same host once, and reads the
// version when the forge allows it anonymously. It saves nothing.
func (m *Manager) Probe(ctx context.Context, baseURL string, allowHTTP bool) (Server, error) {
	if !m.enabled {
		return Server{}, fail(CodeUnavailable)
	}
	candidates, err := probeCandidates(baseURL, allowHTTP)
	if err != nil {
		return Server{}, err
	}
	for _, candidate := range candidates {
		doc, err := m.discover(ctx, candidate)
		if errors.Is(err, errNoDocument) {
			continue
		}
		if err != nil {
			return Server{}, err
		}
		base, err := m.settle(ctx, candidate, doc)
		if err != nil {
			return Server{}, err
		}
		server := Server{BaseURL: base, Product: gitea.Product, InsecureHTTP: strings.HasPrefix(base, "http://")}
		if version := m.version(ctx, base, ""); version != "" {
			server.Version, server.Product = version, productFor(version)
		}
		return server, nil
	}
	return Server{}, fail(CodeNotAForge)
}

func (m *Manager) discover(ctx context.Context, base string) (*discovery, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+gitea.DiscoveryPath, nil)
	if err != nil {
		return nil, fail(CodeInvalidServerURL)
	}
	r, err := m.send(request, maxBody)
	if errors.Is(err, errOversize) {
		return nil, errNoDocument
	}
	if err != nil {
		return nil, err
	}
	var doc discovery
	if r.status != http.StatusOK || json.Unmarshal(r.body, &doc) != nil || doc.Issuer == "" {
		return nil, errNoDocument
	}
	return &doc, nil
}

// settle decides the base URL from the issuer a discovery document names.
func (m *Manager) settle(ctx context.Context, candidate string, doc *discovery) (string, error) {
	if !doc.supported() {
		return "", fail(CodeNotAForge)
	}
	probed, _ := parseServerURL(candidate)
	issuer, err := parseServerURL(doc.Issuer)
	if err != nil {
		return "", fail(CodeNotAForge)
	}
	if issuer.hostKey() != probed.hostKey() {
		return "", &Error{Code: CodeIssuerMismatch, Issuer: issuer.String()}
	}
	if strings.EqualFold(issuer.path, probed.path) {
		return candidate, nil
	}
	// Same host, another path: probe the issuer once, never further.
	next := probed.origin() + issuer.path
	again, err := m.discover(ctx, next)
	if errors.Is(err, errNoDocument) {
		return "", fail(CodeNotAForge)
	}
	if err != nil {
		return "", err
	}
	confirmed, err := parseServerURL(again.Issuer)
	if !again.supported() || err != nil || confirmed.hostKey() != probed.hostKey() || !strings.EqualFold(confirmed.path, issuer.path) {
		return "", fail(CodeNotAForge)
	}
	return next, nil
}

// version is best effort: empty when refused or malformed.
func (m *Manager) version(ctx context.Context, base, token string) string {
	r, err := m.get(ctx, base+gitea.VersionPath, token, maxBody)
	if err != nil || r.status != http.StatusOK {
		return ""
	}
	var body struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(r.body, &body) != nil || !validVersion(body.Version) {
		return ""
	}
	return body.Version
}

func productFor(version string) string {
	if strings.Contains(version, gitea.ForkMarker) {
		return gitea.ForkProduct
	}
	return gitea.Product
}

// get sends a GET, with a bearer token when one is given.
func (m *Manager) get(ctx context.Context, target, token string, limit int64) (*reply, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fail(CodeInvalidServerURL)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return m.send(request, limit)
}
