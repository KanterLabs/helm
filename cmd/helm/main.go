package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KanterLabs/helm/internal/auth"
	"github.com/KanterLabs/helm/internal/codexruntime"
	"github.com/KanterLabs/helm/internal/config"
	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/httpapi"
	"github.com/KanterLabs/helm/internal/store"
)

const (
	healthcheckURL     = "http://127.0.0.1:8080/healthz"
	readinesscheckURL  = "http://127.0.0.1:8080/readyz"
	healthcheckTimeout = 2 * time.Second
	healthcheckMaxBody = 64 * 1024
)

func main() {
	log.SetFlags(0)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			if len(os.Args) != 2 {
				fatalLog("healthcheck does not accept arguments", "invalid_arguments")
			}
			if err := runHealthcheck(); err != nil {
				errorLog("healthcheck failed", err)
				os.Exit(1)
			}
			return
		case "migration-info":
			if len(os.Args) != 2 {
				fatalLog("migration-info does not accept arguments", "invalid_arguments")
			}
			if err := runMigrationInfo(os.Stdout); err != nil {
				errorLog("migration info failed", err)
				os.Exit(1)
			}
			return
		case "schema-preflight", "migration-preflight":
			if len(os.Args) != 3 {
				fatalLog("schema preflight requires exactly one database path", "invalid_arguments")
			}
			if err := runSchemaPreflight(context.Background(), os.Args[2], os.Stdout); err != nil {
				errorLog("schema preflight failed", err)
				os.Exit(1)
			}
			return
		case "migration-apply":
			if len(os.Args) != 3 {
				fatalLog("migration apply requires exactly one staged database path", "invalid_arguments")
			}
			if err := runMigrationApply(context.Background(), os.Args[2], os.Stdout); err != nil {
				errorLog("migration apply failed", err)
				os.Exit(1)
			}
			return
		default:
			fatalLog("unknown command", "invalid_command")
		}
	}
	cfg, err := config.FromEnv()
	if err != nil {
		fatalLog("configuration failed", classifyMainError(err))
	}
	ctx := context.Background()
	database, err := db.Open(ctx, cfg.DB)
	if err != nil {
		fatalLog("database open failed", classifyMainError(err))
	}
	defer database.Close()
	data := store.New(database)
	if cfg.DemoSeed {
		// Demo data must not create a passwordless human in local-auth mode;
		// doing so would make first-run setup appear complete with no usable
		// login. Disabled mode already has an explicit development actor.
		seedActorID := ""
		if cfg.AuthMode == "disabled" {
			actor, seedErr := data.EnsureDisabledActor(ctx)
			if seedErr != nil {
				fatalLog("demo actor seed failed", classifyMainError(seedErr))
			}
			seedActorID = actor.ID
		}
		if seedErr := data.SeedDemo(ctx, seedActorID); seedErr != nil {
			fatalLog("demo data seed failed", classifyMainError(seedErr))
		}
	}
	manager := auth.NewManager(data, cfg)
	codexManager := codexruntime.NewManager(codexruntime.Options{
		Binary:     cfg.CodexBinary,
		HomeRoot:   cfg.CodexHomeRoot,
		WorkingDir: ".",
	})
	api := httpapi.New(data, manager, cfg, codexManager)
	server := &http.Server{Addr: cfg.Addr, Handler: api, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 * 1024}
	var privateServer *http.Server
	var privateListener net.Listener
	if cfg.AuthMode == "tailnet" {
		privateHandler, handlerErr := httpapi.NewTailnetPrivateHandler(api, cfg.TailnetAllowedPeerIPs)
		if handlerErr != nil {
			fatalLog("tailnet private listener configuration failed", classifyMainError(handlerErr))
		}
		certificate, certErr := tls.LoadX509KeyPair(cfg.TailnetTLSCertFile, cfg.TailnetTLSKeyFile)
		if certErr != nil {
			fatalLog("tailnet private TLS configuration failed", classifyMainError(certErr))
		}
		privateListener, err = net.Listen("tcp", cfg.TailnetTLSAddr)
		if err != nil {
			fatalLog("tailnet private listener failed", classifyMainError(err))
		}
		privateListener = tls.NewListener(privateListener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
		privateServer = &http.Server{Handler: privateHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 * 1024}
	}
	go func() {
		log.Printf(`{"level":"info","msg":"helm listening","addr":%q}`, cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorLog("server stopped unexpectedly", err)
			os.Exit(1)
		}
	}()
	if privateServer != nil {
		go func() {
			log.Printf(`{"level":"info","msg":"helm tailnet private listener started","addr":%q}`, cfg.TailnetTLSAddr)
			if serveErr := privateServer.Serve(privateListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
				errorLog("tailnet private listener stopped unexpectedly", serveErr)
				os.Exit(1)
			}
		}()
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-signalCtx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		errorLog("server shutdown failed", err)
	}
	if privateServer != nil {
		if err := privateServer.Shutdown(shutdownCtx); err != nil {
			errorLog("tailnet private listener shutdown failed", err)
		}
	}
	if err := codexManager.Close(shutdownCtx); err != nil {
		errorLog("Codex shutdown failed", err)
	}
}

