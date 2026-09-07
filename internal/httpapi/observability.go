package httpapi

// This file contains the deliberately small, dependency-free observability
// surface used by the Helm server.  Metrics are rendered in Prometheus's text
// format so an operator can scrape a loopback listener without adding a
// second runtime or exposing task data to a telemetry provider.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

const (
	metricsContentType        = "text/plain; version=0.0.4; charset=utf-8"
	readinessTimeout          = 2 * time.Second
	readinessMinFreeBytes     = int64(64 << 20)
	readinessLockWarn         = 250 * time.Millisecond
	readinessListLimit        = 16
	readinessStorageWALCap    = int64(64 << 20)
	maxMetricRouteLength      = 96
	maxLoggedErrorClassLength = 48
)

var metricHistogramBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// metricRouteTemplates is intentionally finite.  A route label is useful for
// operations only when it describes a real handler; arbitrary combinations of
// otherwise-known path segments would let an attacker grow the process-local
// registry without bound.
var metricRouteTemplates = map[string]struct{}{
	"/api/v1": {},

	"/api/v1/search": {}, "/api/v1/search/tasks": {}, "/api/v1/search/projects": {}, "/api/v1/search/views": {},
	"/api/v1/views": {}, "/api/v1/views/:view": {}, "/api/v1/views/:view/search": {}, "/api/v1/views/:view/tasks": {},
	"/api/v1/saved-views": {}, "/api/v1/saved-views/:view": {}, "/api/v1/saved-views/:view/search": {}, "/api/v1/saved-views/:view/tasks": {},
	"/api/v1/export": {}, "/api/v1/import": {}, "/api/v1/import/trello": {},
	"/api/v1/auth/status": {}, "/api/v1/auth/setup": {}, "/api/v1/auth/login": {}, "/api/v1/auth/logout": {}, "/api/v1/auth/me": {},
	"/api/v1/codex/account": {}, "/api/v1/codex/login": {}, "/api/v1/codex/login/cancel": {}, "/api/v1/codex/logout": {},
	"/api/v1/projects": {}, "/api/v1/projects/:project": {},
	"/api/v1/projects/:project/export": {}, "/api/v1/projects/:project/import": {}, "/api/v1/projects/:project/boards": {},
	"/api/v1/projects/:project/columns": {}, "/api/v1/projects/:project/tasks": {}, "/api/v1/projects/:project/task-context": {},
	"/api/v1/projects/:project/task-draft": {}, "/api/v1/projects/:project/timeline": {}, "/api/v1/projects/:project/audits": {},
	"/api/v1/projects/:project/labels": {}, "/api/v1/projects/:project/roadmap": {},
	"/api/v1/columns/:column": {}, "/api/v1/issues": {}, "/api/v1/issues/metrics": {}, "/api/v1/audit-findings/:finding": {},
	"/api/v1/audits/:audit": {}, "/api/v1/audits/:audit/findings": {}, "/api/v1/audits/:audit/finalize": {},
	"/api/v1/tasks/:task": {}, "/api/v1/tasks/:task/restore": {}, "/api/v1/tasks/:task/move": {}, "/api/v1/tasks/:task/reorder": {},
	"/api/v1/tasks/:task/checklist": {}, "/api/v1/tasks/:task/checklists": {}, "/api/v1/tasks/:task/checklist/:item": {}, "/api/v1/tasks/:task/checklists/:item": {},
	"/api/v1/tasks/:task/checklist/reorder": {}, "/api/v1/tasks/:task/checklists/reorder": {},
	"/api/v1/tasks/:task/hierarchy": {}, "/api/v1/tasks/:task/children": {}, "/api/v1/tasks/:task/children/:child": {},
	"/api/v1/tasks/:task/ancestors": {}, "/api/v1/tasks/:task/descendants": {}, "/api/v1/tasks/:task/parent": {},
	"/api/v1/tasks/:task/dependencies": {}, "/api/v1/tasks/:task/dependencies/:prerequisite": {},
	"/api/v1/tasks/:task/comments": {}, "/api/v1/tasks/:task/comments/:comment": {}, "/api/v1/tasks/:task/timeline": {},
	"/api/v1/tasks/:task/progress": {}, "/api/v1/tasks/:task/heartbeat": {}, "/api/v1/tasks/:task/claim": {}, "/api/v1/tasks/:task/renew": {},
	"/api/v1/tasks/:task/release": {}, "/api/v1/tasks/:task/complete": {}, "/api/v1/tasks/:task/block": {},
	"/api/v1/tasks/:task/triage": {}, "/api/v1/tasks/:task/resolve": {}, "/api/v1/tasks/:task/reopen": {},
	"/api/v1/labels/:label": {}, "/api/v1/sidebar-counts": {}, "/api/v1/my-work": {}, "/api/v1/roadmap": {}, "/api/v1/events": {}, "/api/v1/agents": {},
	"/api/v1/agents/:agent/tokens": {}, "/api/v1/tokens/:token": {},
}

