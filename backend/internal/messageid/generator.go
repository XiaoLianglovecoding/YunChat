// Package messageid provides the single source of server-side message IDs.
//
// IDs are allocated in durable MySQL segments and then handed out from memory.
// A process crash may therefore leave gaps, but a committed segment is never
// handed out again. Message IDs are identifiers, not gap-free counters.
package messageid

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	// MaxID is JavaScript's largest exactly representable integer. The current
	// HTTP/WebSocket contracts encode IDs as JSON numbers, so staying below this
	// limit prevents browsers from silently rounding an int64 message ID.
	MaxID int64 = 1<<53 - 1

	// DefaultSegmentSize keeps the hot path in memory. At the documented target
	// of 100,000 IDs/second it needs about 1.53 MySQL reservations per second.
	DefaultSegmentSize int64 = 65_536

	// MaxSegmentSize limits the number of IDs skipped when an instance crashes
	// immediately after reserving a segment.
	MaxSegmentSize int64 = 1_000_000
)

var (
	ErrExhausted      = errors.New("message ID space exhausted")
	ErrInvalidSegment = errors.New("message ID store returned an invalid segment")
)

// Segment is an inclusive, durable range of message IDs.
type Segment struct {
	First int64
	Last  int64
}

// SegmentStore must never return overlapping committed segments. MySQLStore
// implements this contract with a row lock and a durable high-water mark.
type SegmentStore interface {
	Reserve(context.Context, int64) (Segment, error)
}

type Options struct {
	SegmentSize int64
}

// Generator serves IDs from one local segment. Generator is safe for concurrent
// use by every goroutine in one application instance.
type Generator struct {
	mu          sync.Mutex
	store       SegmentStore
	segmentSize int64
	next        int64
	last        int64
}

func New(store SegmentStore, options Options) (*Generator, error) {
	if store == nil {
		return nil, errors.New("message ID segment store is nil")
	}
	segmentSize := options.SegmentSize
	if segmentSize == 0 {
		segmentSize = DefaultSegmentSize
	}
	if segmentSize < 1 || segmentSize > MaxSegmentSize {
		return nil, fmt.Errorf("message ID segment size must be between 1 and %d", MaxSegmentSize)
	}
	return &Generator{store: store, segmentSize: segmentSize}, nil
}

// Next returns one positive, globally unique, browser-safe message ID.
//
// The mutex intentionally remains held during the rare segment refill. This
// makes it impossible for two goroutines in one process to install or consume
// the same segment while keeping the ordinary path to one small critical
// section and one integer increment.
func (g *Generator) Next(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	if g.next == 0 || g.next > g.last {
		segment, err := g.store.Reserve(ctx, g.segmentSize)
		if err != nil {
			return 0, fmt.Errorf("reserve message ID segment: %w", err)
		}
		if err := validateSegment(segment, g.segmentSize); err != nil {
			return 0, err
		}
		g.next = segment.First
		g.last = segment.Last
	}

	id := g.next
	g.next++
	return id, nil
}

func validateSegment(segment Segment, expectedSize int64) error {
	if segment.First < 1 || segment.Last < segment.First || segment.Last > MaxID {
		return fmt.Errorf("%w: [%d,%d]", ErrInvalidSegment, segment.First, segment.Last)
	}
	actualSize := segment.Last - segment.First + 1
	if actualSize > expectedSize {
		return fmt.Errorf("%w: [%d,%d] has size %d, want at most %d", ErrInvalidSegment,
			segment.First, segment.Last, actualSize, expectedSize)
	}
	return nil
}
