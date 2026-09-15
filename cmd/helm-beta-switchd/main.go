// Command helm-beta-switchd is the root-owned Unix-socket beta release
// broker. It has no public TCP listener and accepts only the roadmap service
// UID over its fixed Unix socket.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/KanterLabs/helm/internal/betaswitch"
)

func main() {
	if os.Geteuid() != 0 {
		fatal("must run as root")
	}
	socketPath := flag.String("socket", betaswitch.DefaultSocketPath, "absolute Unix socket path")
	socketGroup := flag.String("socket-group", betaswitch.DefaultSocketGroup, "Unix socket group")
	releasesDir := flag.String("releases-dir", betaswitch.DefaultReleasesDir, "fixed retained release directory")
	currentPath := flag.String("current", betaswitch.DefaultCurrentPath, "fixed active release pointer")
	flag.StringVar(currentPath, "current-link", betaswitch.DefaultCurrentPath, "alias for -current")
	stateDir := flag.String("state-dir", betaswitch.DefaultStateDir, "fixed durable switch state directory")
	flag.StringVar(stateDir, "jobs-dir", betaswitch.DefaultStateDir, "alias for -state-dir")
	rollbackPath := flag.String("rollback", betaswitch.DefaultRollbackPath, "fixed rollback executable")
	branch := flag.String("branch", "", "optional exact branch label; empty accepts any validated refs/heads label")
	maxBody := flag.Int64("max-body-bytes", betaswitch.DefaultMaxBodyBytes, "maximum JSON request body size")
	allowedUser := flag.String("allowed-user", "roadmap", "fixed peer account (must remain roadmap)")
	flag.Parse()
	if *allowedUser != "roadmap" {
		fatal("allowed-user must be roadmap")
	}
	if *rollbackPath != betaswitch.DefaultRollbackPath {
		fatal("rollback path must remain %s", betaswitch.DefaultRollbackPath)
	}

	broker, err := betaswitch.NewBroker(betaswitch.Config{
		SocketPath:   *socketPath,
		SocketGroup:  *socketGroup,
		ReleasesDir:  *releasesDir,
		CurrentPath:  *currentPath,
		StateDir:     *stateDir,
		RollbackPath: *rollbackPath,
		Branch:       *branch,
		MaxBodyBytes: *maxBody,
	})
	if err != nil {
		fatal("configure broker: %v", err)
	}
	defer broker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := broker.Serve(ctx); err != nil {
		fatal("serve broker: %v", err)
	}
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "helm-beta-switchd: "+format+"\n", args...)
	os.Exit(1)
}
