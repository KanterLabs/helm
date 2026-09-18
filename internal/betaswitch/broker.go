package betaswitch

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	maxReleaseSHABytes     = 128
	maxReleaseRefBytes     = 256
	maxReleaseSubjectBytes = MaxCommitSubjectBytes + 1 // subject plus its required newline
	maxManifestBytes       = 128 << 10
	maxChecksumBytes       = 256
	maxBinaryBytes         = 256 << 20
	maxJobBytes            = 4096
	defaultQueueDepth      = 128
	maxJobIDAttempts       = 64
)

// ExecFunc is deliberately passed the fixed configured executable and the
// validated release SHA as separate arguments. The production implementation
// never invokes a shell.
type ExecFunc func(context.Context, string, string) error

// PeerCheck can be injected by tests. Production listeners use Linux
// SO_PEERCRED and compare the peer UID to the roadmap service UID.
type PeerCheck func(net.Conn) (bool, error)

// OwnerCheck allows tests to model root-owned fixtures without requiring the
// test process to be privileged. Production code uses a strict root UID/GID
// check for retained release directories.
type OwnerCheck func(string, os.FileInfo) bool

// SleepFunc is the short startup grace used after a job is durably marked
// running. Tests inject a zero-delay function; production uses a context-aware
// timer so the 202 response has time to leave the HTTP process before the
// rollback helper stops helm.service.
type SleepFunc func(context.Context, time.Duration) error

// Config controls the broker's fixed installation paths and test seams. All
// request-controlled values are validated identifiers and are never used as
// paths or commands.
type Config struct {
	SocketPath  string
	SocketGroup string

	ReleasesDir  string
	CurrentPath  string
	StateDir     string
	RollbackPath string
	Branch       string

	MaxBodyBytes     int64
	MaxResponseBytes int64
	QueueDepth       int
	StartupGrace     time.Duration

	// RoadmapUID, when set, is the only UID accepted by the socket. When nil,
	// the daemon resolves the fixed roadmap account at startup.
	RoadmapUID  *uint32
	PeerCheck   PeerCheck
	OwnerCheck  OwnerCheck
	Chown       func(string, int, int) error
	LookupUser  func(string) (*user.User, error)
	LookupGroup func(string) (*user.Group, error)
	Exec        ExecFunc
	NewJobID    func() (string, error)
	Sleep       SleepFunc
	Now         func() time.Time
}

func (c Config) withDefaults() (Config, error) {
	if c.SocketPath == "" {
		c.SocketPath = DefaultSocketPath
	}
	if c.SocketGroup == "" {
		c.SocketGroup = DefaultSocketGroup
	}
	if c.ReleasesDir == "" {
		c.ReleasesDir = DefaultReleasesDir
	}
	if c.CurrentPath == "" {
		c.CurrentPath = DefaultCurrentPath
	}
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	if c.RollbackPath == "" {
		c.RollbackPath = DefaultRollbackPath
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if c.QueueDepth <= 0 {
		c.QueueDepth = defaultQueueDepth
	}
	if c.StartupGrace < 0 {
		return c, fmt.Errorf("startup grace must not be negative")
	}
	for name, value := range map[string]string{
		"socket": c.SocketPath, "releases": c.ReleasesDir, "current": c.CurrentPath,
		"state": c.StateDir, "rollback": c.RollbackPath,
	} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return c, fmt.Errorf("%s path must be absolute and contain no controls", name)
		}
	}
	if c.Branch != "" {
		if err := validateBranchLabel(c.Branch); err != nil {
			return c, err
		}
	}
	if c.Chown == nil {
		c.Chown = os.Chown
	}
	if c.OwnerCheck == nil {
		c.OwnerCheck = rootOwner
	}
	if c.LookupUser == nil {
		c.LookupUser = user.Lookup
	}
	if c.LookupGroup == nil {
		c.LookupGroup = user.LookupGroup
	}
	if c.Exec == nil {
		c.Exec = runFixedCommand
	}
	if c.NewJobID == nil {
		c.NewJobID = randomJobID
	}
	if c.Sleep == nil {
		if c.StartupGrace == 0 {
			c.StartupGrace = DefaultStartupGrace
		}
		c.Sleep = sleepWithContext
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c, nil
}

// Broker is an HTTP handler and asynchronous single-worker job broker.
type Broker struct {
	cfg Config

	queue   chan string
	worker  context.Context
	stop    context.CancelFunc
	done    chan struct{}
	close   sync.Once
	admitMu sync.Mutex

	// listMu serializes release listings so a startup warm-up and concurrent
	// requests never hash the same retained binaries in parallel.
	listMu      sync.Mutex
	binaryMu    sync.Mutex
	binaryCache map[string]binaryStamp
}

