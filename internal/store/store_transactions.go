package store

import (
	"context"
	"database/sql"
)

// Shared write-transaction entrypoints. Every store mutation that writes an
// event runs through withTx, withImmediateTx or withImmediateDependencyTx;
// after a successful commit each one wakes the neglect-signal projector
// (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D4). The wake is a non-blocking signal
// sent only after COMMIT returns, so the projector always sees the write and a
// writer is never delayed or failed by it. Writes from another process are
// found by the projector's ticker instead.

// dependencySQL is implemented by *sql.DB, *sql.Tx, and *sql.Conn. Dependency
// mutations use a dedicated *sql.Conn so they can explicitly issue
// BEGIN IMMEDIATE before any graph validation read.
type dependencySQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.afterCommit()
	return nil
}

// withImmediateDependencyTx is the dependency-specific transaction entrypoint.
// database/sql's ordinary BeginTx starts a deferred transaction, which would
// let opposing cycle checks both read the same pre-write graph and then race
// at insertion. A dedicated connection and BEGIN IMMEDIATE establish the
// serialization point before the first validation read.
func (s *Store) withImmediateDependencyTx(ctx context.Context, fn func(dependencySQL) error) error {
	if err := s.immediateTx(ctx, fn); err != nil {
		return err
	}
	s.afterCommit()
	return nil
}

// withImmediateTx is the shared immediate-writer transaction entrypoint for
// bounded mutations that must validate task state before their first write.
// It uses the same dedicated-connection implementation as dependency graph
// mutations while keeping the dependency-specific helper name available to
// its existing callers.
func (s *Store) withImmediateTx(ctx context.Context, fn func(dependencySQL) error) error {
	return s.withImmediateDependencyTx(ctx, fn)
}

// immediateTx runs fn in a BEGIN IMMEDIATE transaction on a dedicated
// connection without waking the projector. The projector writes its own rows
// through it so its commits do not wake itself.
func (s *Store) immediateTx(ctx context.Context, fn func(dependencySQL) error) (retErr error) {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// Preserve the original function/commit error. Rollback is best effort;
		// the connection is closed immediately afterwards and returned to the
		// pool only once SQLite has had a chance to unwind the transaction.
		_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	committed = true
	return nil
}

// afterCommit wakes the attached projector, if any. It never blocks.
func (s *Store) afterCommit() {
	if projector := s.activitySignal.Load(); projector != nil {
		projector.Wake()
	}
}
