package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/KanterLabs/helm/internal/db"
	"github.com/KanterLabs/helm/internal/store"
)

const auditAccessUsage = `Usage:
  helm audit-access grant --db PATH --project REF --owner HUMAN_ADMIN_ID --agent AGENT_ID --token TOKEN_ID [--expires-in 1h] [--max-tasks 10]
  helm audit-access grant --db PATH --project REF --owner HUMAN_ADMIN_ID --agent AGENT_ID --token-file /PRIVATE/DIR/NEW_FILE [--expires-in 1h] [--max-tasks 10]
  helm audit-access list --db PATH --project REF
  helm audit-access revoke --db PATH --authorization ID --owner HUMAN_ADMIN_ID

Administer delegated AI usage for GitHub audits using an existing local database.
Grant/revoke require an explicit enabled human administrator account owner.
The agent must already exist. Choose exactly one of --token or --token-file.
TOKEN_ID is metadata, never a bearer secret. --token-file creates a new project-bound
projects:read/tasks:read token with the same expiry as the grant, delivered only to
a new 0600 file. Its existing parent must be owned by this user and mode 0700.
Expiry must be greater than zero and at most 24h; task count must be 1 through 100.
The database must already match this binary's schema; this command does not migrate it.
This is local operator administration, not a server session or account setup command.
Successful operations emit only authorization metadata as JSON.
`

