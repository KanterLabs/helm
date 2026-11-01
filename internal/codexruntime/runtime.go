// Package codexruntime manages isolated Codex App Server processes for Helm
// actors and exposes a small, testable JSONL protocol client.
package codexruntime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultMaxMessageBytes = 2 << 20
	defaultEventBuffer     = 256
)

var (
	ErrClosed                 = errors.New("codex runtime is closed")
	ErrActorMismatch          = errors.New("codex runtime actor mismatch")
	ErrEventOverflow          = errors.New("codex runtime event buffer overflow")
	ErrInvalidAccountResponse = errors.New("invalid Codex account response")
)

type Options struct {
	Binary          string
	HomeRoot        string
	WorkingDir      string
	MaxMessageBytes int
	EventBuffer     int
	ShutdownGrace   time.Duration
	evidenceOnly    bool
}

type Manager struct {
	opts     Options
	mu       sync.Mutex
	sessions map[string]*Session
	evidence map[*Session]struct{}
	accounts map[string]evidenceAccountState
	closed   bool
}

func NewManager(opts Options) *Manager {
	if strings.TrimSpace(opts.Binary) == "" {
		opts.Binary = "codex"
	}
	if opts.MaxMessageBytes <= 0 {
		opts.MaxMessageBytes = defaultMaxMessageBytes
	}
	if opts.EventBuffer <= 0 {
		opts.EventBuffer = defaultEventBuffer
	}
	if opts.ShutdownGrace <= 0 {
		opts.ShutdownGrace = 2 * time.Second
	}
	return &Manager{opts: opts, sessions: make(map[string]*Session), evidence: make(map[*Session]struct{}), accounts: make(map[string]evidenceAccountState)}
}

func (m *Manager) Session(ctx context.Context, actorID string) (*Session, error) {
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || len(actorID) > 512 || strings.ContainsAny(actorID, "\r\n\x00") {
		return nil, fmt.Errorf("invalid actor id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if current := m.sessions[actorID]; current != nil && current.Alive() {
		return current, nil
	}
	session, err := startSession(ctx, actorID, m.opts)
	if err != nil {
		return nil, err
	}
	m.sessions[actorID] = session
	return session, nil
}

func (m *Manager) CloseActor(ctx context.Context, actorID string) error {
	m.mu.Lock()
	session := m.sessions[actorID]
	delete(m.sessions, actorID)
	var evidence []*Session
	for current := range m.evidence {
		if current.actorID == actorID {
			evidence = append(evidence, current)
			delete(m.evidence, current)
		}
	}
	m.mu.Unlock()
	var joined error
	for _, current := range evidence {
		joined = errors.Join(joined, current.Close(ctx))
	}
	if session == nil {
		return joined
	}
	return errors.Join(joined, session.Close(ctx))
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	for session := range m.evidence {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*Session)
	m.evidence = make(map[*Session]struct{})
	m.mu.Unlock()
	var joined error
	for _, session := range sessions {
		joined = errors.Join(joined, session.Close(ctx))
	}
	return joined
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

type Notification struct {
	Method string
	Params json.RawMessage
}

type response struct {
	Result json.RawMessage
	Error  *RPCError
}

type wireMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type Session struct {
	actorID string
	home    string
	cmd     *exec.Cmd
	stdin   io.WriteCloser

	writeMu sync.Mutex
	turnMu  sync.Mutex
	mu      sync.Mutex
	pending map[int64]chan response
	termErr error

	nextID atomic.Int64
	events chan Notification
	done   chan struct{}
	once   sync.Once
	grace  time.Duration
	maxOut int
}

func actorHome(root, actorID string) string {
	digest := sha256.Sum256([]byte(actorID))
	return filepath.Join(root, hex.EncodeToString(digest[:]))
}

func startSession(ctx context.Context, actorID string, opts Options) (*Session, error) {
	if strings.TrimSpace(opts.HomeRoot) == "" {
		return nil, fmt.Errorf("Codex home root is required")
	}
	if err := os.MkdirAll(opts.HomeRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create Codex home root: %w", err)
	}
	if err := os.Chmod(opts.HomeRoot, 0o700); err != nil {
		return nil, fmt.Errorf("protect Codex home root: %w", err)
	}
	home := actorHome(opts.HomeRoot, actorID)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create isolated Codex home: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, fmt.Errorf("protect isolated Codex home: %w", err)
	}
	args := []string{"app-server", "--stdio"}
	if opts.evidenceOnly {
		// Disable capabilities before extension/service initialization as well as
		// at thread creation. In particular no code-mode host may be started.
		for key, value := range evidenceConfig() {
			encoded, _ := json.Marshal(value)
			args = append(args, "-c", key+"="+string(encoded))
		}
	}
	cmd := exec.Command(opts.Binary, args...)
	environment := withoutEnv(os.Environ(), "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN")
	cmd.Env = replaceEnv(environment, "CODEX_HOME", home)
	if opts.evidenceOnly {
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CODEX_HOME=" + home}
		// Provider transport needs the deployment's proxies and TLS trust paths.
		// Keep this explicit: server secrets and execution hooks must not enter
		// the evidence process, whose HOME remains scoped to the actor.
		for _, name := range []string{
			"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
			"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
			"SSL_CERT_FILE", "SSL_CERT_DIR",
		} {
			if value, present := os.LookupEnv(name); present {
				cmd.Env = append(cmd.Env, name+"="+value)
			}
		}
	}
	cmd.Dir = opts.WorkingDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open Codex stdout: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	s := &Session{
		actorID: actorID,
		home:    home,
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[int64]chan response),
		events:  make(chan Notification, opts.EventBuffer),
		done:    make(chan struct{}),
		grace:   opts.ShutdownGrace,
		maxOut:  opts.MaxMessageBytes,
	}
	go s.readLoop(stdout, opts.MaxMessageBytes)
	var initialized json.RawMessage
	initialize := map[string]any{
		"clientInfo": map[string]string{"name": "helm", "title": "Helm", "version": "1"},
	}
	if opts.evidenceOnly {
		initialize["capabilities"] = map[string]bool{"experimentalApi": true}
	}
	if err := s.Call(ctx, "initialize", initialize, &initialized); err != nil {
		_ = s.Close(context.Background())
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := s.notify("initialized", map[string]any{}); err != nil {
		_ = s.Close(context.Background())
		return nil, fmt.Errorf("acknowledge Codex app-server: %w", err)
	}
	return s, nil
}

func replaceEnv(values []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(values)+1)
	for _, item := range values {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}

func withoutEnv(values []string, names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		name, _, _ := strings.Cut(item, "=")
		if _, remove := blocked[name]; !remove {
			result = append(result, item)
		}
	}
	return result
}

