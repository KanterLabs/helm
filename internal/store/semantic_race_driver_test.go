package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanterLabs/helm/internal/db"
	"modernc.org/sqlite"
)

// semanticRaceBarrier is armed only around the operation under test. The
// connector below waits after a deferred transaction's real BEGIN completes,
// or before delegating BEGIN IMMEDIATE. Both paths distinguish the mutation
// transaction from its read-only preflight.
type semanticRaceBarrier struct {
	armed     atomic.Bool
	reached   chan struct{}
	releaseCh chan struct{}
	reachOnce sync.Once
}

func (b *semanticRaceBarrier) arm() {
	b.reached = make(chan struct{})
	b.releaseCh = make(chan struct{})
	b.reachOnce = sync.Once{}
	b.armed.Store(true)
}

func (b *semanticRaceBarrier) waitIfArmed() {
	if !b.armed.Load() {
		return
	}
	b.reachOnce.Do(func() { close(b.reached) })
	<-b.releaseCh
}

func (b *semanticRaceBarrier) release() {
	if b.armed.Swap(false) {
		close(b.releaseCh)
	}
}

var semanticRaceBarriers sync.Map // map[*sql.DB]*semanticRaceBarrier

func semanticRaceBarrierFor(t *testing.T, database *sql.DB) *semanticRaceBarrier {
	t.Helper()
	value, ok := semanticRaceBarriers.Load(database)
	if !ok {
		t.Fatalf("semantic race database has no transaction barrier")
	}
	return value.(*semanticRaceBarrier)
}

// semanticRaceConnector keeps the application SQLite driver intact while
// interposing only on physical connections used by these tests.
type semanticRaceConnector struct {
	driver.Connector
	barrier *semanticRaceBarrier
}

func (c *semanticRaceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &semanticRaceConn{Conn: conn, barrier: c.barrier}, nil
}

type semanticRaceConn struct {
	driver.Conn
	barrier *semanticRaceBarrier
}

func (c *semanticRaceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	var (
		tx  driver.Tx
		err error
	)
	if beginner, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = beginner.BeginTx(ctx, opts)
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	c.barrier.waitIfArmed()
	return tx, nil
}

func (c *semanticRaceConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	prepare, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return prepare.PrepareContext(ctx, query)
}

func (c *semanticRaceConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.EqualFold(strings.TrimSpace(query), "BEGIN IMMEDIATE") {
		c.barrier.waitIfArmed()
	}
	exec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return exec.ExecContext(ctx, query, args)
}

func (c *semanticRaceConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return queryer.QueryContext(ctx, query, args)
}

func (c *semanticRaceConn) Ping(ctx context.Context) error {
	pinger, ok := c.Conn.(driver.Pinger)
	if !ok {
		return driver.ErrSkip
	}
	return pinger.Ping(ctx)
}

func (c *semanticRaceConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.Conn.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx)
}

func (c *semanticRaceConn) IsValid() bool {
	validator, ok := c.Conn.(driver.Validator)
	if !ok {
		return true
	}
	return validator.IsValid()
}

func (c *semanticRaceConn) CheckNamedValue(value *driver.NamedValue) error {
	checker, ok := c.Conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return checker.CheckNamedValue(value)
}

// newSemanticRaceFixture uses a file-backed database initialized by the normal
// application setup, then reopens it through a test-only connector wrapper.
// This keeps the production Store and database setup untouched while allowing
// the tests to observe the mutation transaction boundary.
func newSemanticRaceFixture(t *testing.T, key string) dependencyFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "semantic-race.db")
	bootstrap, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("open semantic race database: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("close semantic race bootstrap database: %v", err)
	}

	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	base, err := sqlite.NewConnector(dsn)
	if err != nil {
		t.Fatalf("create semantic race connector: %v", err)
	}
	barrier := &semanticRaceBarrier{}
	database := sql.OpenDB(&semanticRaceConnector{Connector: base, barrier: barrier})
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(8)
	database.SetConnMaxLifetime(0)
	semanticRaceBarriers.Store(database, barrier)
	t.Cleanup(func() {
		semanticRaceBarriers.Delete(database)
		_ = database.Close()
	})
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping semantic race database: %v", err)
	}

	store := New(database)
	actor, err := store.CreateActor(ctx, Actor{Kind: "human", Name: "Dependency tester"}, "")
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	project, err := store.CreateProject(ctx, ProjectInput{Key: dependencyStringPtr(key), Name: dependencyStringPtr("Dependencies")}, actor.ID)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return dependencyFixture{ctx: ctx, store: store, actor: actor, project: project}
}

func TestSemanticRaceBarrierInterceptsImmediateBegin(t *testing.T) {
	f := newSemanticRaceFixture(t, "SEMRAceBARRIER")
	blocker, err := f.store.DB.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatalf("begin semantic race barrier blocker: %v", err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(f.ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, f.project.ID); err != nil {
		t.Fatalf("acquire semantic race barrier writer lock: %v", err)
	}

	barrier := semanticRaceBarrierFor(t, f.store.DB)
	barrier.arm()
	defer barrier.release()
	result := make(chan error, 1)
	go func() {
		conn, connErr := f.store.DB.Conn(f.ctx)
		if connErr != nil {
			result <- connErr
			return
		}
		defer conn.Close()
		_, execErr := conn.ExecContext(f.ctx, `BEGIN IMMEDIATE`)
		if execErr == nil {
			_, _ = conn.ExecContext(f.ctx, `ROLLBACK`)
		}
		result <- execErr
	}()

	select {
	case <-barrier.reached:
	case err := <-result:
		t.Fatalf("immediate transaction completed before barrier: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("immediate transaction did not reach barrier")
	}
	if err := blocker.Commit(); err != nil {
		t.Fatalf("release semantic race barrier writer lock: %v", err)
	}
	barrier.release()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("immediate transaction after barrier: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("immediate transaction did not finish after barrier release")
	}
}
