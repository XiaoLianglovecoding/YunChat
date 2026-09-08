package messageid

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const DefaultNamespace = "message"

// MySQLStore persists the first not-yet-reserved ID for one namespace.
// Every application instance must point at the same writable MySQL primary.
type MySQLStore struct {
	db        *sql.DB
	namespace string
	timeout   time.Duration
	observer  func(time.Duration, error)
}

type MySQLStoreOptions struct {
	Timeout  time.Duration
	Observer func(time.Duration, error)
}

func NewMySQLStore(db *sql.DB, namespace string) (*MySQLStore, error) {
	return NewMySQLStoreWithOptions(db, namespace, MySQLStoreOptions{})
}

func NewMySQLStoreWithOptions(db *sql.DB, namespace string, options MySQLStoreOptions) (*MySQLStore, error) {
	if db == nil {
		return nil, errors.New("message ID MySQL handle is nil")
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || len(namespace) > 64 {
		return nil, errors.New("message ID namespace must contain 1 to 64 bytes")
	}
	if options.Timeout < 0 {
		return nil, errors.New("message ID MySQL timeout must not be negative")
	}
	if options.Timeout == 0 {
		options.Timeout = 3 * time.Second
	}
	return &MySQLStore{
		db: db, namespace: namespace, timeout: options.Timeout, observer: options.Observer,
	}, nil
}

// Validate performs a read-only startup check. It catches a missing migration,
// a deleted allocator row and an exhausted/corrupt high-water mark before the
// HTTP server starts, without consuming an ID segment.
func (s *MySQLStore) Validate(ctx context.Context) (err error) {
	ctx, cancel := withStoreTimeout(ctx, s.timeout)
	defer cancel()
	started := time.Now()
	defer func() { s.observe(started, err) }()

	var next int64
	err = s.db.QueryRowContext(ctx,
		`SELECT next_id FROM message_id_allocators WHERE namespace = ?`, s.namespace,
	).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("message ID namespace %q is not initialized; run migrations", s.namespace)
	}
	if err != nil {
		return fmt.Errorf("validate message ID allocator %q: %w", s.namespace, err)
	}
	if next < 1 {
		return fmt.Errorf("validate message ID allocator %q: invalid next_id %d", s.namespace, next)
	}
	if next > MaxID {
		return ErrExhausted
	}
	return nil
}

// Reserve advances the durable high-water mark before returning the segment.
// SELECT ... FOR UPDATE serializes reservations made by different processes.
func (s *MySQLStore) Reserve(ctx context.Context, size int64) (segment Segment, err error) {
	if size < 1 || size > MaxSegmentSize {
		return Segment{}, fmt.Errorf("message ID segment size must be between 1 and %d", MaxSegmentSize)
	}
	ctx, cancel := withStoreTimeout(ctx, s.timeout)
	defer cancel()
	started := time.Now()
	defer func() { s.observe(started, err) }()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Segment{}, fmt.Errorf("begin message ID reservation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var first int64
	err = tx.QueryRowContext(ctx,
		`SELECT next_id FROM message_id_allocators WHERE namespace = ? FOR UPDATE`,
		s.namespace,
	).Scan(&first)
	if errors.Is(err, sql.ErrNoRows) {
		return Segment{}, fmt.Errorf("message ID namespace %q is not initialized; run migrations", s.namespace)
	}
	if err != nil {
		return Segment{}, fmt.Errorf("lock message ID allocator %q: %w", s.namespace, err)
	}
	if first < 1 || first > MaxID {
		return Segment{}, ErrExhausted
	}
	remaining := MaxID - first + 1
	if size > remaining {
		size = remaining
	}

	last := first + size - 1
	next := last + 1
	result, err := tx.ExecContext(ctx,
		`UPDATE message_id_allocators SET next_id = ?, updated_at = UTC_TIMESTAMP(6) WHERE namespace = ?`,
		next, s.namespace,
	)
	if err != nil {
		return Segment{}, fmt.Errorf("advance message ID allocator %q: %w", s.namespace, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Segment{}, fmt.Errorf("inspect message ID allocator update %q: %w", s.namespace, err)
	}
	if rows != 1 {
		return Segment{}, fmt.Errorf("advance message ID allocator %q: updated %d rows", s.namespace, rows)
	}
	if err := tx.Commit(); err != nil {
		return Segment{}, fmt.Errorf("commit message ID reservation %q: %w", s.namespace, err)
	}
	committed = true
	return Segment{First: first, Last: last}, nil
}

func (s *MySQLStore) observe(started time.Time, err error) {
	if s.observer != nil {
		s.observer(time.Since(started), err)
	}
}

func withStoreTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