var metricDynamicSegments = map[string]string{
	"projects":       ":project",
	"tasks":          ":task",
	"columns":        ":column",
	"labels":         ":label",
	"tokens":         ":token",
	"audits":         ":audit",
	"audit-findings": ":finding",
	"agents":         ":agent",
	"views":          ":view",
	"saved-views":    ":view",
	"checklist":      ":item",
	"checklists":     ":item",
	"children":       ":child",
	"dependencies":   ":prerequisite",
	"comments":       ":comment",
}

var proxyIdentityHeaders = [...]string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Real-IP",
	"True-Client-IP",
	"CF-Connecting-IP",
	"Cf-Access-Jwt-Assertion",
	"Cf-Access-Authenticated-User-Email",
}

type requestMetricKey struct {
	method string
	route  string
	status int
}

type requestMetricValue struct {
	count   uint64
	sum     float64
	buckets [len(metricHistogramBuckets)]uint64
}

// metricsRegistry is process-local by design.  It has bounded label sets and
// never uses actor IDs, project keys, task titles, request bodies, or query
// strings as labels.  A restart resets counters, while the persistent
// database remains the source of truth for application state.
type metricsRegistry struct {
	mu sync.Mutex

	requests       map[requestMetricKey]*requestMetricValue
	httpErrors     map[requestMetricKey]uint64
	authFailures   map[string]uint64
	rateLimit      map[string]uint64
	agentMutations map[string]uint64
	readiness      map[readinessMetricKey]uint64

	databaseLockCount uint64
	databaseLockSum   float64
	databaseLockError uint64
}

type readinessMetricKey struct {
	check  string
	status string
}

func newMetricsRegistry() *metricsRegistry {
	return &metricsRegistry{
		requests:       make(map[requestMetricKey]*requestMetricValue),
		httpErrors:     make(map[requestMetricKey]uint64),
		authFailures:   make(map[string]uint64),
		rateLimit:      make(map[string]uint64),
		agentMutations: make(map[string]uint64),
		readiness:      make(map[readinessMetricKey]uint64),
	}
}

func (m *metricsRegistry) recordRequest(method, rawPath string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	seconds := duration.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		seconds = 0
	}
	key := requestMetricKey{method: safeMetricMethod(method), route: metricRoute(rawPath), status: status}
	m.mu.Lock()
	defer m.mu.Unlock()
	value := m.requests[key]
	if value == nil {
		value = &requestMetricValue{}
		m.requests[key] = value
	}
	value.count++
	value.sum += seconds
	for index, boundary := range metricHistogramBuckets {
		if seconds <= boundary {
			value.buckets[index]++
			break
		}
	}
	if status >= 400 {
		m.httpErrors[key]++
	}
}

func (m *metricsRegistry) recordAuthFailure(reason string) {
	if m == nil {
		return
	}
	reason = safeMetricLabel(reason, "unknown")
	m.mu.Lock()
	m.authFailures[reason]++
	m.mu.Unlock()
}

func (m *metricsRegistry) recordRateLimit(kind string) {
	if m == nil {
		return
	}
	kind = safeMetricLabel(kind, "unknown")
	m.mu.Lock()
	m.rateLimit[kind]++
	m.mu.Unlock()
}

func (m *metricsRegistry) recordAgentMutation(outcome string) {
	if m == nil {
		return
	}
	outcome = safeMetricLabel(outcome, "unknown")
	m.mu.Lock()
	m.agentMutations[outcome]++
	m.mu.Unlock()
}

func (m *metricsRegistry) recordDatabaseLock(duration time.Duration, err error) {
	if m == nil {
		return
	}
	seconds := duration.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		seconds = 0
	}
	m.mu.Lock()
	m.databaseLockCount++
	m.databaseLockSum += seconds
	if err != nil {
		m.databaseLockError++
	}
	m.mu.Unlock()
}

func (m *metricsRegistry) recordReadiness(report readinessReport) {
	if m == nil {
		return
	}
	m.mu.Lock()
	for check, value := range report.Checks {
		status := value.Status
		if status == "" {
			status = "unknown"
		}
		m.readiness[readinessMetricKey{check: safeMetricLabel(check, "unknown"), status: safeMetricLabel(status, "unknown")}]++
	}
	m.mu.Unlock()
}

