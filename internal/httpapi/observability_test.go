package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

func TestMetricsRequiresAuthenticationOffLoopback(t *testing.T) {
	server, _ := testServer(t, "cloudflare")
	server.Auth = auth.NewManagerWithVerifier(server.Store, server.Cfg, staticCloudflareVerifier{
		claims: auth.CloudflareClaims{Email: "owner@example.com", Name: "Owner"},
	})

	unauthenticated := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	unauthenticated.RemoteAddr = "203.0.113.8:4321"
	unauthenticatedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics status = %d, body=%s", unauthenticatedResponse.Code, unauthenticatedResponse.Body.String())
	}

	loopback := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	loopback.RemoteAddr = "127.0.0.1:4321"
	loopbackResponse := httptest.NewRecorder()
	server.ServeHTTP(loopbackResponse, loopback)
	if loopbackResponse.Code != http.StatusOK {
		t.Fatalf("loopback metrics status = %d, body=%s", loopbackResponse.Code, loopbackResponse.Body.String())
	}
	for _, metric := range []string{
		"helm_http_requests_total",
		"helm_http_errors_total",
		"helm_http_request_duration_seconds",
		"helm_auth_failures_total",
		"helm_database_lock_latency_seconds",
		"helm_database_wal_bytes",
		"helm_database_page_usage_ratio",
		"helm_agent_mutations_total",
	} {
		if !strings.Contains(loopbackResponse.Body.String(), metric) {
			t.Errorf("metrics response does not contain %s", metric)
		}
	}
	if strings.Contains(loopbackResponse.Body.String(), "owner@example.com") {
		t.Error("metrics response contains actor email")
	}
}