// NewBroker validates fixed paths, creates the durable state directory, and
// starts the serialized job worker. It does not bind a socket until Serve or
// Listen is called.
func NewBroker(cfg Config) (*Broker, error) {
	normalized, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if err := ensureStateDir(normalized.StateDir, normalized.OwnerCheck); err != nil {
		return nil, coded(ErrStateFailure, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{
		cfg:    normalized,
		queue:  make(chan string, normalized.QueueDepth),
		worker: ctx,
		stop:   cancel,
		done:   make(chan struct{}),
	}
	b.recoverJobs()
	go b.runWorker()
	return b, nil
}

// New is a concise constructor alias retained for small embedders.
func New(cfg Config) (*Broker, error) { return NewBroker(cfg) }

// Handler exposes the broker's fixed HTTP handler without requiring callers
// to know that Broker itself implements http.Handler.
func (b *Broker) Handler() http.Handler { return b }

// Config returns a copy of the normalized broker configuration. It is useful
// to the daemon for diagnostics without exposing mutable broker internals.
func (b *Broker) Config() Config { return b.cfg }

// Close stops the worker and closes no listener owned by a caller of Listen.
func (b *Broker) Close() error {
	if b == nil {
		return nil
	}
	b.close.Do(func() {
		b.stop()
		<-b.done
	})
	return nil
}

func (b *Broker) runWorker() {
	defer close(b.done)
	for {
		select {
		case <-b.worker.Done():
			return
		case id := <-b.queue:
			if id == "" {
				continue
			}
			b.process(id)
		}
	}
}

func (b *Broker) process(id string) {
	job, err := b.readJob(id)
	if err != nil {
		return
	}
	job.State = JobRunning
	job.Error = ""
	if err := b.writeJob(job); err != nil {
		// There is no safe way to run a deployment whose durable state cannot be
		// updated. Leave the prior queued record intact and do not execute.
		return
	}

	if err := b.cfg.Sleep(b.worker, b.cfg.StartupGrace); err != nil {
		job.State = JobFailed
		job.Error = string(ErrUnavailable)
		_ = b.writeJob(job)
		return
	}
	if _, err := b.findRelease(job.TargetSHA); err != nil {
		job.State = JobFailed
		job.Error = string(ErrReleaseInvalid)
		_ = b.writeJob(job)
		return
	}
	if current, err := b.currentSHA(); err != nil {
		job.State = JobFailed
		job.Error = string(ErrReleaseInvalid)
		_ = b.writeJob(job)
		return
	} else if current == job.TargetSHA {
		// The requested release became current while this job was queued or in
		// the startup grace period. Treat it as a durable no-op rather than
		// invoking rollback against the already-active release.
		job.State = JobSucceeded
		job.Error = ""
		_ = b.writeJob(job)
		return
	}
	if err := b.cfg.Exec(b.worker, b.cfg.RollbackPath, job.TargetSHA); err != nil {
		job.State = JobFailed
		job.Error = string(ErrRollbackFailure)
		_ = b.writeJob(job)
		return
	}
	job.State = JobSucceeded
	job.Error = ""
	_ = b.writeJob(job)
}

// ListReleases returns only complete, immutable release records.
func (b *Broker) ListReleases(_ context.Context) (ReleasesResponse, error) {
	b.listMu.Lock()
	defer b.listMu.Unlock()
	entries, err := os.ReadDir(b.cfg.ReleasesDir)
	if err != nil {
		return ReleasesResponse{}, coded(ErrReleaseInvalid, err)
	}
	current, err := b.currentSHA()
	if err != nil {
		return ReleasesResponse{}, coded(ErrReleaseInvalid, err)
	}
	releases := make([]Release, 0, len(entries))
	for _, entry := range entries {
		sha := entry.Name()
		if !validSHA(sha) {
			continue
		}
		release, err := b.findReleaseWith(sha, b.validateBinaryCached)
		if err != nil {
			continue
		}
		release.Current = release.SHA == current
		releases = append(releases, release)
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].SHA < releases[j].SHA })
	return ReleasesResponse{CurrentSHA: current, Releases: releases}, nil
}

// Releases is a concise alias for ListReleases for callers that prefer the
// noun used by the HTTP route.
func (b *Broker) Releases(ctx context.Context) (ReleasesResponse, error) {
	return b.ListReleases(ctx)
}

