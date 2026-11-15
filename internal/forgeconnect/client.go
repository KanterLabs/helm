package forgeconnect

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Outbound limits (contract C4).
const (
	requestTimeout = 10 * time.Second
	maxBody        = 64 << 10
	maxRepoPage    = 1 << 20
)

var (
	errBlockedAddress = errors.New("forgeconnect: address not allowed")
	errOversize       = errors.New("forgeconnect: response too large")
	broadcast         = netip.AddrFrom4([4]byte{255, 255, 255, 255})
)

// systemBundles are the usual Linux CA bundles. They are added explicitly
// only when SSL_CERT_FILE or SSL_CERT_DIR is set, because Go then uses those
// instead of the system roots, and C4 asks for both.
var systemBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",
	"/etc/pki/tls/certs/ca-bundle.crt",
	"/etc/ssl/ca-bundle.pem",
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	"/etc/ssl/cert.pem",
}

// blockedAddr is true for addresses an administrator-typed URL must never
// reach: link-local (cloud metadata), unspecified, multicast and broadcast.
// Private and loopback addresses are allowed; self-hosted forges live there.
func blockedAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if inner, ok := embeddedIPv4(ip); ok {
		return blockedAddr(inner)
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip == broadcast || ip == metadataAWSv6 || ip == metadataAlibaba
}

// Cloud metadata services that sit outside the link-local ranges. Only the
// exact addresses are refused: 100.64.0.0/10 is also where tailnets live.
var (
	metadataAWSv6   = netip.MustParseAddr("fd00:ec2::254")
	metadataAlibaba = netip.MustParseAddr("100.100.100.200")
	nat64Prefix     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
)

// embeddedIPv4 unwraps an IPv4 address carried inside NAT64 or 6to4, so a
// blocked address cannot be reached through a translation prefix.
func embeddedIPv4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case nat64Prefix.Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixToFourPrefix.Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	}
	return netip.Addr{}, false
}

// controlAddress runs in the dialer after name resolution, on the exact
// address about to be dialled, so DNS rebinding cannot slip past it.
func controlAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errBlockedAddress
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || blockedAddr(ip.WithZone("")) {
		return errBlockedAddress
	}
	return nil
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newGuardedClient is the production client: address guard, no proxy, no
// redirects, verified TLS. A nil pool means the system roots.
func newGuardedClient(pool *x509.CertPool, timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: controlAddress}
	transport := &http.Transport{
		Proxy:                  nil, // a proxy would dial on our behalf and bypass the guard
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: maxBody,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           4,
		IdleConnTimeout:        90 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: noRedirect}
}

// injectedClient applies the C4 rules a test client could otherwise skip.
func injectedClient(c *http.Client) *http.Client {
	copied := *c
	if copied.Timeout <= 0 || copied.Timeout > requestTimeout {
		copied.Timeout = requestTimeout
	}
	copied.CheckRedirect = noRedirect
	return &copied
}

// loadRootPool returns the system roots plus SSL_CERT_FILE and SSL_CERT_DIR.
func loadRootPool() *x509.CertPool {
	file, dirs := os.Getenv("SSL_CERT_FILE"), os.Getenv("SSL_CERT_DIR")
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if file == "" && dirs == "" {
		return pool
	}
	for _, bundle := range systemBundles {
		appendPEMFile(pool, bundle)
	}
	if file != "" {
		appendPEMFile(pool, file)
	}
	for _, dir := range filepath.SplitList(dirs) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			appendPEMFile(pool, filepath.Join(dir, entry.Name()))
		}
	}
	return pool
}

func appendPEMFile(pool *x509.CertPool, name string) {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return
	}
	if data, err := os.ReadFile(name); err == nil {
		pool.AppendCertsFromPEM(data)
	}
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

// send performs one request and reads at most limit bytes. Transport errors
// come back as C1 codes; an oversized body as errOversize. Bodies are never
// placed in an error.
func (m *Manager) send(request *http.Request, limit int64) (*reply, error) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Helm")
	response, err := m.http.Do(request)
	if err != nil {
		return nil, transportError(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, transportError(err)
	}
	if int64(len(body)) > limit {
		return nil, errOversize
	}
	return &reply{status: response.StatusCode, header: response.Header, body: body}, nil
}

func transportError(err error) error {
	var (
		verify    *tls.CertificateVerificationError
		authority x509.UnknownAuthorityError
		hostname  x509.HostnameError
		invalid   x509.CertificateInvalidError
		record    tls.RecordHeaderError
		alert     tls.AlertError
	)
	switch {
	case errors.Is(err, errBlockedAddress):
		return fail(CodeInvalidServerURL)
	case errors.As(err, &verify), errors.As(err, &authority), errors.As(err, &hostname),
		errors.As(err, &invalid), errors.As(err, &record), errors.As(err, &alert):
		return fail(CodeTLSError)
	default:
		return fail(CodeServerUnreachable)
	}
}

// codeOr keeps a C1 code already chosen, or uses fallback.
func codeOr(err error, fallback string) error {
	if Code(err) != "" {
		return err
	}
	return fail(fallback)
}
