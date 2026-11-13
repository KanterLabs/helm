package forgeconnect

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestControlAddress(t *testing.T) {
	refused := []string{
		"169.254.169.254:80", "169.254.0.1:443", "[fe80::1]:443", "[fe80::1%eth0]:443",
		"[::ffff:169.254.169.254]:80", "[::ffff:a9fe:a9fe]:80", "0.0.0.0:80", "[::]:443",
		"224.0.0.1:80", "239.255.255.250:1900", "[ff02::1]:443", "[ff05::2]:443",
		"255.255.255.255:80", "[::ffff:0.0.0.0]:80", "[::ffff:224.0.0.1]:80",
	}
	for _, address := range refused {
		if err := controlAddress("tcp", address, nil); !errors.Is(err, errBlockedAddress) {
			t.Errorf("controlAddress(%q) = %v, want refused", address, err)
		}
	}
	allowed := []string{
		"127.0.0.1:3000", "[::1]:3000", "10.0.0.5:443", "192.168.1.20:3000", "172.16.4.4:443",
		"[fd00::10]:443", "100.64.1.1:443", "93.184.216.34:443", "[2606:4700::1]:443", "[::ffff:10.0.0.5]:443",
	}
	for _, address := range allowed {
		if err := controlAddress("tcp", address, nil); err != nil {
			t.Errorf("controlAddress(%q) = %v, want allowed", address, err)
		}
	}
	if err := controlAddress("tcp", "not-an-address", nil); !errors.Is(err, errBlockedAddress) {
		t.Errorf("unparseable address must be refused, got %v", err)
	}
}

// The guard runs in the dialer, so the address checked is the address
// dialled: there is no earlier lookup for DNS rebinding to slip past.
func TestGuardedClientRefusesLinkLocalDial(t *testing.T) {
	client := newGuardedClient(nil, time.Second)
	for _, target := range []string{"http://169.254.169.254/latest/meta-data/", "http://[::ffff:169.254.169.254]/", "http://[fe80::1]/"} {
		response, err := client.Get(target)
		if response != nil {
			response.Body.Close()
		}
		if !errors.Is(err, errBlockedAddress) {
			t.Errorf("GET %s = %v, want blocked", target, err)
		}
	}
	m := &Manager{http: client}
	request, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/", nil)
	if _, err := m.send(request, maxBody); Code(err) != CodeInvalidServerURL {
		t.Fatalf("blocked dial code = %q", Code(err))
	}
}

func TestGuardedClientIgnoresProxy(t *testing.T) {
	transport := newGuardedClient(nil, time.Second).Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("the guarded transport must not use an environment proxy")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("TLS verification must stay on")
	}
}

func TestInjectedClientHygiene(t *testing.T) {
	other := 0
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other++ }))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	defer redirector.Close()
	m, err := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir(), HTTPClient: &http.Client{Timeout: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if m.http.Timeout != requestTimeout {
		t.Fatalf("timeout = %v, want clamped to %v", m.http.Timeout, requestTimeout)
	}
	if _, err := m.Probe(context.Background(), redirector.URL, true); Code(err) != CodeNotAForge {
		t.Fatalf("probe through redirect = %v", err)
	}
	if other != 0 {
		t.Fatalf("redirect was followed to another host %d times", other)
	}
	short, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir(), HTTPClient: &http.Client{Timeout: 50 * time.Millisecond}})
	if short.http.Timeout != 50*time.Millisecond {
		t.Fatalf("a shorter injected timeout must be kept, got %v", short.http.Timeout)
	}
}

func TestSlowServerTimesOut(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	m, err := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir(), HTTPClient: newGuardedClient(nil, 100*time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := m.Probe(context.Background(), slow.URL, true); Code(err) != CodeServerUnreachable {
		t.Fatalf("slow probe = %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("the timeout did not apply")
	}
}

func TestProbeUnreachable(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	address := gone.URL
	gone.Close()
	m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), address, true); Code(err) != CodeServerUnreachable {
		t.Fatalf("closed port = %v", err)
	}
}

func TestTLSUntrustedAndExtraRoots(t *testing.T) {
	forge := newFakeForge(t, "")
	tlsForge := httptest.NewTLSServer(http.HandlerFunc(forge.serve))
	defer tlsForge.Close()
	forge.set(func(f *fakeForge) { f.issuer = tlsForge.URL })
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	t.Setenv("SSL_CERT_DIR", filepath.Join(t.TempDir(), "missing"))
	m, _ := New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), tlsForge.URL, false); Code(err) != CodeTLSError {
		t.Fatalf("untrusted certificate = %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsForge.Certificate().Raw})
	file := filepath.Join(t.TempDir(), "private-ca.pem")
	if err := os.WriteFile(file, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", file)
	m, _ = New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	server, err := m.Probe(context.Background(), tlsForge.URL, false)
	if err != nil || server.InsecureHTTP || server.BaseURL != tlsForge.URL {
		t.Fatalf("SSL_CERT_FILE root not trusted: %+v %v", server, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "missing.pem"))
	t.Setenv("SSL_CERT_DIR", dir)
	m, _ = New(Options{Enabled: true, PublicOrigin: testOrigin, StateDir: t.TempDir()})
	if _, err := m.Probe(context.Background(), tlsForge.URL, false); err != nil {
		t.Fatalf("SSL_CERT_DIR root not trusted: %v", err)
	}
}
