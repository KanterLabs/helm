package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestAcquireWriterLockDiscardsConnectionAfterCleanupFailure(t *testing.T) {
	rollbackErr := errors.New("rollback failed")
	resetErr := errors.New("busy timeout reset failed")
	for _, test := range []struct {
		name        string
		rollbackErr error
		resetErr    error
		wantErr     error
	}{
		{name: "rollback", rollbackErr: rollbackErr, wantErr: rollbackErr},
		{name: "reset", resetErr: resetErr, wantErr: resetErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			testDriver := &writerLockTestDriver{rollbackErr: test.rollbackErr, resetErr: test.resetErr}
			database := sql.OpenDB(writerLockTestConnector{driver: testDriver})
			defer database.Close()
			if err := acquireWriterLock(context.Background(), database); err == nil || !errors.Is(err, test.wantErr) {
				t.Fatalf("acquireWriterLock() error = %v, want %v", err, test.wantErr)
			}
			if !testDriver.lastConnectionClosed() {
				t.Fatal("cleanup failure returned a tainted physical connection to the pool")
			}
		})
	}
}

type writerLockTestConnector struct {
	driver *writerLockTestDriver
}

func (c writerLockTestConnector) Connect(context.Context) (driver.Conn, error) {
	connection := &writerLockTestConn{parent: c.driver, busyTimeout: 5000}
	c.driver.mu.Lock()
	c.driver.connections = append(c.driver.connections, connection)
	c.driver.mu.Unlock()
	return connection, nil
}

func (c writerLockTestConnector) Driver() driver.Driver {
	return c.driver
}

type writerLockTestDriver struct {
	mu          sync.Mutex
	rollbackErr error
	resetErr    error
	connections []*writerLockTestConn
}

func (d *writerLockTestDriver) Open(string) (driver.Conn, error) {
	return writerLockTestConnector{driver: d}.Connect(context.Background())
}

func (d *writerLockTestDriver) lastConnectionClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.connections) > 0 && d.connections[len(d.connections)-1].closed
}

type writerLockTestConn struct {
	parent      *writerLockTestDriver
	busyTimeout int64
	closed      bool
}

func (c *writerLockTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}

func (c *writerLockTestConn) Close() error {
	c.parent.mu.Lock()
	c.closed = true
	c.parent.mu.Unlock()
	return nil
}

func (c *writerLockTestConn) Begin() (driver.Tx, error) {
	return writerLockTestTx{}, nil
}

func (c *writerLockTestConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &writerLockTestRows{value: c.busyTimeout}, nil
}

func (c *writerLockTestConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch query {
	case "PRAGMA busy_timeout = 250":
		c.busyTimeout = 250
		return writerLockTestResult{}, nil
	case "BEGIN IMMEDIATE":
		return writerLockTestResult{}, nil
	case "ROLLBACK":
		if c.parent.rollbackErr != nil {
			return nil, c.parent.rollbackErr
		}
		return writerLockTestResult{}, nil
	default:
		if strings.HasPrefix(query, "PRAGMA busy_timeout = ") {
			if c.parent.resetErr != nil {
				return nil, c.parent.resetErr
			}
			return writerLockTestResult{}, nil
		}
		return nil, errors.New("unexpected writer-lock query")
	}
}

type writerLockTestRows struct {
	value int64
	done  bool
}

func (r *writerLockTestRows) Columns() []string { return []string{"busy_timeout"} }

func (r *writerLockTestRows) Close() error { return nil }

func (r *writerLockTestRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = r.value
	return nil
}

type writerLockTestResult struct{}

func (writerLockTestResult) LastInsertId() (int64, error) { return 0, nil }

func (writerLockTestResult) RowsAffected() (int64, error) { return 0, nil }

type writerLockTestTx struct{}

func (writerLockTestTx) Commit() error { return nil }

func (writerLockTestTx) Rollback() error { return nil }