// RequestSwitch validates a retained release, durably records a queued job,
// and returns immediately. Execution occurs on the broker's single worker.
func (b *Broker) RequestSwitch(_ context.Context, sha string) (Job, error) {
	if err := validateSHA(sha); err != nil {
		return Job{}, coded(ErrInvalidRequest, err)
	}
	b.admitMu.Lock()
	defer b.admitMu.Unlock()
	if _, err := b.findRelease(sha); err != nil {
		return Job{}, coded(ErrNotFound, err)
	}
	if current, err := b.currentSHA(); err == nil && current == sha {
		if existing, ok := b.findDurableJob(sha, JobSucceeded); ok {
			return existing, nil
		}
		job, err := b.newDurableJob(JobSucceeded, sha)
		if err != nil {
			return Job{}, coded(ErrStateFailure, err)
		}
		return job, nil
	} else if err != nil {
		return Job{}, coded(ErrReleaseInvalid, err)
	}
	if existing, ok := b.findDurableJob(sha, JobQueued, JobRunning); ok {
		return existing, nil
	}
	job, err := b.newDurableJob(JobQueued, sha)
	if err != nil {
		return Job{}, coded(ErrStateFailure, err)
	}
	select {
	case b.queue <- job.ID:
		return job, nil
	default:
		job.State = JobFailed
		job.Error = string(ErrUnavailable)
		_ = b.writeJob(job)
		return Job{}, coded(ErrUnavailable, errors.New("job queue full"))
	}
}

// findDurableJob scans only validated, root-owned regular job files. A
// malformed or unsafe entry is ignored so one damaged record cannot prevent a
// new switch from being admitted. Running jobs win over queued jobs when both
// exist for a target, which avoids returning an older queued duplicate.
func (b *Broker) findDurableJob(sha string, states ...string) (Job, bool) {
	if !validSHA(sha) || len(states) == 0 {
		return Job{}, false
	}
	allowed := make(map[string]struct{}, len(states))
	for _, state := range states {
		if validJobState(state) {
			allowed[state] = struct{}{}
		}
	}
	if len(allowed) == 0 || ensureStateDir(b.cfg.StateDir, b.cfg.OwnerCheck) != nil {
		return Job{}, false
	}
	entries, err := os.ReadDir(b.cfg.StateDir)
	if err != nil {
		return Job{}, false
	}
	var queued Job
	haveQueued := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validJobID(id) {
			continue
		}
		path := filepath.Join(b.cfg.StateDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !b.cfg.OwnerCheck(path, info) {
			continue
		}
		job, err := b.readJob(id)
		if err != nil || job.TargetSHA != sha {
			continue
		}
		if _, ok := allowed[job.State]; !ok {
			continue
		}
		if job.State == JobRunning {
			return job, true
		}
		if !haveQueued {
			queued = job
			haveQueued = true
		}
	}
	if haveQueued {
		return queued, true
	}
	return Job{}, false
}

// newDurableJob allocates and atomically creates a job file without replacing
// an existing record. The exclusive creation boundary matters when a daemon
// restart or a concurrent broker sees the same durable state directory.
func (b *Broker) newDurableJob(state, sha string) (Job, error) {
	if !validJobState(state) || !validSHA(sha) {
		return Job{}, fmt.Errorf("invalid durable job")
	}
	if err := ensureStateDir(b.cfg.StateDir, b.cfg.OwnerCheck); err != nil {
		return Job{}, err
	}
	for attempt := 0; attempt < maxJobIDAttempts; attempt++ {
		id, err := b.cfg.NewJobID()
		if err != nil {
			return Job{}, err
		}
		if !validJobID(id) {
			continue
		}
		path := filepath.Join(b.cfg.StateDir, id+".json")
		if info, err := os.Lstat(path); err == nil {
			// Revalidate an existing entry before declining this ID. In
			// particular, never treat a symlink or unowned file as a record
			// that can be overwritten.
			if info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && b.cfg.OwnerCheck(path, info) {
				_, _ = readStateRegular(b.cfg.StateDir, id+".json", maxJobBytes, b.cfg.OwnerCheck)
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			continue
		}
		job := Job{ID: id, TargetSHA: sha, State: state}
		if err := b.writeNewJob(job); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return Job{}, err
		}
		return job, nil
	}
	return Job{}, fmt.Errorf("unable to allocate durable job ID")
}

