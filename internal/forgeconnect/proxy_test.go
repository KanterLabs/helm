package forgeconnect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"
	"time"
)

// A deployment whose only way out is the operator's proxy (HTTPS_PROXY) must
// still reach the forge. Through a proxy the dialer sees only the proxy, so
// the target is checked before the request leaves.

type recordingProxy struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []string
	reply int
}

func newRecordingProxy(t *testing.T) *recordingProxy {
	t.Helper()
	p := &recordingProxy{reply: http.StatusOK}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.URL.String())
		status := p.reply
		p.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"via":"proxy"}`))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *recordingProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.seen...)
}

func (p *recordingProxy) policy(t *testing.T) proxyFunc {
	t.Helper()
	target, err := url.Parse(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	return http.ProxyURL(target)
}

func noLocalDNS(context.Context, string) ([]netip.Addr, error) {
	return nil, errors.New("no such host")
}

func get(t *testing.T, client *http.Client, target string) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client.Do(request)
}

func TestGuardedClientUsesOperatorProxy(t *testing.T) {
	proxy := newRecordingProxy(t)
	// The name does not resolve here: only the proxy can reach it.
	client := newGuardedClient(nil, time.Second, proxy.policy(t), noLocalDNS)
	response, err := get(t, client, "http://forge.internal.test/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("request through the operator's proxy failed: %v", err)
	}
	response.Body.Close()
	if got := proxy.requests(); len(got) != 1 || got[0] != "GET http://forge.internal.test/.well-known/openid-configuration" {
		t.Fatalf("proxy saw %v", got)
	}
}

func TestProxyCannotBeUsedToReachBlockedAddresses(t *testing.T) {
	proxy := newRecordingProxy(t)
	resolve := func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "rebind.test":
			return []netip.Addr{netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("169.254.169.254")}, nil
		case "lan.test":
			return []netip.Addr{netip.MustParseAddr("10.0.0.5")}, nil
		}
		return nil, errors.New("no such host")
	}
	client := newGuardedClient(nil, time.Second, proxy.policy(t), resolve)
	for _, target := range []string{
		"http://169.254.169.254/latest/meta-data/", "http://[::ffff:169.254.169.254]/", "http://[fd00:ec2::254]/",
		"http://100.100.100.200/", "http://rebind.test/",
	} {
		response, err := get(t, client, target)
		if err == nil {
			response.Body.Close()
			t.Fatalf("%s was sent to the proxy", target)
		}
		if !errors.Is(err, errBlockedAddress) || Code(transportError(err)) != CodeInvalidServerURL {
			t.Fatalf("%s: err = %v", target, err)
		}
	}
	if got := proxy.requests(); len(got) != 0 {
		t.Fatalf("blocked targets reached the proxy: %v", got)
	}
	response, err := get(t, client, "http://lan.test/")
	if err != nil {
		t.Fatalf("a private address behind the proxy was refused: %v", err)
	}
	response.Body.Close()
}

func TestNoProxyPolicyDialsDirectUnderTheGuard(t *testing.T) {
	// No proxy for this request (as NO_PROXY would decide): the dialer guard applies.
	direct := func(*http.Request) (*url.URL, error) { return nil, nil }
	client := newGuardedClient(nil, time.Second, direct, noLocalDNS)
	if _, err := get(t, client, "http://169.254.169.254/"); !errors.Is(err, errBlockedAddress) {
		t.Fatalf("err = %v", err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	response, err := get(t, client, target.URL)
	if err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("direct loopback request: %v", err)
	}
	response.Body.Close()
}

func TestProxyRefusalIsUnreachableNotALeak(t *testing.T) {
	proxy := newRecordingProxy(t)
	proxy.mu.Lock()
	proxy.reply = http.StatusForbidden
	proxy.mu.Unlock()
	m, err := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir() + "/forge",
		HTTPClient: newGuardedClient(nil, time.Second, proxy.policy(t), noLocalDNS)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Probe(context.Background(), "http://forge.internal.test", true)
	if code := Code(err); code != CodeNotAForge && code != CodeServerUnreachable {
		t.Fatalf("probe through a refusing proxy = %v", err)
	}
}
