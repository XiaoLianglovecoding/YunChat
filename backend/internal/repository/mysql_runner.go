package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"my-im/internal/model"
)

var ErrConflict = errors.New("repository conflict")

type QueryObserver func(elapsed time.Duration, err error)

type sqlRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type timedRunner struct {
	inner    sqlRunner
	timeout  time.Duration
	observer QueryObserver
}

func newTimedRunner(inner sqlRunner, timeout time.Duration, observer QueryObserver) sqlRunner {
	return &timedRunner{inner: inner, timeout: timeout, observer: observer}
}

func (r *timedRunner) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, cancel := withShorterTimeout(ctx, r.timeout)
	defer cancel()
	started := time.Now()
	result, err := r.inner.ExecContext(ctx, query, args...)
	err = mapMySQLError(err)
	r.observe(started, err)
	return result, err
}

func (r *timedRunner) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	// Rows 持有 context；不能在返回前 cancel。计时器在 timeout 到期后自动释放。
	queryCtx, _ := withShorterTimeout(ctx, r.timeout)
	started := time.Now()
	rows, err := r.inner.QueryContext(queryCtx, query, args...)
	err = mapMySQLError(err)
	r.observe(started, err)
	return rows, err
}

func (r *timedRunner) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	queryCtx, _ := withShorterTimeout(ctx, r.timeout)
	started := time.Now()
	row := r.inner.QueryRowContext(queryCtx, query, args...)
	r.observe(started, nil)
	return row
}

func (r *timedRunner) observe(started time.Time, err error) {
	if r.observer != nil {
		r.observer(time.Since(started), err)
	}
}

func withShorterTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func mapMySQLError(err error) error {
	if err == nil {
		return nil
	}
	var mysqlErr *drivermysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (m *MySQLRepoImpl) WithinTransaction(ctx context.Context, fn func(context.Context, MySQLRepository) error) error {
	if m.root == nil {
		return errors.New("transaction requires a root database handle")
	}
	tx, err := m.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	child := &MySQLRepoImpl{
		db: newTimedRunner(tx, m.queryTimeout, m.observer), root: m.root,
		queryTimeout: m.queryTimeout, observer: m.observer, tx: tx,
	}
	if err := fn(ctx, child); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (m *MySQLRepoImpl) UpsertMessageUserState(ctx context.Context, state *model.MessageUserState) error {
	const query = `INSERT INTO message_user_states(user_id,conv_id,msg_id,deleted_at)
	VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE deleted_at=VALUES(deleted_at)`
	if _, err := m.db.ExecContext(ctx, query, state.UserID, state.ConvID, state.MsgID, state.DeletedAt); err != nil {
		return fmt.Errorf("upsert message user state: %w", err)
	}
	return nil
}

var _ MySQLRepository = (*MySQLRepoImpl)(nil)
