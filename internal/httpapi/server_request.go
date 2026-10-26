package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/KanterLabs/helm/internal/auth"
	"io"
	"net/http"
	"strings"
	"time"
)

type contextKey string

const (
	requestBodyKey      contextKey = "helm-request-body"
	requestIdentityKey  contextKey = "helm-request-identity"
	maxRequestBodyBytes            = 2 * 1024 * 1024
)

const (
	defaultBodyBufferTotalSlots  = 16
	defaultBodyBufferAgentSlots  = 8
	defaultBodyBufferPublicSlots = 4
	defaultBearerAuthSlots       = 4
)

type bodyBufferClass uint8

const (
	bodyBufferHuman bodyBufferClass = iota
	bodyBufferAgent
	bodyBufferPublic
)

type bodyBufferPool struct {
	total  chan struct{}
	agent  chan struct{}
	public chan struct{}
}

func newBodyBufferPool(total, agent, public int) *bodyBufferPool {
	if total < 1 {
		total = defaultBodyBufferTotalSlots
	}
	if agent < 1 || agent > total {
		agent = minBodyBufferClassSlots(defaultBodyBufferAgentSlots, total)
	}
	if public < 1 || public > total {
		public = minBodyBufferClassSlots(defaultBodyBufferPublicSlots, total)
	}
	return &bodyBufferPool{total: make(chan struct{}, total), agent: make(chan struct{}, agent), public: make(chan struct{}, public)}
}

func minBodyBufferClassSlots(want, total int) int {
	if want < 1 {
		return 1
	}
	if want > total {
		return total
	}
	return want
}

func (p *bodyBufferPool) classSlots(class bodyBufferClass) chan struct{} {
	switch class {
	case bodyBufferAgent:
		return p.agent
	case bodyBufferPublic:
		return p.public
	default:
		return nil
	}
}

func (p *bodyBufferPool) tryAcquire(class bodyBufferClass) bool {
	if p == nil || p.total == nil {
		return true
	}
	classSlots := p.classSlots(class)
	if classSlots != nil {
		select {
		case classSlots <- struct{}{}:
		default:
			return false
		}
	}
	select {
	case p.total <- struct{}{}:
		return true
	default:
		if classSlots != nil {
			<-classSlots
		}
		return false
	}
}

func (p *bodyBufferPool) release(class bodyBufferClass) {
	if p == nil || p.total == nil {
		return
	}
	<-p.total
	if classSlots := p.classSlots(class); classSlots != nil {
		<-classSlots
	}
}

// All Server instances in one process share the body-buffer cap. Tests can
// replace the per-server pool with a smaller one to exercise saturation.
var processBodyBufferPool = newBodyBufferPool(defaultBodyBufferTotalSlots, defaultBodyBufferAgentSlots, defaultBodyBufferPublicSlots)

