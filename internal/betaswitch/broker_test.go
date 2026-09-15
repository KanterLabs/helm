package betaswitch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const alternateSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type brokerFixture struct {
	root     string
	releases string
	state    string
	current  string
	broker   *Broker
}

func newFixture(t *testing.T, execFn ExecFunc) brokerFixture {
	t.Helper()
	root := t.TempDir()
	fixture := brokerFixture{
		root:     root,
		releases: filepath.Join(root, "releases"),
		state:    filepath.Join(root, "state"),
		current:  filepath.Join(root, "current"),
	}
	if err := os.MkdirAll(fixture.releases, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture.releases, testSHA), fixture.current); err != nil {
		t.Fatal(err)
	}
	ids := []string{"0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"}
	var next atomic.Int64
	fixture.broker, _ = NewBroker(Config{
		SocketPath:   filepath.Join(root, "switch.sock"),
		SocketGroup:  "0",
		ReleasesDir:  fixture.releases,
		CurrentPath:  fixture.current,
		StateDir:     fixture.state,
		RollbackPath: filepath.Join(root, "rollback-fixed"),
		Branch:       "",
		StartupGrace: 0,
		Sleep:        func(context.Context, time.Duration) error { return nil },
		OwnerCheck:   func(string, os.FileInfo) bool { return true },
		Chown:        func(string, int, int) error { return nil },
		PeerCheck:    func(net.Conn) (bool, error) { return true, nil },
		NewJobID: func() (string, error) {
			i := next.Add(1) - 1
			if i >= int64(len(ids)) {
				return randomJobID()
			}
			return ids[i], nil
		},
		Exec: execFn,
	})
	if fixture.broker == nil {
		t.Fatal("new broker returned nil")
	}
	writeRelease(t, fixture.releases, testSHA, "refs/heads/feature/foo")
	return fixture
}

func writeRelease(t *testing.T, releasesDir, sha, ref string) {
	writeReleaseWithSubject(t, releasesDir, sha, ref, "")
}

func writeReleaseWithSubject(t *testing.T, releasesDir, sha, ref, subject string) {
	t.Helper()
	dir := filepath.Join(releasesDir, sha)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "release.sha"), []byte(sha+"\n"), 0644)
	writeFile(t, filepath.Join(dir, "release.ref"), []byte(ref+"\n"), 0644)
	if subject != "" {
		writeFile(t, filepath.Join(dir, "release.subject"), []byte(subject+"\n"), 0644)
	}
	writeFile(t, filepath.Join(dir, "roadmap.env"), []byte("HELM_RELEASE_SHA="+sha+"\nROADMAP_RELEASE_SHA="+sha+"\n"), 0644)
	helm := []byte("helm binary")
	codex := []byte("codex binary")
	writeFile(t, filepath.Join(dir, "helm"), helm, 0755)
	writeFile(t, filepath.Join(dir, "codex"), codex, 0755)
	writeFile(t, filepath.Join(dir, "helm.sha256"), []byte(checksumLine(helm, "helm")), 0644)
	writeFile(t, filepath.Join(dir, "codex.sha256"), []byte(checksumLine(codex, "codex")), 0644)
	refBytes := []byte(ref + "\n")
	manifest := "roadmap-release-manifest-v1\nrelease.ref\t" + fmt.Sprint(len(refBytes)) + "\t" + digest(refBytes) + "\n"
	if subject != "" {
		subjectBytes := []byte(subject + "\n")
		manifest += "release.subject\t" + fmt.Sprint(len(subjectBytes)) + "\t" + digest(subjectBytes) + "\n"
	}
	writeFile(t, filepath.Join(dir, "release.manifest"), []byte(manifest), 0644)
	writeFile(t, filepath.Join(dir, "release.manifest.sig"), bytes.Repeat([]byte{1}, 64), 0644)
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func checksumLine(data []byte, name string) string { return digest(data) + "  " + name + "\n" }

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestListReleasesValidatesLayoutAndCurrent(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	badSHA := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeRelease(t, fixture.releases, badSHA, "refs/heads/beta")
	writeFile(t, filepath.Join(fixture.releases, badSHA, "codex.sha256"), []byte("not a checksum\n"), 0644)
	if err := os.Symlink(filepath.Join(fixture.releases, testSHA), filepath.Join(fixture.releases, "cccccccccccccccccccccccccccccccccccccccc")); err != nil {
		t.Fatal(err)
	}
	response, err := fixture.broker.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Releases) != 1 || response.Releases[0].SHA != testSHA || response.Releases[0].Ref != "refs/heads/feature/foo" || !response.Releases[0].Current {
		t.Fatalf("releases = %#v current=%q", response.Releases, response.CurrentSHA)
	}
	if response.CurrentSHA != testSHA {
		t.Fatalf("current SHA = %q", response.CurrentSHA)
	}
}