func TestMetricsAuthenticatedOffLoopback(t *testing.T) {
	server, _ := testServer(t, "cloudflare")
	server.Auth = auth.NewManagerWithVerifier(server.Store, server.Cfg, staticCloudflareVerifier{
		claims: auth.CloudflareClaims{Email: "owner@example.com", Name: "Owner"},
	})
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.RemoteAddr = "203.0.113.8:4321"
	request.Header.Set("Cf-Access-Jwt-Assertion", "test-assertion")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated metrics status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestMetricRoutesUseBoundedTemplates(t *testing.T) {
	tests := map[string]string{
		"/api/v1/tasks/task-secret/comments?query=private": "/api/v1/tasks/:task/comments",
		"/api/v1/projects/project-secret/tasks":            "/api/v1/projects/:project/tasks",
		"/api/v1/tasks/task-secret/checklist/reorder":      "/api/v1/tasks/:task/checklist/reorder",
		"/api/v1/tasks/task-secret/checklists/reorder":     "/api/v1/tasks/:task/checklists/reorder",
		"/api/v1/not-a-route/credential-secret":            "/api/v1/other",
		"/api/v1/tasks/task-secret/private-secret":         "/api/v1/other",
		"/assets/secret.js?token=private":                  "/static",
	}
	for rawPath, want := range tests {
		if got := metricRoute(rawPath); got != want {
			t.Errorf("metricRoute(%q) = %q, want %q", rawPath, got, want)
		}
	}
	registry := newMetricsRegistry()
	registry.recordRequest(http.MethodGet, "/api/v1/tasks/task-secret?query=private", http.StatusOK, 10)
	rendered := registry.render(databaseMetricSnapshot{}, "")
	if strings.Contains(rendered, "task-secret") || strings.Contains(rendered, "private") {
		t.Fatalf("metrics labels leaked request data: %s", rendered)
	}
	if !strings.Contains(rendered, `route="/api/v1/tasks/:task"`) {
		t.Fatalf("metrics route template missing: %s", rendered)
	}
	for index := 0; index < 2000; index++ {
		route := "/api/v1/tasks/task-secret/comments/tasks/invalid-" + strconv.Itoa(index)
		if got := metricRoute(route); got != "/api/v1/other" {
			t.Fatalf("invalid metric route %q = %q, want /api/v1/other", route, got)
		}
	}
}

func TestMetricMethodLabelsAreFinite(t *testing.T) {
	for _, test := range []struct {
		method string
		want   string
	}{
		{method: http.MethodGet, want: http.MethodGet},
		{method: http.MethodHead, want: http.MethodHead},
		{method: "WEBSOCKET", want: "OTHER"},
		{method: "X-" + strings.Repeat("A", 64), want: "OTHER"},
	} {
		if got := safeMetricMethod(test.method); got != test.want {
			t.Errorf("safeMetricMethod(%q) = %q, want %q", test.method, got, test.want)
		}
	}
}

func TestMetricRegistryRendersOperationalSignals(t *testing.T) {
	registry := newMetricsRegistry()
	registry.recordRequest(http.MethodGet, "/readyz", http.StatusServiceUnavailable, 300*time.Millisecond)
	registry.recordAuthFailure("invalid_token")
	registry.recordRateLimit("mutation")
	registry.recordAgentMutation("attempted")
	registry.recordAgentMutation("rejected_rate_limit")
	registry.recordDatabaseLock(300*time.Millisecond, errors.New("database is busy"))
	rendered := registry.render(databaseMetricSnapshot{
		WALBytes:       readinessStorageWALCap,
		PageUsageRatio: 0.8,
	}, "revision")
	for _, sample := range []string{
		`helm_http_errors_total{method="GET",route="/readyz",status="503"} 1`,
		`helm_auth_failures_total{reason="invalid_token"} 1`,
		`helm_rate_limit_failures_total{scope="mutation"} 1`,
		`helm_agent_mutation_pressure_ratio 1`,
		`helm_database_lock_errors_total 1`,
		`helm_database_wal_bytes 67108864`,
		`helm_build_info{revision="revision"} 1`,
	} {
		if !strings.Contains(rendered, sample) {
			t.Errorf("metrics response does not contain %s: %s", sample, rendered)
		}
	}
}

func TestReadinessReportsSchemaAndStorageChecks(t *testing.T) {
	server, data := testServer(t, "disabled")
	response := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, check := range []string{"database", "schema_compatibility", "migration", "writable_capacity", "storage"} {
		if !strings.Contains(response.Body.String(), `"`+check+`"`) {
			t.Errorf("ready response does not contain %s check: %s", check, response.Body.String())
		}
	}
	if _, err := data.DB.ExecContext(context.Background(), `DELETE FROM schema_migrations WHERE version=(SELECT MAX(version) FROM schema_migrations)`); err != nil {
		t.Fatal(err)
	}
	stale := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if stale.Code != http.StatusServiceUnavailable {
		t.Fatalf("stale schema status = %d, body=%s", stale.Code, stale.Body.String())
	}
	if !strings.Contains(stale.Body.String(), `"migration_state":"pending"`) {
		t.Fatalf("stale schema response = %s", stale.Body.String())
	}
}

func TestReadinessAllowsUnknownNewerAdditiveSchema(t *testing.T) {
	server, data := testServer(t, "disabled")
	latest, err := db.LatestEmbeddedSchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := data.DB.ExecContext(context.Background(), `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, latest+1000, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	response := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("newer additive schema status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"migration_state":"unknown"`) || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("newer additive schema response = %s", response.Body.String())
	}
}

func TestReadinessReportsUnavailableDatabase(t *testing.T) {
	server, _ := testServer(t, "disabled")
	if err := server.Store.DB.Close(); err != nil {
		t.Fatal(err)
	}
	response := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable database status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"database":{"status":"degraded"`) {
		t.Fatalf("database response = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sql: database is closed") {
		t.Fatalf("database error leaked into readiness response = %s", response.Body.String())
	}
}

func TestReadinessReportsUnavailableDatabaseCapacity(t *testing.T) {
	server, _ := testServer(t, "disabled")
	directory := t.TempDir()
	server.Cfg.DB = filepath.Join(directory, "missing", "roadmap.db")
	response := request(t, server, http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable capacity status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"writable_capacity":{"status":"degraded"`) {
		t.Fatalf("capacity response = %s", response.Body.String())
	}
}

