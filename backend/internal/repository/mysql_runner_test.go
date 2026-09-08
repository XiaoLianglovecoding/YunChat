package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"

	"my-im/internal/messageid"
	"my-im/internal/model"
)

func TestMapMySQLDuplicateKey(t *testing.T) {
	err := mapMySQLError(&drivermysql.MySQLError{Number: 1062, Message: "duplicate"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

type blockingRunner struct{}

func (blockingRunner) ExecContext(ctx context.Context, _ string, _ ...any) (sql.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (blockingRunner) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unused")
}
func (blockingRunner) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func TestTimedRunnerCancelsSlowQuery(t *testing.T) {
	runner := newTimedRunner(blockingRunner{}, 20*time.Millisecond, nil)
	started := time.Now()
	_, err := runner.ExecContext(context.Background(), "slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("query timeout was not enforced")
	}
}

var registerFakeDriver sync.Once
var fakeCommits atomic.Int64
var fakeRollbacks atomic.Int64

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error)                          { return nil, errors.New("prepare unsupported") }
func (fakeConn) Close() error                                                 { return nil }
func (fakeConn) Begin() (driver.Tx, error)                                    { return fakeTx{}, nil }
func (fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return fakeTx{}, nil }
func (fakeConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return fakeResult(1), nil
}
func (fakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &fakeRows{}, nil
}

type fakeTx struct{}

func (fakeTx) Commit() error   { fakeCommits.Add(1); return nil }
func (fakeTx) Rollback() error { fakeRollbacks.Add(1); return nil }

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fakeResult) RowsAffected() (int64, error) { return int64(r), nil }

type fakeRows struct{}

func (*fakeRows) Columns() []string         { return []string{"id"} }
func (*fakeRows) Close() error              { return nil }
func (*fakeRows) Next([]driver.Value) error { return io.EOF }

func fakeDB(t *testing.T) *sql.DB {
	t.Helper()
	registerFakeDriver.Do(func() { sql.Register("my-im-fake", fakeDriver{}) })
	db, err := sql.Open("my-im-fake", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWithinTransactionCommitAndRollback(t *testing.T) {
	fakeCommits.Store(0)
	fakeRollbacks.Store(0)
	repo := NewMySQLRepository(fakeDB(t), time.Second, nil)
	err := repo.WithinTransaction(context.Background(), func(ctx context.Context, txRepo MySQLRepository) error {
		return txRepo.CreateUser(ctx, &model.User{Username: "alice", PasswordHash: "hash"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if fakeCommits.Load() != 1 {
		t.Fatalf("commits = %d", fakeCommits.Load())
	}
	want := errors.New("business failed")
	err = repo.WithinTransaction(context.Background(), func(context.Context, MySQLRepository) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if fakeRollbacks.Load() == 0 {
		t.Fatal("expected rollback")
	}
}

func TestMessagePersistenceRejectsIDsOutsideBrowserSafeRange(t *testing.T) {
	repo := NewMySQLRepository(fakeDB(t), time.Second, nil)
	for _, id := range []int64{0, -1, messageid.MaxID + 1} {
		if err := repo.InsertPrivateMessage(context.Background(), &model.PrivateMessage{ID: id}); err == nil {
			t.Fatalf("private message ID %d unexpectedly accepted", id)
		}
		if err := repo.InsertGroupMessage(context.Background(), &model.GroupMessage{ID: id}); err == nil {
			t.Fatalf("group message ID %d unexpectedly accepted", id)
		}
	}
	if err := repo.InsertPrivateMessage(context.Background(), nil); err == nil {
		t.Fatal("nil private message unexpectedly accepted")
	}
	if err := repo.InsertGroupMessage(context.Background(), nil); err == nil {
		t.Fatal("nil group message unexpectedly accepted")
	}
	if err := repo.InsertPrivateMessage(context.Background(), &model.PrivateMessage{ID: messageid.MaxID}); err != nil {
		t.Fatalf("browser-safe private message ID rejected: %v", err)
	}
	if err := repo.InsertGroupMessage(context.Background(), &model.GroupMessage{ID: messageid.MaxID}); err != nil {
		t.Fatalf("browser-safe group message ID rejected: %v", err)
	}
}