func (m *metricsRegistry) render(database databaseMetricSnapshot, revision string) string {
	if m == nil {
		m = newMetricsRegistry()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var output strings.Builder
	output.WriteString("# HELP helm_http_requests_total Total HTTP requests handled by Helm.\n")
	output.WriteString("# TYPE helm_http_requests_total counter\n")
	requestKeys := sortedRequestKeys(m.requests)
	for _, key := range requestKeys {
		value := m.requests[key]
		fmt.Fprintf(&output, "helm_http_requests_total{method=%q,route=%q,status=%q} %d\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), value.count)
	}

	output.WriteString("# HELP helm_http_errors_total HTTP responses with a client or server error status.\n")
	output.WriteString("# TYPE helm_http_errors_total counter\n")
	for _, key := range sortedUintRequestKeys(m.httpErrors) {
		fmt.Fprintf(&output, "helm_http_errors_total{method=%q,route=%q,status=%q} %d\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), m.httpErrors[key])
	}

	output.WriteString("# HELP helm_http_request_duration_seconds HTTP request duration.\n")
	output.WriteString("# TYPE helm_http_request_duration_seconds histogram\n")
	for _, key := range requestKeys {
		value := m.requests[key]
		for index, boundary := range metricHistogramBuckets {
			fmt.Fprintf(&output, "helm_http_request_duration_seconds_bucket{method=%q,route=%q,status=%q,le=%q} %d\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), formatFloat(boundary), histogramCumulative(value.buckets[:], index))
		}
		fmt.Fprintf(&output, "helm_http_request_duration_seconds_bucket{method=%q,route=%q,status=%q,le=\"+Inf\"} %d\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), value.count)
		fmt.Fprintf(&output, "helm_http_request_duration_seconds_sum{method=%q,route=%q,status=%q} %s\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), formatFloat(value.sum))
		fmt.Fprintf(&output, "helm_http_request_duration_seconds_count{method=%q,route=%q,status=%q} %d\n", prometheusLabel(key.method), prometheusLabel(key.route), strconv.Itoa(key.status), value.count)
	}

	writeStringCounter(&output, "helm_auth_failures_total", "Authentication failures by bounded reason.", "reason", m.authFailures)
	writeStringCounter(&output, "helm_rate_limit_failures_total", "Rate-limit rejections by bounded scope.", "scope", m.rateLimit)
	writeStringCounter(&output, "helm_agent_mutations_total", "Agent mutation pressure by bounded outcome.", "outcome", m.agentMutations)
	output.WriteString("# HELP helm_agent_mutation_pressure_ratio Fraction of observed agent mutation attempts rejected by a limit.\n")
	output.WriteString("# TYPE helm_agent_mutation_pressure_ratio gauge\n")
	attempts := m.agentMutations["attempted"]
	rejections := m.agentMutations["rejected_rate_limit"] + m.agentMutations["rejected_resource_limit"]
	pressure := float64(0)
	if attempts > 0 {
		pressure = float64(rejections) / float64(attempts)
	}
	fmt.Fprintf(&output, "helm_agent_mutation_pressure_ratio %s\n", formatFloat(pressure))

	output.WriteString("# HELP helm_database_lock_latency_seconds SQLite writer-lock acquisition latency.\n")
	output.WriteString("# TYPE helm_database_lock_latency_seconds summary\n")
	fmt.Fprintf(&output, "helm_database_lock_latency_seconds_sum %s\n", formatFloat(m.databaseLockSum))
	fmt.Fprintf(&output, "helm_database_lock_latency_seconds_count %d\n", m.databaseLockCount)
	output.WriteString("# HELP helm_database_lock_errors_total SQLite writer-lock acquisition failures.\n")
	output.WriteString("# TYPE helm_database_lock_errors_total counter\n")
	fmt.Fprintf(&output, "helm_database_lock_errors_total %d\n", m.databaseLockError)

	output.WriteString("# HELP helm_readiness_checks_total Readiness checks by name and result.\n")
	output.WriteString("# TYPE helm_readiness_checks_total counter\n")
	readinessKeys := make([]readinessMetricKey, 0, len(m.readiness))
	for key := range m.readiness {
		readinessKeys = append(readinessKeys, key)
	}
	sort.Slice(readinessKeys, func(i, j int) bool {
		if readinessKeys[i].check == readinessKeys[j].check {
			return readinessKeys[i].status < readinessKeys[j].status
		}
		return readinessKeys[i].check < readinessKeys[j].check
	})
	for _, key := range readinessKeys {
		fmt.Fprintf(&output, "helm_readiness_checks_total{check=%q,status=%q} %d\n", prometheusLabel(key.check), prometheusLabel(key.status), m.readiness[key])
	}

	output.WriteString("# HELP helm_database_size_bytes SQLite logical database size from page usage.\n")
	output.WriteString("# TYPE helm_database_size_bytes gauge\n")
	fmt.Fprintf(&output, "helm_database_size_bytes %d\n", database.DatabaseBytes)
	output.WriteString("# HELP helm_database_wal_bytes SQLite WAL sidecar size.\n")
	output.WriteString("# TYPE helm_database_wal_bytes gauge\n")
	fmt.Fprintf(&output, "helm_database_wal_bytes %d\n", database.WALBytes)
	output.WriteString("# HELP helm_database_page_count SQLite page count.\n")
	output.WriteString("# TYPE helm_database_page_count gauge\n")
	fmt.Fprintf(&output, "helm_database_page_count %d\n", database.PageCount)
	output.WriteString("# HELP helm_database_max_page_count SQLite configured page ceiling.\n")
	output.WriteString("# TYPE helm_database_max_page_count gauge\n")
	fmt.Fprintf(&output, "helm_database_max_page_count %d\n", database.MaxPageCount)
	output.WriteString("# HELP helm_database_page_usage_ratio SQLite page usage divided by configured ceiling.\n")
	output.WriteString("# TYPE helm_database_page_usage_ratio gauge\n")
	fmt.Fprintf(&output, "helm_database_page_usage_ratio %s\n", formatFloat(database.PageUsageRatio))
	output.WriteString("# HELP helm_database_pool_open_connections Current database/sql open connections.\n")
	output.WriteString("# TYPE helm_database_pool_open_connections gauge\n")
	fmt.Fprintf(&output, "helm_database_pool_open_connections %d\n", database.OpenConnections)
	output.WriteString("# HELP helm_database_pool_wait_count Database/sql connection wait count.\n")
	output.WriteString("# TYPE helm_database_pool_wait_count counter\n")
	fmt.Fprintf(&output, "helm_database_pool_wait_count %d\n", database.WaitCount)

	if revision != "" {
		output.WriteString("# HELP helm_build_info Helm build revision.\n")
		output.WriteString("# TYPE helm_build_info gauge\n")
		fmt.Fprintf(&output, "helm_build_info{revision=%q} 1\n", prometheusLabel(revision))
	}
	return output.String()
}

func histogramCumulative(buckets []uint64, index int) uint64 {
	var total uint64
	for position := 0; position <= index && position < len(buckets); position++ {
		total += buckets[position]
	}
	return total
}

func writeStringCounter(output *strings.Builder, name, help, label string, values map[string]uint64) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(output, "%s{%s=%q} %d\n", name, label, prometheusLabel(key), values[key])
	}
}

func sortedRequestKeys(values map[requestMetricKey]*requestMetricValue) []requestMetricKey {
	keys := make([]requestMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		return keys[i].status < keys[j].status
	})
	return keys
}

func sortedUintRequestKeys(values map[requestMetricKey]uint64) []requestMetricKey {
	keys := make([]requestMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].method != keys[j].method {
			return keys[i].method < keys[j].method
		}
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		return keys[i].status < keys[j].status
	})
	return keys
}

func safeMetricMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect,
		http.MethodTrace:
		return method
	default:
		// Do not accept arbitrary extension method names as labels. A caller can
		// otherwise create an unbounded number of process-local time series.
		return "OTHER"
	}
}

func safeMetricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 48 {
		return fallback
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return fallback
		}
	}
	return value
}

func prometheusLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func formatFloat(value float64) string {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func metricRoute(rawPath string) string {
	clean := path.Clean("/" + strings.TrimSpace(strings.SplitN(rawPath, "?", 2)[0]))
	if clean == "." || clean == "" {
		return "/"
	}
	if clean == "/healthz" || clean == "/health" || clean == "/readyz" || clean == "/ready" || clean == "/metrics" || clean == "/openapi.json" {
		return clean
	}
	if clean == "/api/v1" {
		return clean
	}
	if !strings.HasPrefix(clean, "/api/v1/") {
		return "/static"
	}
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if len(parts) < 3 {
		return "/api/v1/other"
	}
	for index := 3; index < len(parts); index++ {
		// Reorder is a literal action under both checklist aliases, not an item
		// identifier. Keep it literal before applying the generic segment map.
		if parts[index] == "reorder" && (parts[index-1] == "checklist" || parts[index-1] == "checklists") {
			continue
		}
		if replacement, ok := metricDynamicSegments[parts[index-1]]; ok {
			parts[index] = replacement
		}
	}
	result := "/" + strings.Join(parts, "/")
	if len(result) > maxMetricRouteLength {
		return "/api/v1/other"
	}
	if _, ok := metricRouteTemplates[result]; !ok {
		return "/api/v1/other"
	}
	return result
}

func (s *Server) metricsValue() *metricsRegistry {
	s.metricsOnce.Do(func() {
		if s.metrics == nil {
			s.metrics = newMetricsRegistry()
		}
	})
	return s.metrics
}

func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	identity, authenticated := requestIdentity(r)
	if !isLoopbackRequest(r) && !authenticated {
		var err error
		var admitted bool
		identity, err, admitted = s.authenticateRequest(w, r)
		if !admitted {
			return
		}
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		authenticated = true
	}
	_ = identity
	_ = authenticated
	database := s.collectDatabaseMetrics(r.Context())
	body := s.metricsValue().render(database, s.Cfg.ReleaseSHA)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", metricsContentType)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = ioWriteString(w, body)
	}
}

