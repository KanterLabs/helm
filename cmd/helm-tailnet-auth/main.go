// Command helm-tailnet-auth is the loopback forward-auth boundary for Helm's
// private Tailnet origin. It never trusts a browser identity header: the edge
// supplies one sanitized method, URI, and remote address, and this process
// resolves that address through tailscaled's Unix-socket LocalAPI WhoIs call.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/config"
)

const (
	forwardedMethodHeader = "X-Forwarded-Method"
	forwardedURIHeader    = "X-Forwarded-Uri"
	remoteAddrHeader      = "X-Helm-Tailnet-Remote-Addr"
	defaultListenAddr     = "127.0.0.1:19603"
	defaultTailnetSocket  = "/run/tailscale/tailscaled.sock"
	whoIsTimeout          = 2 * time.Second
	maxForwardAuthBody    = 4 * 1024
	maxWhoIsBody          = 128 * 1024
	maxForwardedMethod    = 32
	maxForwardedURI       = auth.TailnetMaxURI
	maxRemoteAddr         = 128
)

var (
	errUnknownPeer = errors.New("tailnet peer is unknown")
	errTaggedPeer  = errors.New("tailnet peer is tagged")
)

// TailnetPeer is the small identity projection needed from LocalAPI WhoIs.
// Tags are retained so tagged service nodes can never impersonate the owner.
type TailnetPeer struct {
	LoginName   string
	DisplayName string
	Tags        []string
}

// WhoIs resolves one edge-provided peer address through the tailscaled
// LocalAPI. Implementations are injectable so tests never need a tailscaled
// process or a new module dependency.
type WhoIs interface {
	WhoIs(context.Context, string) (TailnetPeer, error)
}

// LocalAPIWhoIs is the production Unix-socket LocalAPI client.
type LocalAPIWhoIs struct {
	Socket  string
	Timeout time.Duration
}

