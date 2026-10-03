package publicendpoint

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RunnerStatus describes the supervised cloudflared process.
type RunnerStatus struct {
	State     string `json:"state"` // stopped, starting, connected, restarting, error
	Message   string `json:"message,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	Restarts  int    `json:"restarts"`
}

// runner supervises one cloudflared connector. The tunnel token is passed
// only through TUNNEL_TOKEN so it never appears in the process list.
type runner struct {
	binary string
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	status RunnerStatus
}

func newRunner(binary string) *runner {
	return &runner{binary: binary, status: RunnerStatus{State: "stopped"}}
}

func (r *runner) Status() RunnerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

func (r *runner) set(update func(*RunnerStatus)) {
	r.mu.Lock()
	update(&r.status)
	r.mu.Unlock()
}

func (r *runner) Start(token string) {
	r.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.mu.Lock()
	r.cancel, r.done = cancel, done
	r.status = RunnerStatus{State: "starting", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	r.mu.Unlock()
	go r.loop(ctx, token, done)
}

func (r *runner) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.cancel, r.done = nil, nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	r.set(func(status *RunnerStatus) { *status = RunnerStatus{State: "stopped"} })
}

func (r *runner) loop(ctx context.Context, token string, done chan struct{}) {
	defer close(done)
	backoff := 2 * time.Second
	for {
		started := time.Now()
		err := r.runOnce(ctx, token)
		if ctx.Err() != nil {
			return
		}
		message := "cloudflared exited"
		if err != nil {
			message = err.Error()
		}
		if time.Since(started) > time.Minute {
			backoff = 2 * time.Second
		}
		r.set(func(status *RunnerStatus) {
			status.State, status.Message = "restarting", message
			status.Restarts++
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (r *runner) runOnce(ctx context.Context, token string) error {
	command := exec.Command(r.binary, "tunnel", "--no-autoupdate", "run")
	command.Env = append(minimalEnv(), "TUNNEL_TOKEN="+token)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	command.Stdout = nil
	if err := command.Start(); err != nil {
		r.set(func(status *RunnerStatus) {
			status.State, status.Message = "error", "cloudflared could not start: "+err.Error()
		})
		return err
	}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.Contains(line, "Registered tunnel connection"):
				r.set(func(status *RunnerStatus) { status.State, status.Message = "connected", "" })
			case strings.Contains(line, " ERR "):
				r.set(func(status *RunnerStatus) { status.Message = sanitizeLine(line, token) })
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	select {
	case <-ctx.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		return ctx.Err()
	case err := <-exited:
		return err
	}
}

// minimalEnv passes only what cloudflared needs, never Helm's own secrets.
func minimalEnv() []string {
	env := []string{}
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func sanitizeLine(line, token string) string {
	if token != "" {
		line = strings.ReplaceAll(line, token, "[redacted]")
	}
	if len(line) > 300 {
		line = line[:300]
	}
	return line
}