func TestMetricsDoesNotTrustLoopbackProxyHeaders(t *testing.T) {
	server, _ := testServer(t, "cloudflare")
	server.Auth = auth.NewManagerWithVerifier(server.Store, server.Cfg, rejectingCloudflareVerifier{})
	for _, header := range proxyIdentityHeaders {
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		request.RemoteAddr = "127.0.0.1:4321"
		request.Header.Set(header, "203.0.113.8")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("loopback proxy header %s status = %d, body=%s", header, response.Code, response.Body.String())
		}
	}
}

type rejectingCloudflareVerifier struct{}

func (rejectingCloudflareVerifier) Verify(context.Context, string) (auth.CloudflareClaims, error) {
	return auth.CloudflareClaims{}, errors.New("invalid assertion")
}

func TestStatusWriterPreservesResponseSemantics(t *testing.T) {
	firstRecorder := httptest.NewRecorder()
	firstWriter := &statusWriter{ResponseWriter: firstRecorder}
	firstWriter.WriteHeader(http.StatusTeapot)
	firstWriter.WriteHeader(http.StatusServiceUnavailable)
	if firstRecorder.Code != http.StatusTeapot || responseStatus(firstWriter) != http.StatusTeapot {
		t.Fatalf("status writer changed first final status: recorder=%d wrapper=%d", firstRecorder.Code, responseStatus(firstWriter))
	}

	recorder := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: recorder}
	writer.WriteHeader(http.StatusEarlyHints)
	writer.WriteHeader(http.StatusOK)
	writer.WriteHeader(http.StatusTeapot)
	writer.WriteHeader(http.StatusServiceUnavailable)
	if _, err := writer.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusEarlyHints || responseStatus(writer) != http.StatusOK {
		t.Fatalf("status writer changed first status: recorder=%d wrapper=%d", recorder.Code, responseStatus(writer))
	}
	if writer.Unwrap() != recorder {
		t.Fatal("status writer does not expose the underlying writer")
	}
	if err := http.NewResponseController(writer).Flush(); err != nil {
		t.Fatalf("response controller could not flush through status writer: %v", err)
	}
}

func TestReadinessCapacityThresholdsAreDeterministic(t *testing.T) {
	lowDisk := writableCapacityReadinessCheck(filesystemCapacity{
		Applicable:     true,
		Writable:       true,
		AvailableBytes: readinessMinFreeBytes - 1,
	})
	if lowDisk.Status != "degraded" || lowDisk.Message != "database filesystem capacity is low" {
		t.Fatalf("low-disk readiness = %#v", lowDisk)
	}

	fullDatabase := storageReadinessCheck(databaseMetricSnapshot{PageUsageRatio: 1})
	if fullDatabase.Status != "degraded" || fullDatabase.Message != "database page capacity is exhausted" {
		t.Fatalf("full-database readiness = %#v", fullDatabase)
	}

	fullWAL := storageReadinessCheck(databaseMetricSnapshot{WALBytes: readinessStorageWALCap})
	if fullWAL.Status != "degraded" || fullWAL.Message != "database WAL capacity is exhausted" {
		t.Fatalf("full-WAL readiness = %#v", fullWAL)
	}
}

func TestObservabilityLogsRedactErrorsAndActorIdentity(t *testing.T) {
	server, _ := testServer(t, "disabled")
	var output bytes.Buffer
	prior := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(prior) })
	server.logInternalError(httptest.NewRecorder(), errors.New("task body must never enter logs"))
	server.logRequest("request-123", http.MethodGet, "/api/v1/tasks/task-secret/comments", http.StatusInternalServerError, 10, auth.Identity{Actor: structActor("agent-secret")}, true)
	logged := output.String()
	if strings.Contains(logged, "task body must never enter logs") || strings.Contains(logged, "agent-secret") || strings.Contains(logged, "task-secret") {
		t.Fatalf("sensitive value leaked into logs: %s", logged)
	}
	if !strings.Contains(logged, `"request_id":"request-123"`) || !strings.Contains(logged, `"route":"/api/v1/tasks/:task/comments"`) {
		t.Fatalf("structured request context missing: %s", logged)
	}
}

func structActor(id string) store.Actor {
	return store.Actor{ID: id, Kind: "agent"}
}