func runAuditAccess(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		_, err := io.WriteString(output, auditAccessUsage)
		return err
	}
	if len(args) == 0 {
		return errors.New("audit-access requires grant, list, or revoke")
	}
	operation := args[0]
	if operation != "grant" && operation != "list" && operation != "revoke" {
		return errors.New("unknown audit-access operation")
	}
	flags := flag.NewFlagSet("audit-access "+operation, flag.ContinueOnError)
	// The flag package otherwise echoes user input, which can contain secrets.
	flags.SetOutput(io.Discard)
	path := flags.String("db", "", "existing local database")
	var project, owner, agent, token, tokenFile, authorization string
	expires, maxTasks := time.Hour, 10
	if operation != "revoke" {
		flags.StringVar(&project, "project", "", "project reference")
	}
	if operation != "list" {
		flags.StringVar(&owner, "owner", "", "human administrator account owner")
	}
	if operation == "grant" {
		flags.StringVar(&agent, "agent", "", "existing agent ID")
		flags.StringVar(&token, "token", "", "existing token ID")
		flags.StringVar(&tokenFile, "token-file", "", "absolute path to a new private token file")
		flags.DurationVar(&expires, "expires-in", time.Hour, "authorization lifetime")
		flags.IntVar(&maxTasks, "max-tasks", 10, "maximum captured tasks")
	}
	if operation == "revoke" {
		flags.StringVar(&authorization, "authorization", "", "authorization ID")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = io.WriteString(output, auditAccessUsage)
			return err
		}
		return errors.New("invalid audit-access flags; use --help")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected audit-access arguments")
	}
	if strings.TrimSpace(*path) == "" || (operation != "revoke" && strings.TrimSpace(project) == "") ||
		(operation != "list" && strings.TrimSpace(owner) == "") ||
		(operation == "grant" && strings.TrimSpace(agent) == "") ||
		(operation == "revoke" && strings.TrimSpace(authorization) == "") {
		return errors.New("missing required audit-access identifier; use --help")
	}
	if operation == "grant" && ((strings.TrimSpace(token) == "") == (strings.TrimSpace(tokenFile) == "")) {
		return errors.New("grant requires exactly one of --token or --token-file")
	}
	if operation == "grant" && (expires <= 0 || expires > 24*time.Hour || maxTasks < 1 || maxTasks > 100) {
		return errors.New("audit-access expiry or task limit is outside the allowed bounds")
	}
	database, err := openAuditAccessDatabase(ctx, *path)
	if err != nil {
		return err
	}
	defer database.Close()
	data := store.New(database)
	var result any
	if operation == "revoke" {
		result, err = data.RevokeGithubAuditAuthorization(ctx, authorization, owner)
	} else {
		resolved, resolveErr := data.GetProject(ctx, project)
		if resolveErr != nil {
			return resolveErr
		}
		if operation == "list" {
			result, err = data.ListGithubAuditAuthorizations(ctx, resolved.ID)
		} else {
			if tokenFile != "" {
				return provisionAuditAccessToken(ctx, data, resolved.ID, owner, agent, tokenFile, expires, maxTasks, output)
			}
			result, err = data.CreateGithubAuditAuthorization(ctx, resolved.ID, owner, store.CreateGithubAuditAuthorizationInput{
				AgentID: agent, TokenID: token, ExpiresAt: time.Now().UTC().Add(expires).Format(time.RFC3339Nano), MaxTasks: maxTasks,
			})
		}
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

// Only this invocation's newly reserved file and newly minted credential may be
// cleaned up. In particular, failed provisioning never touches existing tokens.
func provisionAuditAccessToken(ctx context.Context, data *store.Store, project, owner, agent, path string, lifetime time.Duration, maxTasks int, output io.Writer) (err error) {
	file, err := reserveAuditTokenFile(path)
	if err != nil {
		return err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	var tokenID, grantID string
	committed := false
	defer func() {
		_ = file.Close()
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if grantID != "" {
			_, cleanupErr := data.RevokeGithubAuditAuthorization(cleanupCtx, grantID, owner)
			err = errors.Join(err, cleanupErr)
		}
		if tokenID != "" {
			err = errors.Join(err, data.DeleteTokenBy(cleanupCtx, tokenID, owner))
		}
		if current, statErr := os.Lstat(path); statErr == nil && os.SameFile(fileInfo, current) {
			err = errors.Join(err, os.Remove(path))
		}
	}()
	expiry := time.Now().UTC().Add(lifetime)
	token, secret, err := data.CreateTokenBy(ctx, agent, owner, "Delegated GitHub audit", []string{"projects:read", "tasks:read"}, []string{project}, &expiry)
	if err != nil {
		return err
	}
	tokenID = token.ID
	grant, err := data.CreateGithubAuditAuthorization(ctx, project, owner, store.CreateGithubAuditAuthorizationInput{
		AgentID: agent, TokenID: tokenID, ExpiresAt: expiry.Format(time.RFC3339Nano), MaxTasks: maxTasks,
	})
	if err != nil {
		return err
	}
	grantID = grant.ID
	if _, err = io.WriteString(file, secret+"\n"); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = json.NewEncoder(output).Encode(grant); err != nil {
		return err
	}
	committed = true
	return nil
}

func reserveAuditTokenFile(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("token file requires a clean absolute new path")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	stat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || !parent.IsDir() || parent.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("token file parent must be a private directory owned by the current user")
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

// Inspect the existing database read-only before invoking ordinary application
// setup. Reject URI arguments and nonregular paths so missing files, in-memory
// databases, and older/newer schemas cannot be silently created or upgraded.
func openAuditAccessDatabase(ctx context.Context, path string) (*sql.DB, error) {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return nil, errors.New("audit-access requires a regular database path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("audit-access requires an existing regular database")
	}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
	inspectionDB, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	inspection, inspectErr := db.InspectSchema(ctx, inspectionDB)
	closeErr := inspectionDB.Close()
	if inspectErr != nil {
		return nil, inspectErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if inspection.SchemaVersion != inspection.EmbeddedSchemaVersion || len(inspection.PendingVersions) != 0 || len(inspection.UnknownVersions) != 0 {
		return nil, fmt.Errorf("audit-access requires the current database schema; run the deployment migration workflow first")
	}
	uri.RawQuery = "mode=rw"
	return db.Open(ctx, uri.String())
}