func (s *Session) ActorID() string { return s.actorID }

func (s *Session) RequireActor(actorID string) error {
	if actorID != s.actorID {
		return ErrActorMismatch
	}
	return nil
}

func (s *Session) Alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *Session) Call(ctx context.Context, method string, params any, out any) error {
	if strings.TrimSpace(method) == "" {
		return fmt.Errorf("Codex method is required")
	}
	id := s.nextID.Add(1)
	reply := make(chan response, 1)
	s.mu.Lock()
	if s.termErr != nil {
		err := s.termErr
		s.mu.Unlock()
		return err
	}
	s.pending[id] = reply
	s.mu.Unlock()
	if err := s.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		s.removePending(id)
		return err
	}
	select {
	case value := <-reply:
		if value.Error != nil {
			return value.Error
		}
		if out == nil || len(value.Result) == 0 || string(value.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(value.Result, out); err != nil {
			return fmt.Errorf("decode Codex response: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.removePending(id)
		return ctx.Err()
	case <-s.done:
		return s.terminalError()
	}
}

func (s *Session) removePending(id int64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func (s *Session) notify(method string, params any) error {
	return s.write(map[string]any{"method": method, "params": params})
}

func (s *Session) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode Codex request: %w", err)
	}
	payload = append(payload, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if !s.Alive() {
		return s.terminalError()
	}
	if _, err := s.stdin.Write(payload); err != nil {
		return fmt.Errorf("write Codex request: %w", err)
	}
	return nil
}

func (s *Session) readLoop(reader io.Reader, maxMessageBytes int) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		var message wireMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			s.finish(fmt.Errorf("malformed Codex protocol message: %w", err))
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
			return
		}
		if message.ID != nil && message.Method == "" {
			s.mu.Lock()
			reply := s.pending[*message.ID]
			delete(s.pending, *message.ID)
			s.mu.Unlock()
			if reply != nil {
				reply <- response{Result: message.Result, Error: message.Error}
			}
			continue
		}
		if message.ID != nil && message.Method != "" {
			_ = s.write(map[string]any{"id": *message.ID, "error": map[string]any{"code": -32601, "message": "method not supported by Helm"}})
			continue
		}
		if message.Method != "" {
			select {
			case s.events <- Notification{Method: message.Method, Params: message.Params}:
			default:
				s.finish(ErrEventOverflow)
				_ = s.cmd.Process.Kill()
				_ = s.cmd.Wait()
				return
			}
		}
	}
	err := scanner.Err()
	if err != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if err == nil {
		err = io.EOF
	}
	if waitErr := s.cmd.Wait(); waitErr != nil && errors.Is(err, io.EOF) {
		err = waitErr
	}
	s.finish(fmt.Errorf("Codex app-server exited: %w", err))
}

func (s *Session) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.termErr = err
		pending := s.pending
		s.pending = make(map[int64]chan response)
		s.mu.Unlock()
		for _, reply := range pending {
			reply <- response{Error: &RPCError{Code: -32000, Message: "Codex app-server stopped"}}
		}
		close(s.done)
	})
}

func (s *Session) terminalError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.termErr != nil {
		return s.termErr
	}
	return ErrClosed
}

func (s *Session) Close(ctx context.Context) error {
	if !s.Alive() {
		return nil
	}
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}
	timer := time.NewTimer(s.grace)
	defer timer.Stop()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		return ctx.Err()
	case <-timer.C:
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		<-s.done
		return nil
	}
}