var processBearerAuthSlots = make(chan struct{}, defaultBearerAuthSlots)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w}
	w = sw
	started := time.Now()
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" || len(requestID) > 128 {
		requestID = newRequestID()
	}
	w.Header().Set("X-Request-ID", requestID)
	if s.Cfg.ReleaseSHA != "" {
		w.Header().Set("X-Roadmap-Revision", s.Cfg.ReleaseSHA)
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	if strings.HasPrefix(s.Cfg.PublicOrigin, "https://") {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	// The OpenAPI document is part of the API surface and must not be cached
	// separately from the JSON API responses.  In particular, deployments can
	// serve a revision-specific document, so a stale intermediary response is
	// misleading even though the document itself is public.
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/openapi.json" ||
		r.URL.Path == "/healthz" || r.URL.Path == "/health" || r.URL.Path == "/readyz" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
		w.Header().Set("Cache-Control", "no-store")
	}
	if s.Cfg.PublicOrigin != "" && r.Header.Get("Origin") == s.Cfg.PublicOrigin {
		w.Header().Set("Access-Control-Allow-Origin", s.Cfg.PublicOrigin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	identity := auth.Identity{}
	hasIdentity := false
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logJSON(map[string]any{"level": "error", "msg": "panic recovered", "request_id": safeLogToken(requestID), "route": metricRoute(r.URL.Path)})
			s.writeError(w, http.StatusInternalServerError, "internal_error", "internal server error", nil)
		}
		s.logRequest(requestID, r.Method, r.URL.Path, responseStatus(w), time.Since(started), identity, hasIdentity)
	}()
	if s.serveLegacyOrigin(w, r) {
		return
	}
	// A disabled beta switcher is intentionally indistinguishable from an
	// unknown route, including before authentication. This avoids turning the
	// feature flag into an oracle and guarantees the broker is never called.
	if isBetaSwitchRoute(r) && !s.betaSwitchEnabled() {
		s.writeError(w, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,If-Match,Idempotency-Key,X-Request-ID")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var proceed bool
	identity, hasIdentity, proceed = s.prepareRequest(w, r)
	if !proceed {
		return
	}
	if isBufferableRequest(r) && r.ContentLength > maxRequestBodyBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 2 MiB", nil)
		return
	}

	var body []byte
	if shouldBufferRequestBody(r) {
		pool := s.bodyBufferPoolValue()
		class := requestBodyClass(r, identity, hasIdentity)
		if !pool.tryAcquire(class) {
			w.Header().Set("Retry-After", "1")
			s.writeError(w, http.StatusServiceUnavailable, "server_busy", "server is busy buffering request bodies; retry later", nil)
			return
		}
		defer pool.release(class)
		var err error
		body, err = readRequestBody(r.Body, r.ContentLength)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid_body", "could not read request body", nil)
			return
		}
		if len(body) > maxRequestBodyBytes {
			s.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 2 MiB", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	requestContext := context.WithValue(r.Context(), requestBodyKey, body)
	if hasIdentity {
		requestContext = context.WithValue(requestContext, requestIdentityKey, identity)
	}
	r = r.WithContext(requestContext)
	s.route(w, r)
}

func (s *Server) prepareRequest(w http.ResponseWriter, r *http.Request) (auth.Identity, bool, bool) {
	protected := isProtectedAPIRequest(r)
	// Metrics are a protected endpoint for remote peers but remain available to
	// the local monitoring process without credentials. Handling this here
	// keeps the authenticated identity in the request context so the common
	// request log has actor-safe context and avoids authenticating twice.
	if r.URL.Path == "/metrics" && !isLoopbackRequest(r) {
		protected = true
	}
	publicAuth := isPublicAuthRequest(r)
	if !protected && !publicAuth {
		return auth.Identity{}, false, true
	}
	if !protected && strings.TrimSpace(r.Header.Get("Authorization")) == "" {
		return auth.Identity{}, false, true
	}
	identity, err, admitted := s.authenticateRequest(w, r)
	if !admitted {
		return auth.Identity{}, false, false
	}
	if err != nil {
		if protected {
			s.writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return auth.Identity{}, false, false
		}
		// Setup, login, logout, and status remain public even when a stale or
		// malformed bearer value accompanies the request.
		return auth.Identity{}, false, true
	}
	// A valid bearer is admitted before body buffering. This protects the
	// request body pool and also ensures idempotency replays pay only the
	// general request cost, not a mutation reservation.
	if !s.admitBearerRequest(w, r, identity) {
		return auth.Identity{}, false, false
	}
	return identity, true, true
}

func isProtectedAPIRequest(r *http.Request) bool {
	if !isAPIPath(r.URL.Path) {
		return false
	}
	// Keep the unauthenticated API discovery endpoint public.
	if (r.URL.Path == "/api/v1" || r.URL.Path == "/api/v1/") && r.Method == http.MethodGet {
		return false
	}
	// Webhook intake authenticates with its own path secret, never a session.
	if parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1")); isCoolifyIntakePath(parts) || isTicketHookPath(parts) || isEmailHookPath(parts) {
		return false
	}
	return !isPublicAuthRequest(r)
}

func isAPIPath(value string) bool {
	return value == "/api/v1" || strings.HasPrefix(value, "/api/v1/")
}

func isPublicAuthRequest(r *http.Request) bool {
	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1"))
	if len(parts) < 2 || parts[0] != "auth" {
		return false
	}
	switch parts[1] {
	case "status":
		return r.Method == http.MethodGet
	case "setup", "login", "logout":
		return r.Method == http.MethodPost
	default:
		return false
	}
}

func isBufferableRequest(r *http.Request) bool {
	if !isBodyBearingMethod(r.Method) {
		return false
	}
	return isProtectedAPIRequest(r) || isPublicAuthRequest(r)
}

func shouldBufferRequestBody(r *http.Request) bool {
	return isBufferableRequest(r) && r.Body != nil && r.Body != http.NoBody
}

func requestBodyClass(r *http.Request, identity auth.Identity, hasIdentity bool) bodyBufferClass {
	if isPublicAuthRequest(r) || !hasIdentity {
		return bodyBufferPublic
	}
	if identity.IsToken || identity.Actor.Kind != "human" {
		return bodyBufferAgent
	}
	return bodyBufferHuman
}

func requestIdentity(r *http.Request) (auth.Identity, bool) {
	identity, ok := r.Context().Value(requestIdentityKey).(auth.Identity)
	return identity, ok
}

func bearerCredentialKey(r *http.Request) (string, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	credential := strings.TrimSpace(parts[1])
	if credential == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(sum[:]), true
}

func (s *Server) bearerAuthSemaphore() chan struct{} {
	if s.bearerAuthSlots != nil {
		return s.bearerAuthSlots
	}
	return processBearerAuthSlots
}

func (s *Server) authenticateRequest(w http.ResponseWriter, r *http.Request) (auth.Identity, error, bool) {
	if _, isBearer := bearerCredentialKey(r); isBearer {
		if !s.admitBearerCredential(w, r) {
			return auth.Identity{}, nil, false
		}
		slots := s.bearerAuthSemaphore()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			s.metricsValue().recordRateLimit("authentication_slots")
			w.Header().Set("Retry-After", "1")
			s.writeError(w, http.StatusServiceUnavailable, "server_busy", "server is busy authenticating bearer requests; retry later", nil)
			return auth.Identity{}, nil, false
		}
	}
	identity, err := s.Auth.Authenticate(r.Context(), r)
	if err != nil {
		s.observeAuthFailure(err, r)
	}
	return identity, err, true
}

func readRequestBody(reader io.Reader, contentLength int64) ([]byte, error) {
	limited := io.LimitReader(reader, maxRequestBodyBytes+1)
	if contentLength >= 0 && contentLength <= maxRequestBodyBytes {
		buffer := bytes.NewBuffer(make([]byte, 0, int(contentLength)))
		if _, err := buffer.ReadFrom(limited); err != nil {
			return nil, err
		}
		return buffer.Bytes(), nil
	}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Server) bodyBufferPoolValue() *bodyBufferPool {
	if s.bodyBufferPool != nil {
		return s.bodyBufferPool
	}
	return processBodyBufferPool
}

// statusWriter lets the request logger report the status without changing
// handlers' response behavior.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	// A server may send multiple informational responses before one final
	// status. Preserve those writes while recording only the final response
	// for request metrics. Switching Protocols (101) is itself final.
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach optional capabilities such as
// Flush, Hijack, and full-duplex request handling without advertising an
// interface the underlying writer does not implement.
func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func responseStatus(w http.ResponseWriter) int {
	if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
		return sw.status
	}
	return http.StatusOK
}

func newRequestID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return hex.EncodeToString(sum[:8])
}