func (c LocalAPIWhoIs) WhoIs(ctx context.Context, address string) (TailnetPeer, error) {
	if strings.TrimSpace(c.Socket) == "" {
		return TailnetPeer{}, errors.New("tailscaled socket is not configured")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = whoIsTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, "unix", c.Socket)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// tailscaled validates this Host value before dispatching LocalAPI
	// requests. The connection is over the Unix socket above; the hostname is
	// an API protocol marker, not a DNS destination.
	target := "http://local-tailscaled.sock/localapi/v0/whois?addr=" + url.QueryEscape(address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return TailnetPeer{}, errors.New("could not create tailscaled request")
	}
	response, err := client.Do(req)
	if err != nil {
		return TailnetPeer{}, errors.New("tailscaled WhoIs request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized {
			return TailnetPeer{}, errUnknownPeer
		}
		return TailnetPeer{}, fmt.Errorf("tailscaled WhoIs returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxWhoIsBody+1))
	if err != nil || len(body) > maxWhoIsBody {
		return TailnetPeer{}, errors.New("invalid tailscaled WhoIs response size")
	}
	var payload localWhoIsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return TailnetPeer{}, errors.New("invalid tailscaled WhoIs response")
	}
	if payload.Node == nil || payload.UserProfile == nil || strings.TrimSpace(payload.UserProfile.LoginName) == "" {
		return TailnetPeer{}, errUnknownPeer
	}
	if len(payload.Node.Tags) > 0 {
		return TailnetPeer{}, errTaggedPeer
	}
	return TailnetPeer{
		LoginName:   payload.UserProfile.LoginName,
		DisplayName: payload.UserProfile.DisplayName,
		Tags:        append([]string(nil), payload.Node.Tags...),
	}, nil
}

type localWhoIsResponse struct {
	Node        *localWhoIsNode        `json:"Node"`
	UserProfile *localWhoIsUserProfile `json:"UserProfile"`
}

type localWhoIsNode struct {
	Tags []string `json:"Tags"`
}

type localWhoIsUserProfile struct {
	LoginName   string `json:"LoginName"`
	DisplayName string `json:"DisplayName"`
}

type handler struct {
	Signer     []byte
	Owner      string
	AdminEmail string
	Audience   string
	WhoIs      WhoIs
	Now        func() time.Time
}

// NewHandler builds a strict loopback forward-auth handler. The returned
// handler emits the signed assertion only as a response header with an empty
// body; the edge must explicitly copy that header on its trusted success path.
func NewHandler(key []byte, owner, audience string, resolver WhoIs) (http.Handler, error) {
	return NewHandlerWithAdminEmail(key, owner, owner, audience, resolver)
}

// NewHandlerWithAdminEmail separates the exact tailscaled LoginName from the
// existing Helm human actor's configured email. They are signed together so a
// valid Tailnet node cannot select a different local actor.
func NewHandlerWithAdminEmail(key []byte, owner, adminEmail, audience string, resolver WhoIs) (http.Handler, error) {
	if _, err := auth.NewTailnetJWTVerifier(key); err != nil {
		return nil, err
	}
	owner = strings.TrimSpace(owner)
	if owner == "" || len(owner) > 320 || owner != strings.TrimSpace(owner) || strings.ContainsAny(owner, "\r\n\x00") {
		return nil, errors.New("tailnet owner login is invalid")
	}
	adminEmail = strings.TrimSpace(adminEmail)
	if !auth.ValidEmail(adminEmail) {
		return nil, errors.New("tailnet administrator email must be valid")
	}
	if err := validateAudience(audience); err != nil {
		return nil, err
	}
	if resolver == nil {
		return nil, errors.New("tailnet WhoIs resolver is required")
	}
	return &handler{Signer: append([]byte(nil), key...), Owner: owner, AdminEmail: adminEmail, Audience: audience, WhoIs: resolver, Now: time.Now}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !config.IsLoopbackAddr(r.RemoteAddr) {
		writeHelperError(w, http.StatusForbidden, "forward-auth helper requires a loopback caller")
		return
	}
	if values := r.Header.Values(auth.TailnetAssertionHeader); len(values) != 0 {
		writeHelperError(w, http.StatusBadRequest, "assertion header is not accepted by the helper")
		return
	}
	method, ok := singleHeader(r, forwardedMethodHeader)
	if !ok {
		writeHelperError(w, http.StatusBadRequest, "exactly one forwarded method is required")
		return
	}
	requestURI, ok := singleHeader(r, forwardedURIHeader)
	if !ok {
		writeHelperError(w, http.StatusBadRequest, "exactly one forwarded URI is required")
		return
	}
	remoteAddr, ok := singleHeader(r, remoteAddrHeader)
	if !ok {
		writeHelperError(w, http.StatusBadRequest, "exactly one tailnet remote address is required")
		return
	}
	if !validForwardedMethod(method) || !validForwardedURI(requestURI) {
		writeHelperError(w, http.StatusBadRequest, "forwarded request binding is invalid")
		return
	}
	lookupAddr, err := normalizeRemoteAddr(remoteAddr)
	if err != nil {
		writeHelperError(w, http.StatusBadRequest, "tailnet remote address is invalid")
		return
	}
	if !consumeEmptyBody(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), whoIsTimeout)
	defer cancel()
	peer, err := h.WhoIs.WhoIs(ctx, lookupAddr)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errUnknownPeer) || errors.Is(err, errTaggedPeer) {
			status = http.StatusUnauthorized
		}
		writeHelperError(w, status, "tailnet identity could not be verified")
		return
	}
	if len(peer.Tags) > 0 {
		writeHelperError(w, http.StatusUnauthorized, "tailnet identity could not be verified")
		return
	}
	if peer.LoginName != h.Owner {
		writeHelperError(w, http.StatusUnauthorized, "tailnet identity could not be verified")
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	claims := auth.TailnetClaims{
		Issuer:     auth.TailnetAssertionIssuer,
		Audience:   h.Audience,
		Subject:    peer.LoginName,
		Email:      h.AdminEmail,
		Name:       strings.TrimSpace(peer.DisplayName),
		Method:     method,
		RequestURI: requestURI,
		IssuedAt:   now.Unix(),
		ExpiresAt:  now.Add(auth.TailnetAssertionTTL).Unix(),
	}
	assertion, err := auth.SignTailnetAssertionAt(h.Signer, claims, now)
	if err != nil {
		writeHelperError(w, http.StatusInternalServerError, "tailnet assertion could not be created")
		return
	}
	w.Header().Set(auth.TailnetAssertionHeader, assertion)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func singleHeader(r *http.Request, name string) (string, bool) {
	values := r.Header.Values(name)
	returnValue := ""
	if len(values) == 1 {
		returnValue = values[0]
	}
	return returnValue, len(values) == 1 && returnValue != "" && strings.TrimSpace(returnValue) == returnValue
}

func validForwardedMethod(method string) bool {
	if len(method) > maxForwardedMethod || method == "" || method != strings.ToUpper(method) {
		return false
	}
	for _, char := range method {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '!' && char != '#' && char != '$' && char != '%' && char != '&' && char != '\'' && char != '*' && char != '+' && char != '-' && char != '.' && char != '^' && char != '_' && char != '`' && char != '|' && char != '~' {
			return false
		}
	}
	return true
}

func validForwardedURI(requestURI string) bool {
	if requestURI == "" || len(requestURI) > maxForwardedURI || requestURI != strings.TrimSpace(requestURI) || strings.IndexFunc(requestURI, unicode.IsControl) >= 0 || !strings.HasPrefix(requestURI, "/") || strings.HasPrefix(requestURI, "//") {
		return false
	}
	parsed, err := url.ParseRequestURI(requestURI)
	return err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.Fragment == ""
}

func normalizeRemoteAddr(value string) (string, error) {
	if value == "" || len(value) > maxRemoteAddr || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid remote address")
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		if ip := net.ParseIP(value); ip != nil {
			return net.JoinHostPort(ip.String(), "443"), nil
		}
		return "", err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("remote address host is not an IP")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", errors.New("remote address port is invalid")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(parsedPort)), nil
}

func consumeEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength > maxForwardAuthBody {
		writeHelperError(w, http.StatusRequestEntityTooLarge, "forward-auth request body is too large")
		return false
	}
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxForwardAuthBody+1))
	if err != nil {
		writeHelperError(w, http.StatusBadRequest, "forward-auth request body is invalid")
		return false
	}
	if len(body) > maxForwardAuthBody {
		writeHelperError(w, http.StatusRequestEntityTooLarge, "forward-auth request body is too large")
		return false
	}
	if len(body) != 0 {
		writeHelperError(w, http.StatusBadRequest, "forward-auth request body must be empty")
		return false
	}
	return true
}