// ioWriteString is kept as a tiny adapter so metrics writing remains easy to
// exercise with ResponseRecorder and never needs to buffer user data.
func ioWriteString(w http.ResponseWriter, value string) (int, error) {
	return w.Write([]byte(value))
}

func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return false
	}
	// A reverse proxy commonly connects over loopback while forwarding an
	// external caller. Treat forwarding and Access identity headers as proof
	// that the peer is a proxy, not as a reason to grant the unauthenticated
	// local-monitoring exception. The headers are never trusted for identity.
	for _, header := range proxyIdentityHeaders {
		if len(r.Header.Values(header)) > 0 {
			return false
		}
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	} else {
		host = strings.Trim(host, "[]")
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type databaseMetricSnapshot struct {
	DatabaseBytes    int64
	WALBytes         int64
	WALCapacityBytes int64
	PageCount        int64
	MaxPageCount     int64
	PageUsageRatio   float64
	OpenConnections  int
	WaitCount        int64
}

func (s *Server) collectDatabaseMetrics(ctx context.Context) databaseMetricSnapshot {
	snapshot := databaseMetricSnapshot{}
	if s == nil || s.Store == nil || s.Store.DB == nil {
		return snapshot
	}
	if value, err := pragmaInt64(ctx, s.Store.DB, "page_count"); err == nil {
		snapshot.PageCount = nonNegativeInt64(value)
	}
	if value, err := pragmaInt64(ctx, s.Store.DB, "max_page_count"); err == nil {
		snapshot.MaxPageCount = nonNegativeInt64(value)
	}
	if value, err := pragmaInt64(ctx, s.Store.DB, "journal_size_limit"); err == nil {
		snapshot.WALCapacityBytes = nonNegativeInt64(value)
	}
	pageSize, _ := pragmaInt64(ctx, s.Store.DB, "page_size")
	if pageSize > 0 && snapshot.PageCount > 0 && snapshot.PageCount <= math.MaxInt64/pageSize {
		snapshot.DatabaseBytes = snapshot.PageCount * pageSize
	}
	if snapshot.MaxPageCount > 0 && snapshot.PageCount >= 0 {
		snapshot.PageUsageRatio = boundedRatio(float64(snapshot.PageCount) / float64(snapshot.MaxPageCount))
	}
	if databasePath := databaseFilesystemPath(s.Cfg.DB); databasePath != "" {
		snapshot.WALBytes = fileSize(databasePath + "-wal")
		if actualSize := fileSize(databasePath); actualSize > snapshot.DatabaseBytes {
			snapshot.DatabaseBytes = actualSize
		}
	}
	stats := s.Store.DB.Stats()
	snapshot.OpenConnections = stats.OpenConnections
	snapshot.WaitCount = stats.WaitCount
	return snapshot
}

func pragmaInt64(ctx context.Context, database *sql.DB, name string) (int64, error) {
	if database == nil {
		return 0, errors.New("database unavailable")
	}
	allowed := map[string]struct{}{"page_count": {}, "max_page_count": {}, "page_size": {}, "freelist_count": {}, "journal_size_limit": {}}
	if _, ok := allowed[name]; !ok {
		return 0, errors.New("unsupported pragma")
	}
	var value int64
	if err := database.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
		return 0, err
	}
	return value, nil
}

func nonNegativeInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func fileSize(name string) int64 {
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 {
		return 0
	}
	return info.Size()
}

func databaseFilesystemPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == ":memory:" {
		return ""
	}
	if strings.HasPrefix(value, "file:") {
		value = strings.TrimPrefix(value, "file:")
		if index := strings.IndexByte(value, '?'); index >= 0 {
			value = value[:index]
		}
		if decoded, err := url.PathUnescape(value); err == nil {
			value = decoded
		}
	}
	if value == "" || value == ":memory:" || strings.Contains(value, "mode=memory") {
		return ""
	}
	return value
}

type readinessCheck struct {
	Status                string  `json:"status"`
	Message               string  `json:"message,omitempty"`
	LatencyMS             int64   `json:"latency_ms,omitempty"`
	LockLatencyMS         int64   `json:"lock_latency_ms,omitempty"`
	SchemaVersion         *int    `json:"schema_version,omitempty"`
	ExpectedSchemaVersion *int    `json:"expected_schema_version,omitempty"`
	MigrationState        string  `json:"migration_state,omitempty"`
	PendingCount          int     `json:"pending_count,omitempty"`
	UnknownCount          int     `json:"unknown_count,omitempty"`
	PendingVersions       []int   `json:"pending_versions,omitempty"`
	UnknownVersions       []int   `json:"unknown_versions,omitempty"`
	AvailableBytes        int64   `json:"available_bytes,omitempty"`
	RequiredFreeBytes     int64   `json:"required_free_bytes,omitempty"`
	Writable              *bool   `json:"writable,omitempty"`
	DatabaseBytes         int64   `json:"database_bytes,omitempty"`
	WALBytes              int64   `json:"wal_bytes,omitempty"`
	PageCount             int64   `json:"page_count,omitempty"`
	MaxPageCount          int64   `json:"max_page_count,omitempty"`
	PageUsageRatio        float64 `json:"page_usage_ratio,omitempty"`
	JournalMode           string  `json:"journal_mode,omitempty"`
}

type readinessReport struct {
	Status   string                    `json:"status"`
	Service  string                    `json:"service"`
	Revision string                    `json:"revision,omitempty"`
	Ready    bool                      `json:"ready"`
	Checks   map[string]readinessCheck `json:"checks"`
}

func (s *Server) readiness(ctx context.Context) readinessReport {
	report := readinessReport{Status: "ok", Service: "helm", Ready: true, Checks: make(map[string]readinessCheck)}
	if s.Cfg.ReleaseSHA != "" {
		report.Revision = s.Cfg.ReleaseSHA
	}
	fail := func(name string, check readinessCheck) {
		report.Checks[name] = check
		if check.Status != "ok" {
			report.Status = "degraded"
			report.Ready = false
		}
	}

	databaseCheck := readinessCheck{Status: "ok"}
	if s.Store == nil || s.Store.DB == nil {
		databaseCheck.Status = "degraded"
		databaseCheck.Message = "database is unavailable"
		fail("database", databaseCheck)
	} else {
		started := time.Now()
		if err := s.Store.DB.PingContext(ctx); err != nil {
			databaseCheck.Status = "degraded"
			databaseCheck.Message = "database ping failed"
		} else {
			databaseCheck.LatencyMS = boundedMilliseconds(time.Since(started))
			lockStarted := time.Now()
			lockErr := acquireWriterLock(ctx, s.Store.DB)
			lockLatency := time.Since(lockStarted)
			databaseCheck.LockLatencyMS = boundedMilliseconds(lockLatency)
			s.metricsValue().recordDatabaseLock(lockLatency, lockErr)
			if lockErr != nil {
				databaseCheck.Status = "degraded"
				databaseCheck.Message = "database writer lock unavailable"
			} else if lockLatency >= readinessLockWarn {
				databaseCheck.Status = "degraded"
				databaseCheck.Message = "database writer lock is slow"
			}
		}
		fail("database", databaseCheck)
	}

	schemaCheck := readinessCheck{Status: "ok", MigrationState: "current"}
	var inspection db.SchemaInspection
	var inspectionErr error
	if s.Store == nil || s.Store.DB == nil {
		inspectionErr = errors.New("database unavailable")
	} else {
		inspection, inspectionErr = db.InspectSchema(ctx, s.Store.DB)
	}
	if inspectionErr != nil {
		schemaCheck.Status = "degraded"
		schemaCheck.Message = "schema inspection failed"
		schemaCheck.MigrationState = "unreadable"
	} else {
		schemaVersion := inspection.SchemaVersion
		expectedVersion := inspection.EmbeddedSchemaVersion
		schemaCheck.SchemaVersion = &schemaVersion
		schemaCheck.ExpectedSchemaVersion = &expectedVersion
		schemaCheck.PendingCount = len(inspection.PendingVersions)
		schemaCheck.UnknownCount = len(inspection.UnknownVersions)
		schemaCheck.PendingVersions = boundedVersions(inspection.PendingVersions)
		schemaCheck.UnknownVersions = boundedVersions(inspection.UnknownVersions)
		if len(inspection.UnknownVersions) > 0 {
			// InspectSchema and the migration layer explicitly preserve the
			// retained-binary rollback path for newer additive migrations. Keep
			// this as a visible compatibility warning without taking a usable
			// instance out of readiness; pending embedded migrations remain a
			// hard failure below.
			schemaCheck.Message = "database includes newer additive migrations"
			schemaCheck.MigrationState = "unknown"
		}
		if len(inspection.PendingVersions) > 0 || inspection.SchemaVersion < inspection.EmbeddedSchemaVersion {
			schemaCheck.Status = "degraded"
			schemaCheck.Message = "database schema is not current"
			schemaCheck.MigrationState = "pending"
		}
	}
	fail("schema_compatibility", schemaCheck)
	// Keep the short check name available for simple probes while retaining the
	// explicit name used by the operator contract.
	fail("schema", schemaCheck)

	migrationCheck := schemaCheck
	if migrationCheck.Status == "ok" && len(inspection.UnknownVersions) == 0 {
		migrationCheck.Message = "embedded migrations are current"
	}
	fail("migration", migrationCheck)

	capacity := inspectFilesystemCapacity(databaseFilesystemPath(s.Cfg.DB))
	fail("writable_capacity", writableCapacityReadinessCheck(capacity))

	storage := s.collectDatabaseMetrics(ctx)
	fail("storage", storageReadinessCheck(storage))

	if report.Ready {
		report.Status = "ok"
	}
	s.metricsValue().recordReadiness(report)
	return report
}