func TestListReleasesIncludesOptionalCommitSubject(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	writeReleaseWithSubject(t, fixture.releases, alternateSHA, "refs/heads/feature/foo", "Show commit subjects in beta switcher")

	response, err := fixture.broker.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Releases) != 2 {
		t.Fatalf("releases = %#v", response.Releases)
	}
	for _, release := range response.Releases {
		if release.SHA == alternateSHA && release.Subject != "Show commit subjects in beta switcher" {
			t.Fatalf("subject = %q, want retained commit subject", release.Subject)
		}
		if release.SHA == testSHA && release.Subject != "" {
			t.Fatalf("legacy subject = %q, want empty", release.Subject)
		}
	}
}

func TestListReleasesRejectsInvalidCommitSubjectMetadata(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	writeReleaseWithSubject(t, fixture.releases, alternateSHA, "refs/heads/feature/foo", "invalid\nsubject")

	response, err := fixture.broker.ListReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Releases) != 1 || response.Releases[0].SHA != testSHA {
		t.Fatalf("releases = %#v, want only legacy release", response.Releases)
	}
}

func TestValidCommitSubjectEnforcesCanonicalBoundaries(t *testing.T) {
	for name, subject := range map[string]string{
		"empty":          "",
		"leading space":  " subject",
		"trailing space": "subject ",
		"newline":        "subject\nwith newline",
		"control":        "subject\x00with control",
		"line separator": "subject\u2028with separator",
		"too long":       strings.Repeat("x", MaxCommitSubjectBytes+1),
	} {
		if ValidCommitSubject(subject) {
			t.Errorf("%s subject unexpectedly accepted", name)
		}
	}
	if !ValidCommitSubject("a UTF-8 subject — safely retained") {
		t.Fatal("valid UTF-8 commit subject rejected")
	}
	if !ValidCommitSubject(strings.Repeat("é", MaxCommitSubjectBytes/2)) {
		t.Fatal("UTF-8 subject at the byte limit rejected")
	}
	if ValidCommitSubject(strings.Repeat("é", MaxCommitSubjectBytes/2+1)) {
		t.Fatal("UTF-8 subject over the byte limit accepted")
	}
}

func TestValidateBinaryStreamsAndEnforcesSizeLimit(t *testing.T) {
	dir := t.TempDir()
	data := []byte("streamed executable")
	writeFile(t, filepath.Join(dir, "helm"), data, 0755)
	writeFile(t, filepath.Join(dir, "helm.sha256"), []byte(checksumLine(data, "helm")), 0644)

	if err := validateBinaryWithLimit(dir, "helm", int64(len(data))); err != nil {
		t.Fatalf("binary at size limit rejected: %v", err)
	}
	if err := validateBinaryWithLimit(dir, "helm", int64(len(data)-1)); err == nil {
		t.Fatal("binary over size limit was accepted")
	}
}