func validateAudience(value string) error {
	if value == "" || len(value) > auth.TailnetMaxAudienceSize || strings.TrimSpace(value) != value {
		return errors.New("tailnet audience is invalid")
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") || parsed.Path != "" && parsed.Path != "/" {
		return errors.New("tailnet audience must be an exact https origin")
	}
	return nil
}

func writeHelperError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.Error(w, message, status)
}

type helperOptions struct {
	Addr       string
	Socket     string
	KeyFile    string
	Owner      string
	AdminEmail string
	Audience   string
}

func parseOptions(args []string) (helperOptions, error) {
	addr, err := envValue(defaultListenAddr, "HELM_TAILNET_AUTH_ADDR", "ROADMAP_TAILNET_AUTH_ADDR")
	if err != nil {
		return helperOptions{}, err
	}
	socket, err := envValue(defaultTailnetSocket, "HELM_TAILNET_SOCKET", "ROADMAP_TAILNET_SOCKET", "HELM_TAILNET_TAILSCALED_SOCKET", "ROADMAP_TAILNET_TAILSCALED_SOCKET")
	if err != nil {
		return helperOptions{}, err
	}
	keyFile, err := envValue("", "HELM_TAILNET_ASSERTION_KEY_FILE", "HELM_TAILNET_AUTH_KEY_FILE", "HELM_TAILNET_KEY_FILE", "ROADMAP_TAILNET_ASSERTION_KEY_FILE", "ROADMAP_TAILNET_AUTH_KEY_FILE", "ROADMAP_TAILNET_KEY_FILE")
	if err != nil {
		return helperOptions{}, err
	}
	adminEmail, err := envValue("", "HELM_ADMIN_EMAIL", "ROADMAP_ADMIN_EMAIL")
	if err != nil {
		return helperOptions{}, err
	}
	owner, err := envValue("", "HELM_TAILNET_OWNER_LOGIN", "HELM_TAILNET_ADMIN_EMAIL", "ROADMAP_TAILNET_OWNER_LOGIN", "ROADMAP_TAILNET_ADMIN_EMAIL")
	if err != nil {
		return helperOptions{}, err
	}
	if owner == "" {
		owner = adminEmail
	}
	audience, err := envValue("", "HELM_TAILNET_AUDIENCE", "ROADMAP_TAILNET_AUDIENCE")
	if err != nil {
		return helperOptions{}, err
	}
	if audience == "" {
		audience, err = envValue("", "HELM_PUBLIC_ORIGIN", "ROADMAP_PUBLIC_ORIGIN")
		if err != nil {
			return helperOptions{}, err
		}
	}
	defaults := helperOptions{Addr: addr, Socket: socket, KeyFile: keyFile, Owner: owner, AdminEmail: adminEmail, Audience: audience}
	flags := flag.NewFlagSet("helm-tailnet-auth", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&defaults.Addr, "addr", defaults.Addr, "loopback listen address")
	flags.StringVar(&defaults.Socket, "socket", defaults.Socket, "tailscaled LocalAPI Unix socket")
	flags.StringVar(&defaults.KeyFile, "key-file", defaults.KeyFile, "protected assertion key file")
	flags.StringVar(&defaults.Owner, "owner-login", defaults.Owner, "exact Tailnet owner login")
	flags.StringVar(&defaults.AdminEmail, "admin-email", defaults.AdminEmail, "existing Helm administrator email")
	flags.StringVar(&defaults.Audience, "audience", defaults.Audience, "exact private HTTPS origin")
	if err := flags.Parse(args); err != nil {
		return helperOptions{}, err
	}
	if flags.NArg() != 0 {
		return helperOptions{}, errors.New("unexpected arguments")
	}
	return defaults, nil
}