// recoverJobs repairs jobs left by a process restart. A running record cannot
// be known to have completed, so it is made retryable; queued records are
// re-admitted to the serialized worker. Malformed, symlinked, and unowned
// files are ignored without being modified.
func (b *Broker) recoverJobs() {
	entries, err := os.ReadDir(b.cfg.StateDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validJobID(id) {
			continue
		}
		path := filepath.Join(b.cfg.StateDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !b.cfg.OwnerCheck(path, info) {
			continue
		}
		job, err := b.readJob(id)
		if err != nil {
			continue
		}
		switch job.State {
		case JobRunning:
			job.State = JobFailed
			job.Error = string(ErrUnavailable)
			_ = b.writeJob(job)
		case JobQueued:
			select {
			case b.queue <- job.ID:
			default:
				// Leave it queued for a later process restart if recovery
				// exceeds the bounded in-memory queue.
			}
		}
	}
}

// Switch is an alias for RequestSwitch.
func (b *Broker) Switch(ctx context.Context, sha string) (Job, error) {
	return b.RequestSwitch(ctx, sha)
}

// GetJob returns one durable job record.
func (b *Broker) GetJob(_ context.Context, id string) (Job, error) {
	if err := validateJobID(id); err != nil {
		return Job{}, coded(ErrInvalidRequest, err)
	}
	job, err := b.readJob(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Job{}, coded(ErrNotFound, err)
		}
		return Job{}, coded(ErrStateFailure, err)
	}
	return job, nil
}

// Job is an alias for GetJob.
func (b *Broker) Job(ctx context.Context, id string) (Job, error) {
	return b.GetJob(ctx, id)
}

func (b *Broker) currentSHA() (string, error) {
	info, err := os.Lstat(b.cfg.CurrentPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 || !b.cfg.OwnerCheck(b.cfg.CurrentPath, info) {
		return "", fmt.Errorf("current pointer is not a symlink")
	}
	target, err := os.Readlink(b.cfg.CurrentPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(b.cfg.CurrentPath), target)
	}
	target = filepath.Clean(target)
	base := filepath.Base(target)
	if !validSHA(base) || target != filepath.Join(b.cfg.ReleasesDir, base) {
		return "", fmt.Errorf("current pointer escapes releases directory")
	}
	if _, err := b.findRelease(base); err != nil {
		return "", err
	}
	return base, nil
}

// findRelease fully re-verifies a release, including hashing every binary.
// It is used before a switch is admitted.
func (b *Broker) findRelease(sha string) (Release, error) {
	return b.findReleaseWith(sha, validateBinary)
}