func TestHTTPRejectsMalformedAndOversizedRequests(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	server := httptest.NewServer(fixture.broker)
	defer server.Close()
	request := func(method, path, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, body := range []string{
		`{"sha":"` + testSHA + `","extra":"no"}`,
		`{"sha":"` + testSHA + `"}{"sha":"` + testSHA + `"}`,
		`{"sha":"` + testSHA + `","sha":"` + testSHA + `"}`,
		`{"sha":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`,
	} {
		response := request(http.MethodPost, "/v1/switch", body)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed body status = %d", response.StatusCode)
		}
		_ = response.Body.Close()
	}
	response := request(http.MethodPost, "/v1/switch", `{"sha":"`+testSHA+`","padding":"`+string(bytes.Repeat([]byte{'x'}, 5000))+`"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = request(http.MethodGet, "/v1/jobs/../etc", "")
	if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusNotFound {
		t.Fatalf("unsafe job path status = %d", response.StatusCode)
	}
	_ = response.Body.Close()
}

func TestJobsAreDurableAndSerialized(t *testing.T) {
	writeTarget := alternateSHA
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	var firstOnce atomic.Bool
	fixture := newFixture(t, func(ctx context.Context, path, sha string) error {
		if path == "" || sha != writeTarget {
			t.Errorf("executor received path=%q sha=%q", path, sha)
		}
		count := active.Add(1)
		for {
			old := maximum.Load()
			if count <= old || maximum.CompareAndSwap(old, count) {
				break
			}
		}
		if firstOnce.CompareAndSwap(false, true) {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
			}
		}
		active.Add(-1)
		return nil
	})
	defer fixture.broker.Close()
	writeRelease(t, fixture.releases, writeTarget, "refs/heads/feature/foo")
	// The callback above only needs to establish serialization; the fixed path
	// assertion is exercised by the separate command-path test below.
	job1, err := fixture.broker.RequestSwitch(context.Background(), writeTarget)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first job did not start")
	}
	job2, err := fixture.broker.RequestSwitch(context.Background(), writeTarget)
	if err != nil {
		t.Fatal(err)
	}
	if job1.ID != job2.ID || job1.State != JobQueued || (job2.State != JobQueued && job2.State != JobRunning) {
		t.Fatalf("queued jobs = %#v %#v", job1, job2)
	}
	close(releaseFirst)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got1, err1 := fixture.broker.GetJob(context.Background(), job1.ID)
		got2, err2 := fixture.broker.GetJob(context.Background(), job2.ID)
		if err1 == nil && err2 == nil && got1.State == JobSucceeded && got2.State == JobSucceeded {
			if maximum.Load() != 1 {
				t.Fatalf("maximum concurrent executions = %d", maximum.Load())
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("jobs did not finish: %v %v", job1.ID, job2.ID)
}

func TestCurrentSwitchCreatesDurableNoOp(t *testing.T) {
	var executions atomic.Int32
	fixture := newFixture(t, func(context.Context, string, string) error {
		executions.Add(1)
		return nil
	})
	defer fixture.broker.Close()

	first, err := fixture.broker.RequestSwitch(context.Background(), testSHA)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != JobSucceeded || first.TargetSHA != testSHA || first.Error != "" {
		t.Fatalf("no-op job = %#v", first)
	}
	second, err := fixture.broker.RequestSwitch(context.Background(), testSHA)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("repeated current request = %#v, want %#v", second, first)
	}
	status, err := fixture.broker.GetJob(context.Background(), first.ID)
	if err != nil || status != first {
		t.Fatalf("durable no-op status = %#v, %v", status, err)
	}
	if executions.Load() != 0 {
		t.Fatalf("no-op invoked rollback %d times", executions.Load())
	}
}

func TestConcurrentSwitchAdmissionReturnsOneDurableJob(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce atomic.Bool
	var executions atomic.Int32
	fixture := newFixture(t, func(ctx context.Context, path, sha string) error {
		if path == "" || sha != alternateSHA {
			t.Errorf("executor received path=%q sha=%q", path, sha)
		}
		executions.Add(1)
		if startedOnce.CompareAndSwap(false, true) {
			close(started)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	defer fixture.broker.Close()
	writeRelease(t, fixture.releases, alternateSHA, "refs/heads/feature/foo")
	// These entries exercise the admission scan's malformed/symlink handling.
	writeFile(t, filepath.Join(fixture.state, "22222222222222222222222222222222.json"), []byte("not-json"), 0600)
	if err := os.Symlink(filepath.Join(fixture.root, "outside-job"), filepath.Join(fixture.state, "33333333333333333333333333333333.json")); err != nil {
		t.Fatal(err)
	}

	const callers = 24
	start := make(chan struct{})
	jobs := make(chan Job, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			job, err := fixture.broker.RequestSwitch(context.Background(), alternateSHA)
			if err != nil {
				errs <- err
				return
			}
			jobs <- job
		}()
	}
	close(start)
	var first Job
	for i := 0; i < callers; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case job := <-jobs:
			if i == 0 {
				first = job
			} else if job.ID != first.ID {
				t.Fatalf("request %d returned job %q, want %q", i, job.ID, first.ID)
			}
		}
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("deduplicated job did not start")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := fixture.broker.GetJob(context.Background(), first.ID)
		if err == nil && status.State == JobSucceeded {
			if executions.Load() != 1 {
				t.Fatalf("rollback executions = %d, want 1", executions.Load())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("deduplicated job did not finish")
}

func TestQueuedJobSurvivesBrokerRestart(t *testing.T) {
	var executions atomic.Int32
	fixture := newFixture(t, func(context.Context, string, string) error {
		executions.Add(1)
		return nil
	})
	writeRelease(t, fixture.releases, alternateSHA, "refs/heads/feature/foo")
	old := fixture.broker
	queued := Job{ID: "11111111111111111111111111111111", TargetSHA: alternateSHA, State: JobQueued}
	if err := old.writeJob(queued); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce atomic.Bool
	cfg := old.cfg
	cfg.Sleep = func(ctx context.Context, _ time.Duration) error {
		if enteredOnce.CompareAndSwap(false, true) {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	restarted, err := NewBroker(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	recovered, err := restarted.RequestSwitch(context.Background(), alternateSHA)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ID != queued.ID || (recovered.State != JobQueued && recovered.State != JobRunning) {
		t.Fatalf("recovered job = %#v, want durable id %q", recovered, queued.ID)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("queued job was not recovered by restarted broker")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := restarted.GetJob(context.Background(), queued.ID)
		if err == nil && status.State == JobSucceeded {
			if executions.Load() != 1 {
				t.Fatalf("recovered rollback executions = %d, want 1", executions.Load())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("recovered job did not finish")
}

func TestStartupGraceRunsAfterRunningState(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error {
		return nil
	})
	defer fixture.broker.Close()
	writeRelease(t, fixture.releases, alternateSHA, "refs/heads/feature/foo")
	entered := make(chan struct{})
	release := make(chan struct{})
	var once atomic.Bool
	fixture.broker.cfg.StartupGrace = time.Second
	fixture.broker.cfg.Sleep = func(ctx context.Context, duration time.Duration) error {
		if duration != time.Second {
			t.Errorf("startup grace = %s, want 1s", duration)
		}
		if once.CompareAndSwap(false, true) {
			close(entered)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	job, err := fixture.broker.RequestSwitch(context.Background(), alternateSHA)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("startup grace was not entered")
	}
	status, err := fixture.broker.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != JobRunning {
		t.Fatalf("job state during grace = %q, want %q", status.State, JobRunning)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err = fixture.broker.GetJob(context.Background(), job.ID)
		if err == nil && status.State == JobSucceeded {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("job did not succeed after grace: %#v", status)
}

func TestStateDirectoryAndJobFilesMustBeOwnedRegularFiles(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	jobID := "0123456789abcdef0123456789abcdef"
	jobPath := filepath.Join(fixture.state, jobID+".json")
	if err := os.Symlink(filepath.Join(fixture.root, "outside-job"), jobPath); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.broker.GetJob(context.Background(), jobID); codeOf(err) != ErrStateFailure {
		t.Fatalf("job symlink error = %v", err)
	}
	if err := os.Remove(jobPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobPath, []byte(`{"id":"0123456789abcdef0123456789abcdef","target_sha":"`+testSHA+`","state":"queued"}`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.broker.cfg.OwnerCheck = func(path string, _ os.FileInfo) bool {
		return path == fixture.state
	}
	if _, err := fixture.broker.GetJob(context.Background(), jobID); codeOf(err) != ErrStateFailure {
		t.Fatalf("unowned job error = %v", err)
	}
	if err := os.Rename(fixture.state, fixture.state+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fixture.state+".real", fixture.state); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.broker.GetJob(context.Background(), jobID); codeOf(err) != ErrStateFailure {
		t.Fatalf("state directory symlink error = %v", err)
	}
}
func TestSocketClientAndPeerFilter(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	listener, err := fixture.broker.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Handler: fixture.broker}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	info, err := os.Stat(fixture.broker.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}
	client, err := NewClient(fixture.broker.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.ListReleases(context.Background())
	if err != nil || len(response.Releases) != 1 {
		t.Fatalf("client list = %#v, %v", response, err)
	}
	job, err := client.RequestSwitch(context.Background(), testSHA)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		got, err := client.GetJob(context.Background(), job.ID)
		if err == nil && got.State == JobSucceeded {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client job did not finish")
}

func TestClientResponseLimit(t *testing.T) {
	client, err := NewClient("/tmp/beta-switch-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	client.MaxResponseBytes = 8
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{'x'}, 9)))}, nil
	})}
	_, err = client.ListReleases(context.Background())
	if codeOf(err) != ErrInternal {
		t.Fatalf("response limit error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPeerListenerRejectsUnauthorized(t *testing.T) {
	fixture := newFixture(t, func(context.Context, string, string) error { return nil })
	defer fixture.broker.Close()
	var calls atomic.Int32
	fixture.broker.cfg.PeerCheck = func(net.Conn) (bool, error) {
		return calls.Add(1) > 1, nil
	}
	listener, err := fixture.broker.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		_, _ = listener.Accept()
	}()
	conn, err := net.Dial("unix", fixture.broker.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	conn, err = net.Dial("unix", fixture.broker.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() < 2 {
		t.Fatalf("peer check calls = %d", calls.Load())
	}
}

func TestClientRejectsUnsafeIDsLocally(t *testing.T) {
	client, err := NewClient("/tmp/beta-switch-test.sock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetJob(context.Background(), "../job"); codeOf(err) != ErrInvalidRequest {
		t.Fatalf("unsafe job id error = %v", err)
	}
	if _, err := client.RequestSwitch(context.Background(), "ABC"); codeOf(err) != ErrInvalidRequest {
		t.Fatalf("unsafe SHA error = %v", err)
	}
}

func TestRootOwnerDefaultRejectsUnownedFixture(t *testing.T) {
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	if err := os.MkdirAll(releases, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBroker(Config{ReleasesDir: releases, StateDir: filepath.Join(root, "state"), SocketPath: filepath.Join(root, "sock"), SocketGroup: "0"}); codeOf(err) != ErrStateFailure {
		t.Fatalf("unowned state dir error = %v", err)
	}
}

var _ Client = (*UnixClient)(nil)
var _ Client = (*Broker)(nil)