func boundedVersions(values []int) []int {
	if len(values) <= readinessListLimit {
		return append([]int(nil), values...)
	}
	return append([]int(nil), values[:readinessListLimit]...)
}

func boundedMilliseconds(duration time.Duration) int64 {
	if duration < 0 {
		return 0
	}
	value := duration.Milliseconds()
	if value > 120_000 {
		return 120_000
	}
	return value
}

func acquireWriterLock(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return errors.New("database unavailable")
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	var previousBusyTimeout int
	if err := connection.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&previousBusyTimeout); err != nil {
		return err
	}
	// The probe deliberately uses a short timeout, but the pooled connection
	// must be restored before it is returned to application traffic. Otherwise
	// each readiness request would silently make one writer connection more
	// likely to fail under normal contention.
	defer func() {
		_, _ = connection.ExecContext(context.Background(), fmt.Sprintf("PRAGMA busy_timeout = %d", previousBusyTimeout))
	}()
	if _, err := connection.ExecContext(ctx, "PRAGMA busy_timeout = 250"); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	_, rollbackErr := connection.ExecContext(context.Background(), "ROLLBACK")
	return rollbackErr
}

type filesystemCapacity struct {
	Applicable     bool
	Writable       bool
	AvailableBytes int64
	Err            error
}

func writableCapacityReadinessCheck(capacity filesystemCapacity) readinessCheck {
	check := readinessCheck{Status: "ok", RequiredFreeBytes: readinessMinFreeBytes}
	if capacity.Applicable {
		check.AvailableBytes = nonNegativeInt64(capacity.AvailableBytes)
		writable := capacity.Writable
		check.Writable = &writable
		if !capacity.Writable {
			check.Status = "degraded"
			check.Message = "database directory is not writable"
		} else if capacity.Err != nil {
			check.Status = "degraded"
			check.Message = "database filesystem capacity is unavailable"
		} else if check.AvailableBytes < readinessMinFreeBytes {
			check.Status = "degraded"
			check.Message = "database filesystem capacity is low"
		}
	} else if capacity.Err != nil {
		check.Status = "degraded"
		check.Message = "database filesystem path is unavailable"
	}
	return check
}

func storageReadinessCheck(storage databaseMetricSnapshot) readinessCheck {
	check := readinessCheck{
		Status:         "ok",
		DatabaseBytes:  nonNegativeInt64(storage.DatabaseBytes),
		WALBytes:       nonNegativeInt64(storage.WALBytes),
		PageCount:      nonNegativeInt64(storage.PageCount),
		MaxPageCount:   nonNegativeInt64(storage.MaxPageCount),
		PageUsageRatio: boundedRatio(storage.PageUsageRatio),
	}
	if check.PageUsageRatio >= 1 {
		check.Status = "degraded"
		check.Message = "database page capacity is exhausted"
	} else {
		walCapacity := storage.WALCapacityBytes
		if walCapacity <= 0 {
			// Keep a conservative fallback for drivers that do not expose the
			// journal_size_limit pragma; db.Open configures this same limit.
			walCapacity = readinessStorageWALCap
		}
		if check.WALBytes >= walCapacity {
			check.Status = "degraded"
			check.Message = "database WAL capacity is exhausted"
		}
	}
	return check
}