func (b *Broker) findReleaseWith(sha string, verifyBinary func(dir, name string) error) (Release, error) {
	if err := validateSHA(sha); err != nil {
		return Release{}, err
	}
	rootInfo, err := os.Lstat(b.cfg.ReleasesDir)
	if err != nil {
		return Release{}, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() || !b.cfg.OwnerCheck(b.cfg.ReleasesDir, rootInfo) {
		return Release{}, fmt.Errorf("release root is not root-owned")
	}
	path := filepath.Join(b.cfg.ReleasesDir, sha)
	info, err := os.Lstat(path)
	if err != nil {
		return Release{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !b.cfg.OwnerCheck(path, info) {
		return Release{}, fmt.Errorf("release directory is not root-owned")
	}

	shaBytes, err := readRegular(path, "release.sha", maxReleaseSHABytes)
	if err != nil {
		return Release{}, err
	}
	if got := oneLine(string(shaBytes)); got != sha {
		return Release{}, fmt.Errorf("release SHA mismatch")
	}
	refBytes, err := readRegular(path, "release.ref", maxReleaseRefBytes)
	if err != nil {
		return Release{}, err
	}
	ref, err := parseReleaseRef(refBytes)
	if err != nil {
		return Release{}, err
	}
	branch, err := branchFromRef(ref)
	if err != nil || (b.cfg.Branch != "" && branch != b.cfg.Branch) {
		return Release{}, fmt.Errorf("release branch is invalid")
	}
	if err := b.validateManifestRecord(path, "release.ref", refBytes); err != nil {
		return Release{}, err
	}
	subject, subjectBytes, err := readOptionalCommitSubject(path)
	if err != nil {
		return Release{}, err
	}
	if len(subjectBytes) > 0 {
		if err := b.validateManifestRecord(path, "release.subject", subjectBytes); err != nil {
			return Release{}, err
		}
	}
	signature, err := readRegular(path, "release.manifest.sig", 64)
	if err != nil {
		return Release{}, err
	}
	if len(signature) != 64 {
		return Release{}, fmt.Errorf("release manifest signature is invalid")
	}
	if err := b.validateReleaseEnv(path, sha); err != nil {
		return Release{}, err
	}
	if err := validateReleaseBinary(path, verifyBinary); err != nil {
		return Release{}, err
	}
	if err := verifyBinary(path, "codex"); err != nil {
		return Release{}, err
	}
	return Release{SHA: sha, Ref: ref, Subject: subject}, nil
}

func (b *Broker) validateReleaseEnv(dir, sha string) error {
	data, err := readRegular(dir, "roadmap.env", 64<<10)
	if err != nil {
		return err
	}
	var helm, roadmap []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.ContainsAny(key, " \t\r") {
			continue
		}
		switch key {
		case "HELM_RELEASE_SHA":
			helm = append(helm, value)
		case "ROADMAP_RELEASE_SHA":
			roadmap = append(roadmap, value)
		}
	}
	if len(helm) > 1 || len(roadmap) > 1 || (len(helm) == 0 && len(roadmap) == 0) {
		return fmt.Errorf("release environment revision is invalid")
	}
	if len(helm) == 1 && !validSHA(helm[0]) || len(roadmap) == 1 && !validSHA(roadmap[0]) {
		return fmt.Errorf("release environment revision is invalid")
	}
	if len(helm) == 1 && len(roadmap) == 1 && helm[0] != roadmap[0] {
		return fmt.Errorf("release environment revisions disagree")
	}
	got := roadmapValue(helm, roadmap)
	if got != sha {
		return fmt.Errorf("release environment revision mismatch")
	}
	return nil
}

func roadmapValue(helm, roadmap []string) string {
	if len(helm) == 1 {
		return helm[0]
	}
	if len(roadmap) == 1 {
		return roadmap[0]
	}
	return ""
}

func (b *Broker) validateManifestRecord(dir, name string, actual []byte) error {
	manifest, err := readRegular(dir, "release.manifest", maxManifestBytes)
	if err != nil {
		return err
	}
	lines := strings.Split(string(manifest), "\n")
	if len(lines) < 2 || lines[0] != "roadmap-release-manifest-v1" || lines[len(lines)-1] != "" {
		return fmt.Errorf("release manifest header is invalid")
	}
	expectedSize := strconv.FormatInt(int64(len(actual)), 10)
	expectedDigest := fmt.Sprintf("%x", sha256.Sum256(actual))
	seen := false
	seenNames := make(map[string]struct{})
	for _, line := range lines[1 : len(lines)-1] {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || !safeManifestName(parts[0]) || !canonicalDecimal(parts[1]) || len(parts[2]) != 64 || !isLowerHex(parts[2]) {
			return fmt.Errorf("release manifest record is malformed")
		}
		if _, duplicate := seenNames[parts[0]]; duplicate {
			return fmt.Errorf("release manifest contains duplicate records")
		}
		seenNames[parts[0]] = struct{}{}
		if parts[0] == name {
			if seen || parts[1] != expectedSize || parts[2] != expectedDigest {
				return fmt.Errorf("release manifest record mismatch")
			}
			seen = true
		}
	}
	if !seen {
		return fmt.Errorf("release manifest omits release reference")
	}
	return nil
}

func branchFromRef(ref string) (string, error) {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return "", fmt.Errorf("release ref must use refs/heads")
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if err := validateBranchLabel(branch); err != nil {
		return "", err
	}
	return branch, nil
}

func oneLine(value string) string {
	if strings.HasSuffix(value, "\n") {
		value = strings.TrimSuffix(value, "\n")
	}
	if strings.ContainsAny(value, "\r\n\t ") {
		return ""
	}
	return value
}

func parseReleaseRef(value []byte) (string, error) {
	if len(value) < 2 || value[len(value)-1] != '\n' || strings.Count(string(value), "\n") != 1 {
		return "", fmt.Errorf("release ref must contain exactly one newline")
	}
	ref := string(value[:len(value)-1])
	if strings.ContainsAny(ref, "\r\t") {
		return "", fmt.Errorf("release ref contains a control character")
	}
	return ref, nil
}

func readOptionalCommitSubject(dir string) (string, []byte, error) {
	path := filepath.Join(dir, "release.subject")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return "", nil, nil
	} else if err != nil {
		return "", nil, err
	}
	data, err := readRegular(dir, "release.subject", maxReleaseSubjectBytes)
	if err != nil {
		return "", nil, err
	}
	if len(data) < 2 || data[len(data)-1] != '\n' || strings.Count(string(data), "\n") != 1 {
		return "", nil, fmt.Errorf("release subject must contain exactly one newline")
	}
	subject := string(data[:len(data)-1])
	if !ValidCommitSubject(subject) {
		return "", nil, fmt.Errorf("release subject is invalid")
	}
	return subject, data, nil
}

func validateBinary(dir, name string) error {
	return validateBinaryWithLimit(dir, name, maxBinaryBytes)
}

func validateBinaryWithLimit(dir, name string, max int64) error {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("binary is not a regular file")
	}
	if info.Size() < 0 || info.Size() > max {
		return fmt.Errorf("release member exceeds size limit")
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("binary is not executable")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fmt.Errorf("binary changed while opening")
	}
	if opened.Size() < 0 || opened.Size() > max {
		return fmt.Errorf("release member exceeds size limit")
	}
	if opened.Mode()&0111 == 0 {
		return fmt.Errorf("binary is not executable")
	}
	hasher := sha256.New()
	read, err := io.Copy(hasher, io.LimitReader(f, max+1))
	if err != nil {
		return err
	}
	if read > max {
		return fmt.Errorf("release member exceeds size limit")
	}
	final, err := f.Stat()
	if err != nil {
		return err
	}
	if !final.Mode().IsRegular() || final.Mode()&0111 == 0 || final.Size() < 0 || final.Size() > max || final.Size() != read || final.Size() != opened.Size() {
		return fmt.Errorf("binary changed while reading")
	}
	latest, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if latest.Mode()&os.ModeSymlink != 0 || !latest.Mode().IsRegular() || latest.Mode()&0111 == 0 || !os.SameFile(info, latest) {
		return fmt.Errorf("binary changed while reading")
	}
	checksum, err := readRegular(dir, name+".sha256", maxChecksumBytes)
	if err != nil {
		return err
	}
	line := strings.TrimSuffix(string(checksum), "\n")
	fields := strings.Fields(line)
	if len(fields) != 2 || len(fields[0]) != 64 || !isLowerHex(fields[0]) || fields[1] != name {
		return fmt.Errorf("binary checksum record is invalid")
	}
	digest := hasher.Sum(nil)
	if hex.EncodeToString(digest) != fields[0] {
		return fmt.Errorf("binary checksum mismatch")
	}
	return nil
}

