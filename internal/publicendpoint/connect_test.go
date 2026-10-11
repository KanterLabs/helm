package publicendpoint

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// The token link must select exactly the permissions the setup cards list,
// so following it always produces a token that works.
func TestTokenLinkSelectsDocumentedPermissions(t *testing.T) {
	link, err := url.Parse(TokenLink())
	if err != nil {
		t.Fatal(err)
	}
	if link.Host != "dash.cloudflare.com" || link.Query().Get("to") != "/:account/api-tokens" || link.Query().Get("name") != "Helm public access" {
		t.Fatalf("unexpected link %s", link)
	}
	var keys []map[string]string
	if err := json.Unmarshal([]byte(link.Query().Get("permissionGroupKeys")), &keys); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"zone": "read", "dns": "edit", "zone_settings": "edit", "email_routing_rule": "edit", "argotunnel": "edit", "workers_scripts": "edit", "email_routing_address": "read"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for _, key := range keys {
		if want[key["key"]] != key["type"] {
			t.Fatalf("unexpected permission %v", key)
		}
	}
	listed := strings.Join(append(append([]string{}, RequiredPermissions...), EmailPermissions...), "\n")
	for _, area := range []string{"Cloudflare Tunnel", "DNS", "Zone › Read", "Zone Settings", "Email Routing Rules", "Workers Scripts", "Email Routing Addresses"} {
		if !strings.Contains(listed, area) {
			t.Errorf("setup cards no longer list %q but the token link still requests it", area)
		}
	}
	if len(OAuthScopes) != len(TokenPermissions) {
		t.Fatalf("OAuth scopes (%d) and token permissions (%d) differ", len(OAuthScopes), len(TokenPermissions))
	}
}

func TestOAuthStartUsesPKCEAndOneTimeState(t *testing.T) {
	m := &Manager{cfg: Config{OAuthClientID: "client-1", OAuthRelayURL: "https://relay.example/cloudflare/callback", DashboardURL: "https://dash.example", PublicOrigin: "https://helm.example"}}
	authorize, err := m.StartOAuth("actor-1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorize)
	query := parsed.Query()
	if parsed.Path != "/oauth2/auth" || query.Get("client_id") != "client-1" || query.Get("redirect_uri") != "https://relay.example/cloudflare/callback" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize URL = %s", authorize)
	}
	nonce, origin, _ := strings.Cut(query.Get("state"), ".")
	decoded, _ := base64.RawURLEncoding.DecodeString(origin)
	if string(decoded) != "https://helm.example" {
		t.Fatalf("state origin = %q", decoded)
	}
	pending := m.connect.pending[nonce]
	sum := sha256.Sum256([]byte(pending.verifier))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != query.Get("code_challenge") || pending.actorID != "actor-1" {
		t.Fatal("code challenge does not match the stored verifier")
	}
	if strings.Contains(authorize, pending.verifier) {
		t.Fatal("the PKCE verifier must never leave the server")
	}
	if err := m.FinishOAuth(t.Context(), "someone-else", query.Get("state"), "code"); err == nil {
		t.Fatal("another administrator finished this sign-in")
	}
	if _, ok := m.connect.pending[nonce]; ok {
		t.Fatal("a refused state must not stay usable")
	}
	if (&Manager{cfg: Config{OAuthClientID: ""}}).OAuthAvailable() {
		t.Fatal("sign-in must be unavailable without a client ID")
	}
}