func boundedRatio(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value > 1e9 {
		return 1e9
	}
	return value
}

func inspectFilesystemCapacity(databasePath string) filesystemCapacity {
	if databasePath == "" {
		return filesystemCapacity{}
	}
	result := filesystemCapacity{Applicable: true}
	directory := filepath.Dir(databasePath)
	info, err := os.Stat(directory)
	if err != nil {
		result.Err = err
		return result
	}
	result.Writable = info.IsDir() && info.Mode().Perm()&0222 != 0
	var stats syscall.Statfs_t
	if err := syscall.Statfs(directory, &stats); err != nil {
		result.Err = err
		return result
	}
	if stats.Bsize > 0 && stats.Bavail > 0 {
		blocks := uint64(stats.Bavail)
		blockSize := uint64(stats.Bsize)
		if blocks > math.MaxInt64/blockSize {
			result.AvailableBytes = math.MaxInt64
		} else {
			result.AvailableBytes = int64(blocks * blockSize)
		}
	}
	return result
}

func (s *Server) observeAuthFailure(err error, r *http.Request) {
	reason := "authentication_error"
	if err == nil {
		return
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "required"):
		reason = "missing_credentials"
	case strings.Contains(message, "invalid bearer") || strings.Contains(message, "invalid token"):
		reason = "invalid_token"
	case strings.Contains(message, "disabled"):
		reason = "disabled_actor"
	case strings.Contains(message, "cloudflare"):
		reason = "cloudflare_identity"
	case strings.Contains(message, "unavailable"):
		reason = "auth_unavailable"
	}
	if r != nil && strings.TrimSpace(r.Header.Get("Authorization")) != "" && reason == "missing_credentials" {
		reason = "invalid_token"
	}
	s.metricsValue().recordAuthFailure(reason)
}

func (s *Server) logRequest(requestID, method, rawPath string, status int, duration time.Duration, identity auth.Identity, authenticated bool) {
	fields := map[string]any{
		"level":       "info",
		"msg":         "http request",
		"method":      safeMetricMethod(method),
		"route":       metricRoute(rawPath),
		"status":      status,
		"duration_ms": boundedMilliseconds(duration),
		"request_id":  safeLogToken(requestID),
		"actor_kind":  "anonymous",
		"auth":        "none",
	}
	if authenticated {
		fields["actor_kind"] = safeMetricLabel(identity.Actor.Kind, "unknown")
		fields["auth"] = "session"
		if identity.IsToken {
			fields["auth"] = "bearer"
		}
		if identity.Actor.ID != "" {
			sum := sha256.Sum256([]byte(identity.Actor.ID))
			fields["actor_hash"] = hex.EncodeToString(sum[:6])
		}
	}
	s.logJSON(fields)
	s.metricsValue().recordRequest(method, rawPath, status, duration)
}

func (s *Server) logInternalError(w http.ResponseWriter, err error) {
	fields := map[string]any{
		"level":       "error",
		"msg":         "internal error",
		"error_class": classifyError(err),
	}
	if w != nil {
		if requestID := w.Header().Get("X-Request-ID"); requestID != "" {
			fields["request_id"] = safeLogToken(requestID)
		}
	}
	s.logJSON(fields)
}

func (s *Server) logJSON(fields map[string]any) {
	encoded, err := json.Marshal(fields)
	if err != nil {
		log.Printf(`{"level":"error","msg":"log serialization failed"}`)
		return
	}
	log.Printf("%s", encoded)
}

func classifyError(err error) string {
	if err == nil {
		return "unknown"
	}
	value := "internal"
	switch {
	case errors.Is(err, context.Canceled):
		value = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		value = "deadline"
	case errors.Is(err, store.ErrConflict):
		value = "conflict"
	case errors.Is(err, store.ErrNotFound):
		value = "not_found"
	case errors.Is(err, store.ErrInvalid):
		value = "invalid"
	case errors.Is(err, store.ErrForbidden):
		value = "forbidden"
	case errors.Is(err, sql.ErrNoRows):
		value = "not_found"
	}
	if len(value) > maxLoggedErrorClassLength {
		return value[:maxLoggedErrorClassLength]
	}
	return value
}

func safeLogToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "redacted"
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return "redacted"
		}
	}
	return value
}