func fatalLog(message, class string) {
	errorLogClass(message, class)
	os.Exit(1)
}

func errorLog(message string, err error) {
	errorLogClass(message, classifyMainError(err))
}

func errorLogClass(message, class string) {
	if class == "" {
		class = "internal"
	}
	log.Printf(`{"level":"error","msg":%q,"error_class":%q}`, message, class)
}

func classifyMainError(err error) string {
	if err == nil {
		return "unknown"
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case errors.Is(err, os.ErrPermission):
		return "permission"
	default:
		return "internal"
	}
}

func runMigrationInfo(output io.Writer) error {
	version, digest, err := db.EmbeddedSchema()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "latest_schema_version=%d\nmigration_digest=%s\n", version, digest)
	return err
}

func runSchemaPreflight(ctx context.Context, sourcePath string, output io.Writer) error {
	inspection, err := db.Preflight(ctx, sourcePath)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "schema_version=%d\nlatest_schema_version=%d\nmigration_digest=%s\nintegrity_check=ok\nforeign_key_check=ok\nstatus=ok\n", inspection.SchemaVersion, inspection.EmbeddedSchemaVersion, inspection.MigrationDigest); err != nil {
		return err
	}
	return nil
}

func runMigrationApply(ctx context.Context, candidatePath string, output io.Writer) error {
	inspection, err := db.MigrateCandidate(ctx, candidatePath)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "schema_version=%d\nlatest_schema_version=%d\nmigration_digest=%s\nintegrity_check=ok\nforeign_key_check=ok\nstatus=ok\n", inspection.SchemaVersion, inspection.EmbeddedSchemaVersion, inspection.MigrationDigest); err != nil {
		return err
	}
	return nil
}

func runHealthcheck() error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("unexpected default HTTP transport")
	}
	transport = transport.Clone()
	// The endpoints are intentionally fixed to loopback. Do not allow proxy
	// environment variables to turn a local liveness/readiness check into an
	// outbound request.
	transport.Proxy = nil
	client := &http.Client{
		Transport: transport,
		Timeout:   healthcheckTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	if err := checkHealth(ctx, client, healthcheckURL); err != nil {
		return err
	}
	return checkHealth(ctx, client, readinesscheckURL)
}

func checkHealth(ctx context.Context, client *http.Client, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, healthcheckMaxBody+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(body) > healthcheckMaxBody {
		return fmt.Errorf("response exceeds %d bytes", healthcheckMaxBody)
	}
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("invalid health JSON: %w", err)
	}
	if payload.Status != "ok" {
		return fmt.Errorf("health status is %q", payload.Status)
	}
	return nil
}