func envValue(fallback string, names ...string) (string, error) {
	value := ""
	valueName := ""
	for _, name := range names {
		candidate := strings.TrimSpace(os.Getenv(name))
		if candidate == "" {
			continue
		}
		if valueName == "" {
			value, valueName = candidate, name
			continue
		}
		if candidate != value {
			return "", fmt.Errorf("conflicting environment variables %s and %s", valueName, name)
		}
	}
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func run(args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	if !config.IsLoopbackAddr(options.Addr) {
		return errors.New("helper address must bind to loopback")
	}
	if options.Socket == "" || strings.ContainsAny(options.Socket, "\r\n\x00") {
		return errors.New("tailscaled socket path is invalid")
	}
	key, err := config.LoadPrivateKeyFile(options.KeyFile)
	if err != nil {
		return errors.New("could not load tailnet assertion key")
	}
	if err := validateAudience(options.Audience); err != nil {
		return err
	}
	resolver := LocalAPIWhoIs{Socket: options.Socket, Timeout: whoIsTimeout}
	handler, err := NewHandlerWithAdminEmail(key, options.Owner, options.AdminEmail, options.Audience, resolver)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	return server.Serve(listener)
}

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "helm-tailnet-auth: configuration or server failure")
		os.Exit(1)
	}
}