func validateReleaseBinary(dir string, verifyBinary func(dir, name string) error) error {
	if _, err := os.Lstat(filepath.Join(dir, "helm")); err == nil {
		return verifyBinary(dir, "helm")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return verifyBinary(dir, "roadmap")
}

func readRegular(dir, name string, max int64) ([]byte, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("release member is not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info.Size() < 0 || info.Size() > max {
		return nil, fmt.Errorf("release member exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("release member exceeds size limit")
	}
	return data, nil
}

func allDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func canonicalDecimal(value string) bool {
	return allDecimal(value) && (value == "0" || value[0] != '0')
}

func safeManifestName(value string) bool {
	if value == "" || strings.ContainsAny(value, "/\\\r\n\x00\t ") {
		return false
	}
	for _, r := range value {
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '.' && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func rootOwner(_ string, info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && stat.Gid == 0
}

func ensureStateDir(path string, owner OwnerCheck) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("state directory is not a directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || !owner(path, info) {
		return fmt.Errorf("state directory is not a directory")
	}
	if err := os.Chmod(path, 0700); err != nil {
		return err
	}
	return nil
}

func readStateRegular(dir, name string, max int64, owner OwnerCheck) ([]byte, error) {
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !owner(path, info) {
		return nil, fmt.Errorf("job file is not a root-owned regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	openedInfo, err := f.Stat()
	if err != nil || openedInfo.Mode()&os.ModeSymlink != 0 || !openedInfo.Mode().IsRegular() || !owner(path, openedInfo) {
		return nil, fmt.Errorf("job file is not a root-owned regular file")
	}
	if openedInfo.Size() < 0 || openedInfo.Size() > max {
		return nil, fmt.Errorf("job file exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("job file exceeds size limit")
	}
	return data, nil
}

func (b *Broker) readJob(id string) (Job, error) {
	if err := ensureStateDir(b.cfg.StateDir, b.cfg.OwnerCheck); err != nil {
		return Job{}, err
	}
	data, err := readStateRegular(b.cfg.StateDir, id+".json", maxJobBytes, b.cfg.OwnerCheck)
	if err != nil {
		return Job{}, err
	}
	var job Job
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&job); err != nil {
		return Job{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Job{}, fmt.Errorf("trailing job data")
	}
	if job.ID != id || !validSHA(job.TargetSHA) || !validJobID(job.ID) || !validJobState(job.State) {
		return Job{}, fmt.Errorf("invalid job record")
	}
	if job.Error != "" && !validErrorClass(job.Error) {
		return Job{}, fmt.Errorf("invalid job error class")
	}
	return job, nil
}

func validJobState(state string) bool {
	switch state {
	case JobQueued, JobRunning, JobSucceeded, JobFailed:
		return true
	default:
		return false
	}
}

func validErrorClass(value string) bool {
	switch ErrorCode(value) {
	case ErrInvalidRequest, ErrUnauthorized, ErrNotFound, ErrConflict, ErrReleaseInvalid,
		ErrStateFailure, ErrRollbackFailure, ErrUnavailable, ErrInternal:
		return true
	default:
		return false
	}
}

func (b *Broker) writeJob(job Job) error {
	return b.writeJobAtomic(job, false)
}

func (b *Broker) writeNewJob(job Job) error {
	return b.writeJobAtomic(job, true)
}

func (b *Broker) writeJobAtomic(job Job, exclusive bool) error {
	if err := ensureStateDir(b.cfg.StateDir, b.cfg.OwnerCheck); err != nil {
		return err
	}
	if !validJobID(job.ID) || !validSHA(job.TargetSHA) || !validJobState(job.State) {
		return fmt.Errorf("invalid job")
	}
	if job.Error != "" && !validErrorClass(job.Error) {
		return fmt.Errorf("invalid job error class")
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if len(data) > maxJobBytes {
		return fmt.Errorf("job exceeds size limit")
	}
	tmp, err := os.CreateTemp(b.cfg.StateDir, ".job-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	tmpInfo, err := tmp.Stat()
	if err != nil || !b.cfg.OwnerCheck(tmpName, tmpInfo) {
		_ = tmp.Close()
		return fmt.Errorf("temporary job file is not root-owned")
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	dst := filepath.Join(b.cfg.StateDir, job.ID+".json")
	if exclusive {
		// Linking the fully-written temporary inode creates the destination
		// atomically and fails with EEXIST for any existing entry, including a
		// symlink. The temporary name is removed by the defer below.
		if err := os.Link(tmpName, dst); err != nil {
			return err
		}
	} else {
		if existing, err := os.Lstat(dst); err == nil {
			if existing.Mode()&os.ModeSymlink != 0 || !existing.Mode().IsRegular() || !b.cfg.OwnerCheck(dst, existing) {
				return fmt.Errorf("existing job file is not a root-owned regular file")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(tmpName, dst); err != nil {
			return err
		}
	}
	finalInfo, err := os.Lstat(dst)
	if err != nil || finalInfo.Mode()&os.ModeSymlink != 0 || !finalInfo.Mode().IsRegular() || !b.cfg.OwnerCheck(dst, finalInfo) {
		return fmt.Errorf("job file is not a root-owned regular file")
	}
	dir, err := os.Open(b.cfg.StateDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomJobID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func runFixedCommand(ctx context.Context, path, sha string) error {
	cmd := exec.CommandContext(ctx, path, sha)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// Listen binds and secures the configured Unix socket. The returned listener
// rejects unauthorized peers before net/http sees a connection.
func (b *Broker) Listen() (net.Listener, error) {
	if err := prepareSocketPath(b.cfg.SocketPath); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", b.cfg.SocketPath)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(b.cfg.SocketPath, 0660); err != nil {
		listener.Close()
		_ = os.Remove(b.cfg.SocketPath)
		return nil, err
	}
	gid, err := groupID(b.cfg.SocketGroup, b.cfg.LookupGroup)
	if err != nil {
		listener.Close()
		_ = os.Remove(b.cfg.SocketPath)
		return nil, err
	}
	if err := b.cfg.Chown(b.cfg.SocketPath, 0, gid); err != nil {
		listener.Close()
		_ = os.Remove(b.cfg.SocketPath)
		return nil, err
	}
	peerCheck := b.cfg.PeerCheck
	if peerCheck == nil {
		uid, err := b.allowedUID()
		if err != nil {
			listener.Close()
			_ = os.Remove(b.cfg.SocketPath)
			return nil, err
		}
		peerCheck = func(conn net.Conn) (bool, error) { return peerIsUID(conn, uid) }
	}
	return &peerListener{Listener: listener, check: peerCheck}, nil
}

// Serve runs the HTTP broker until ctx is canceled or the listener fails.
func (b *Broker) Serve(ctx context.Context) error {
	listener, err := b.Listen()
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
		_ = removeSocketIfSocket(b.cfg.SocketPath)
	}()
	server := &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) || ctx.Err() != nil {
		return nil
	}
	return err
}

type peerListener struct {
	net.Listener
	check PeerCheck
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		allowed, checkErr := l.check(conn)
		if checkErr == nil && allowed {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func peerIsUID(conn net.Conn, allowed uint32) (bool, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false, fmt.Errorf("peer is not a Unix connection")
	}
	var cred *unix.Ucred
	var credErr error
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false, err
	}
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return false, err
	}
	if credErr != nil {
		return false, credErr
	}
	return cred != nil && cred.Uid == allowed, nil
}

func (b *Broker) allowedUID() (uint32, error) {
	if b.cfg.RoadmapUID != nil {
		return *b.cfg.RoadmapUID, nil
	}
	roadmap, err := b.cfg.LookupUser("roadmap")
	if err != nil {
		return 0, err
	}
	uid, err := strconv.ParseUint(roadmap.Uid, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(uid), nil
}

func groupID(name string, lookup func(string) (*user.Group, error)) (int, error) {
	if allDecimal(name) {
		gid, err := strconv.ParseUint(name, 10, 31)
		return int(gid), err
	}
	group, err := lookup(name)
	if err != nil {
		return 0, err
	}
	gid, err := strconv.ParseUint(group.Gid, 10, 31)
	return int(gid), err
}

func prepareSocketPath(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return fmt.Errorf("socket path must be absolute and contain no controls")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("existing socket path is not a socket")
	}
	return os.Remove(path)
}

func removeSocketIfSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return os.Remove(path)
}

// ServeHTTP implements the fixed broker API.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/v1/releases" {
		if r.Method != http.MethodGet {
			b.writeError(w, http.StatusMethodNotAllowed, ErrInvalidRequest)
			return
		}
		response, err := b.ListReleases(r.Context())
		if err != nil {
			b.writeError(w, statusFor(err), codeOf(err))
			return
		}
		b.writeJSON(w, http.StatusOK, response)
		return
	}
	if r.URL.Path == "/v1/switch" {
		if r.Method != http.MethodPost {
			b.writeError(w, http.StatusMethodNotAllowed, ErrInvalidRequest)
			return
		}
		var request SwitchRequest
		if err := b.decodeJSON(w, r, &request); err != nil {
			b.writeError(w, http.StatusBadRequest, ErrInvalidRequest)
			return
		}
		job, err := b.RequestSwitch(r.Context(), request.SHA)
		if err != nil {
			b.writeError(w, statusFor(err), codeOf(err))
			return
		}
		b.writeJSON(w, http.StatusAccepted, job)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/jobs/") {
		if r.Method != http.MethodGet {
			b.writeError(w, http.StatusMethodNotAllowed, ErrInvalidRequest)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
		if strings.Contains(id, "/") || !validJobID(id) {
			b.writeError(w, http.StatusBadRequest, ErrInvalidRequest)
			return
		}
		job, err := b.GetJob(r.Context(), id)
		if err != nil {
			b.writeError(w, statusFor(err), codeOf(err))
			return
		}
		b.writeJSON(w, http.StatusOK, job)
		return
	}
	b.writeError(w, http.StatusNotFound, ErrNotFound)
}

func (b *Broker) decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	if r.ContentLength > b.cfg.MaxBodyBytes {
		return fmt.Errorf("request body exceeds limit")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, b.cfg.MaxBodyBytes+1))
	defer r.Body.Close()
	if err != nil {
		return err
	}
	if int64(len(data)) > b.cfg.MaxBodyBytes {
		return fmt.Errorf("request body exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("request must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("request object key is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("request contains duplicate field")
		}
		seen[key] = struct{}{}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
	}
	if token, err = decoder.Token(); err != nil {
		return err
	} else if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("request object is malformed")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("request contains trailing data")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func (b *Broker) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (b *Broker) writeError(w http.ResponseWriter, status int, code ErrorCode) {
	b.writeJSON(w, status, map[string]string{"error": string(code)})
}

func statusFor(err error) int {
	switch codeOf(err) {
	case ErrInvalidRequest:
		return http.StatusBadRequest
	case ErrUnauthorized:
		return http.StatusForbidden
	case ErrNotFound:
		return http.StatusNotFound
	case ErrConflict:
		return http.StatusConflict
	case ErrReleaseInvalid:
		return http.StatusServiceUnavailable
	case ErrUnavailable:
		return http.StatusServiceUnavailable
	case ErrStateFailure, ErrRollbackFailure, ErrInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
